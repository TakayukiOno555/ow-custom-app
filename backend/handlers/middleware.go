package handlers

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ctxKey は context に値を入れる時のキー専用の型。
// string をそのままキーにすると他パッケージと衝突する恐れがあるので、専用型を定義する（Go の定石）。
type ctxKey int

const userCtxKey ctxKey = 0

// RequireAuth はログイン必須のハンドラを包む「関所」。
// セッションCookieを検証し、OKなら request の context にユーザーを載せて次へ通す。
// NGなら 401 を返してここで止める（next は呼ばれない）。
func RequireAuth(pool *pgxpool.Pool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromRequest(r.Context(), pool, r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "ログインが必要です")
			return
		}
		// 通行証（ユーザー情報）を context に載せて、後続ハンドラから取り出せるようにする。
		ctx := context.WithValue(r.Context(), userCtxKey, user)
		next(w, r.WithContext(ctx))
	}
}

// UserFromContext は RequireAuth が載せたログインユーザーを取り出す。
// RequireAuth で包まれたハンドラ内では必ず ok=true になる。
func UserFromContext(ctx context.Context) (sessionUser, bool) {
	user, ok := ctx.Value(userCtxKey).(sessionUser)
	return user, ok
}
