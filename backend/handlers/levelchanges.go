package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// levelChange はレベル変更履歴1件のレスポンス表現。
type levelChange struct {
	ID            string     `json:"id"`
	PlayerID      string     `json:"player_id"`
	PlayerName    string     `json:"player_name"`
	OldLevel      int        `json:"old_level"`
	NewLevel      int        `json:"new_level"`
	ChangeType    string     `json:"change_type"` // manual / auto
	SessionID     *string    `json:"session_id"`
	RevertedAt    *time.Time `json:"reverted_at"` // 取消済みなら時刻、未取消は null
	CreatedAt     time.Time  `json:"created_at"`
	ChangedByName string     `json:"changed_by_name"`
}

// ListLevelChanges は組織のレベル変更履歴を新しい順に返す。admin 必須。
func ListLevelChanges(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		orgID := r.PathValue("orgId")
		if !uuidPattern.MatchString(orgID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "組織が見つかりません")
			return
		}

		role, isMember, err := orgRole(r, pool, orgID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "権限の確認に失敗しました")
			return
		}
		if !isMember || role != "admin" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "管理者権限が必要です")
			return
		}

		const q = `
			SELECT lc.id, lc.player_id, p.name, lc.old_level, lc.new_level, lc.change_type,
			       lc.session_id, lc.reverted_at, lc.created_at, u.name
			FROM level_changes lc
			JOIN players p ON p.id = lc.player_id
			JOIN users u ON u.id = lc.changed_by_user_id
			WHERE p.organization_id = $1
			ORDER BY lc.created_at DESC`
		rows, err := pool.Query(r.Context(), q, orgID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "履歴の取得に失敗しました")
			return
		}
		defer rows.Close()

		items := []levelChange{}
		for rows.Next() {
			var lc levelChange
			if err := rows.Scan(&lc.ID, &lc.PlayerID, &lc.PlayerName, &lc.OldLevel, &lc.NewLevel,
				&lc.ChangeType, &lc.SessionID, &lc.RevertedAt, &lc.CreatedAt, &lc.ChangedByName); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "履歴の読み取りに失敗しました")
				return
			}
			items = append(items, lc)
		}
		if rows.Err() != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "履歴の取得に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}
}

// UndoLevelChange は指定のレベル変更を1件取り消す。admin 必須。
// プレイヤーのレベルを old_level に戻し、reverted_at を記録する。
// - 既に取消済み → 409
// - 同じプレイヤーに、より新しい未取消の変更がある → 409（新しいものから取り消すこと）
func UndoLevelChange(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		changeID := r.PathValue("id")
		if !uuidPattern.MatchString(changeID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "変更履歴が見つかりません")
			return
		}

		// 履歴の所属組織・対象プレイヤー・old_level・取消済みか・作成時刻を取得
		var orgID, playerID string
		var oldLevel int
		var reverted bool
		var createdAt time.Time
		const infoQ = `
			SELECT p.organization_id, lc.player_id, lc.old_level, lc.reverted_at IS NOT NULL, lc.created_at
			FROM level_changes lc
			JOIN players p ON p.id = lc.player_id
			WHERE lc.id = $1`
		err := pool.QueryRow(r.Context(), infoQ, changeID).Scan(&orgID, &playerID, &oldLevel, &reverted, &createdAt)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "変更履歴が見つかりません")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "履歴の確認に失敗しました")
			return
		}

		role, isMember, err := orgRole(r, pool, orgID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "権限の確認に失敗しました")
			return
		}
		if !isMember || role != "admin" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "管理者権限が必要です")
			return
		}
		if reverted {
			writeError(w, http.StatusConflict, "CONFLICT", "この変更は既に取り消されています")
			return
		}

		// より新しい未取消の変更があるか（あれば先にそれを取り消す必要がある）
		var hasNewer bool
		if err := pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM level_changes
			  WHERE player_id = $1 AND reverted_at IS NULL AND created_at > $2)`,
			playerID, createdAt).Scan(&hasNewer); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "履歴の確認に失敗しました")
			return
		}
		if hasNewer {
			writeError(w, http.StatusConflict, "CONFLICT", "より新しい変更があるため取り消せません（新しいものから取り消してください）")
			return
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理を開始できませんでした")
			return
		}
		defer tx.Rollback(r.Context())

		if _, err := tx.Exec(r.Context(),
			`UPDATE players SET level = $1, updated_at = now() WHERE id = $2`, oldLevel, playerID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "レベルの復元に失敗しました")
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE level_changes SET reverted_at = now() WHERE id = $1`, changeID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "履歴の更新に失敗しました")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理の確定に失敗しました")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
