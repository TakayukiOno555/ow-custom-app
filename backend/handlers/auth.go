package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
)

// state は login → callback の往復で「同じ人」を保証する一時トークン（CSRF対策）。
// login 時に Cookie に保存し、callback で URL クエリの state と一致するか比較する。
const oauthStateCookieName = "oauth_state"

// AuthGoogleLogin は Google の同意画面にリダイレクトするハンドラを返す。
// state をランダム生成して Cookie に保存してから Google に飛ばす。
func AuthGoogleLogin(oauthCfg *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := randomState()
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
// ユーザー情報を取得して JSON で返す（最小版。DB保存・セッション発行は次ステップ）。
func AuthGoogleCallback(oauthCfg *oauth2.Config) http.HandlerFunc {
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

		// 4) 最小版：取れた情報をそのまま JSON で返す（次ステップで users テーブルへ upsert）
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(info)
	}
}

// randomState は URL セーフな 32 バイトのランダム文字列を生成する。
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
