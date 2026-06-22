package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

// state は login → callback の往復で「同じ人」を保証する一時トークン（CSRF対策）。
// login 時に Cookie に保存し、callback で URL クエリの state と一致するか比較する。
const oauthStateCookieName = "oauth_state"

// AuthGoogleLogin は Google の同意画面にリダイレクトするハンドラを返す。
// state をランダム生成して Cookie に保存してから Google に飛ばす。
func AuthGoogleLogin(oauthCfg *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := randomToken()
		if err != nil {
			http.Error(w, "failed to generate state", http.StatusInternalServerError)
			return
		}

		// state は短命でいいので 10 分。HttpOnly でJSから読めないように。
		http.SetCookie(w, &http.Cookie{
			Name:     oauthStateCookieName,
			Value:    state,
			Path:     "/",
			MaxAge:   600,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		url := oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
		http.Redirect(w, r, url, http.StatusFound)
	}
}

// googleUserInfo は Google の userinfo エンドポイントが返す JSON の必要部分。
type googleUserInfo struct {
	Sub           string `json:"sub"`     // Google 内部の不変ユーザーID
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// AuthGoogleCallback は Google から戻ってきた code をトークンに交換し、
// ユーザー情報を取得 → users へ upsert → ログインセッションを発行して Cookie に保存する。
func AuthGoogleCallback(oauthCfg *oauth2.Config, pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) state 検証：Cookie の値と URL クエリの値が一致するか
		cookie, err := r.Cookie(oauthStateCookieName)
		if err != nil {
			http.Error(w, "state cookie missing", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("state") != cookie.Value {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}

		// 使い終わった state Cookie はすぐ削除（MaxAge=-1）
		http.SetCookie(w, &http.Cookie{
			Name:   oauthStateCookieName,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})

		// 2) code をトークンに交換
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "code missing", http.StatusBadRequest)
			return
		}
		token, err := oauthCfg.Exchange(r.Context(), code)
		if err != nil {
			http.Error(w, fmt.Sprintf("token exchange failed: %v", err), http.StatusInternalServerError)
			return
		}

		// 3) アクセストークンで userinfo を取得
		client := oauthCfg.Client(r.Context(), token)
		resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
		if err != nil {
			http.Error(w, fmt.Sprintf("userinfo request failed: %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			http.Error(w, fmt.Sprintf("userinfo returned %d: %s", resp.StatusCode, body), http.StatusBadGateway)
			return
		}

		var info googleUserInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			http.Error(w, "failed to parse userinfo", http.StatusInternalServerError)
			return
		}

		// 4) users テーブルへ upsert（初回ログインなら作成、2回目以降は email/name 更新）
		userID, err := upsertUser(r.Context(), pool, info)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to save user: %v", err), http.StatusInternalServerError)
			return
		}

		// 5) ログインセッションを発行し、セッションIDを Cookie に保存
		sessionID, expiresAt, err := createUserSession(r.Context(), pool, userID)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to create session: %v", err), http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, sessionID, expiresAt)

		// 6) ログイン完了。フロントエンドのトップへ戻す。
		http.Redirect(w, r, frontendURL(), http.StatusFound)
	}
}

// AuthMe は現在ログイン中のユーザー情報を返す。
// ログイン必須チェックは RequireAuth ミドルウェアが行うので、ここは context から取り出すだけ。
func AuthMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": user})
	}
}

// AuthLogout はセッションをサーバー・ブラウザ両方から消してログアウトする。
// Cookie が無くても成功扱い（冪等）。
func AuthLogout(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			if err := deleteUserSession(r.Context(), pool, cookie.Value); err != nil {
				http.Error(w, fmt.Sprintf("failed to delete session: %v", err), http.StatusInternalServerError)
				return
			}
		}
		clearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeError は API設計の `{ "error": { "code", "message" } }` 形式でエラーを返す。
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
