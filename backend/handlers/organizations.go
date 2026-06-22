package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// organization は組織（カスタム会グループ）のレスポンス表現。
type organization struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	LevelDisplayMode string    `json:"level_display_mode"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreateOrganization は新しい組織を作り、作成者を admin として所属させる。
// organizations への挿入と organization_members への挿入を1つのトランザクションで行う
// （片方だけ成功して「オーナー不在の組織」が生まれるのを防ぐ）。
func CreateOrganization(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context()) // RequireAuth 済みなので必ず取れる

		// 1) リクエストボディ（JSON）を読む
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "組織名は必須です")
			return
		}

		// 2) トランザクション開始。途中で return しても Rollback されるよう defer しておく
		//    （Commit 済みなら Rollback は何もしないので二重でも安全）。
		tx, err := pool.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理を開始できませんでした")
			return
		}
		defer tx.Rollback(r.Context())

		// 3) 組織を作成
		var org organization
		const insertOrg = `
			INSERT INTO organizations (name, owner_user_id)
			VALUES ($1, $2)
			RETURNING id, name, level_display_mode, created_at`
		if err := tx.QueryRow(r.Context(), insertOrg, name, user.ID).
			Scan(&org.ID, &org.Name, &org.LevelDisplayMode, &org.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "組織の作成に失敗しました")
			return
		}

		// 4) 作成者を admin として所属させる
		const insertMember = `
			INSERT INTO organization_members (organization_id, user_id, role)
			VALUES ($1, $2, 'admin')`
		if _, err := tx.Exec(r.Context(), insertMember, org.ID, user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "メンバー登録に失敗しました")
			return
		}

		// 5) ここまで全部成功したら確定
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理の確定に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": org})
	}
}
