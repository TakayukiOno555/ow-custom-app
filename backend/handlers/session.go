package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sessionCookieName はログインセッションIDを入れる Cookie 名。
// 値はランダムトークンで、中身（誰か）は DB の user_sessions テーブルにだけ持つ。
const sessionCookieName = "session_id"

// sessionTTL はログインセッションの有効期間。期限が切れたら再ログインが必要。
const sessionTTL = 30 * 24 * time.Hour

// upsertUser は Google のユーザー情報を users テーブルに保存する。
// google_id が既にあれば email / name を最新に更新し、無ければ新規作成する（upsert）。
// 戻り値は users.id（このアプリ内部の UUID）。
func upsertUser(ctx context.Context, pool *pgxpool.Pool, info googleUserInfo) (string, error) {
	const q = `
		INSERT INTO users (google_id, email, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (google_id) DO UPDATE
		  SET email = EXCLUDED.email,
		      name  = EXCLUDED.name
		RETURNING id`

	var userID string
	err := pool.QueryRow(ctx, q, info.Sub, info.Email, info.Name).Scan(&userID)
	return userID, err
}

// createUserSession は user_sessions に1行作り、ランダムなセッションIDを返す。
// このIDが Cookie に入る値になる。
func createUserSession(ctx context.Context, pool *pgxpool.Pool, userID string) (string, time.Time, error) {
	sessionID, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(sessionTTL)

	const q = `INSERT INTO user_sessions (id, user_id, expires_at) VALUES ($1, $2, $3)`
	if _, err := pool.Exec(ctx, q, sessionID, userID, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return sessionID, expiresAt, nil
}

// setSessionCookie はセッションIDを HttpOnly Cookie としてブラウザに保存させる。
func setSessionCookie(w http.ResponseWriter, sessionID string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,                 // JS から読めない（XSS でトークンを盗まれにくくする）
		SameSite: http.SameSiteLaxMode, // 別サイトからの遷移でも基本送られる（OAuth 戻りに必要）
		// 本番（HTTPS）では Secure: true を付けたい。ローカルは http なので今は付けない。
	})
}

// sessionUser はセッションCookieから特定したログインユーザーの情報。
type sessionUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// userFromRequest は Cookie のセッションIDを使って現在のログインユーザーを返す。
// Cookie が無い / 期限切れ / 不正な場合は ok=false を返す。
func userFromRequest(ctx context.Context, pool *pgxpool.Pool, r *http.Request) (sessionUser, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return sessionUser{}, false
	}

	// セッションIDからユーザーを引く。期限切れ（expires_at <= now）は弾く。
	const q = `
		SELECT u.id, u.email, u.name
		FROM user_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > now()`

	var u sessionUser
	if err := pool.QueryRow(ctx, q, cookie.Value).Scan(&u.ID, &u.Email, &u.Name); err != nil {
		return sessionUser{}, false
	}
	return u, true
}

// deleteUserSession は user_sessions から該当セッションを削除する（ログアウト）。
func deleteUserSession(ctx context.Context, pool *pgxpool.Pool, sessionID string) error {
	_, err := pool.Exec(ctx, `DELETE FROM user_sessions WHERE id = $1`, sessionID)
	return err
}

// clearSessionCookie はブラウザのセッションCookieを即時失効させる。
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// frontendURL はログイン完了後に戻すフロントエンドのURL。
// 環境変数 FRONTEND_URL があればそれを、無ければローカル開発用の既定値を使う。
func frontendURL() string {
	if v := os.Getenv("FRONTEND_URL"); v != "" {
		return v
	}
	return "http://localhost:3000"
}

// randomToken は URL セーフな 32 バイトのランダム文字列を生成する（セッションID用）。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
