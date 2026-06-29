package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// playerWithStats はプレイヤー情報＋集計（勝率・試合数・観戦数）。一覧レスポンス用。
type playerWithStats struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Level          int     `json:"level"`
	MatchCount     int     `json:"match_count"`
	WinCount       int     `json:"win_count"`
	WinRate        float64 `json:"win_rate"`
	SpectatorCount int     `json:"spectator_count"`
}

// player は単体のプレイヤー（集計なし）。追加・変更の返却用。
type player struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Level     int       `json:"level"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// maxPlayersPerOrg は1組織あたりのプレイヤー上限（仕様: 18人）。
const maxPlayersPerOrg = 18

// playerOrgID はプレイヤーIDから所属組織IDを引く。存在しなければ ok=false。
// PUT/DELETE /players/:id は URL に組織IDが無いので、ここで組織を特定してから権限判定する。
func playerOrgID(r *http.Request, pool *pgxpool.Pool, playerID string) (orgID string, ok bool, err error) {
	err = pool.QueryRow(r.Context(), `SELECT organization_id FROM players WHERE id = $1`, playerID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return orgID, true, nil
}

// ListPlayers は組織のプレイヤー一覧を勝率・試合数・観戦数付きで返す。member 必須。
func ListPlayers(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		orgID := r.PathValue("orgId")
		if !uuidPattern.MatchString(orgID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "組織が見つかりません")
			return
		}

		_, isMember, err := orgRole(r, pool, orgID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "権限の確認に失敗しました")
			return
		}
		if !isMember {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "この組織にアクセスする権限がありません")
			return
		}

		const q = `
			SELECT p.id, p.name, p.level,
			       s.match_count, s.win_count, s.win_rate, s.spectator_count
			FROM players p
			JOIN player_stats s ON s.player_id = p.id
			WHERE p.organization_id = $1
			ORDER BY p.created_at`
		rows, err := pool.Query(r.Context(), q, orgID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤー一覧の取得に失敗しました")
			return
		}
		defer rows.Close()

		items := []playerWithStats{}
		for rows.Next() {
			var p playerWithStats
			if err := rows.Scan(&p.ID, &p.Name, &p.Level,
				&p.MatchCount, &p.WinCount, &p.WinRate, &p.SpectatorCount); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤー一覧の読み取りに失敗しました")
				return
			}
			items = append(items, p)
		}
		if rows.Err() != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤー一覧の取得に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}
}

// CreatePlayer は組織にプレイヤーを追加する。admin 必須。上限18人を超えると 409。
func CreatePlayer(pool *pgxpool.Pool) http.HandlerFunc {
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
		if !isMember {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "この組織にアクセスする権限がありません")
			return
		}
		if role != "admin" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "管理者権限が必要です")
			return
		}

		// level は任意（デフォルト5）。ポインタで「指定なし」と「0指定」を区別する。
		var body struct {
			Name  string `json:"name"`
			Level *int   `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "プレイヤー名は必須です")
			return
		}
		level := 5
		if body.Level != nil {
			level = *body.Level
		}
		if level < 1 || level > 10 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "レベルは1〜10で指定してください")
			return
		}

		// 上限チェック（18人）
		var count int
		if err := pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM players WHERE organization_id = $1`, orgID).Scan(&count); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤー数の確認に失敗しました")
			return
		}
		if count >= maxPlayersPerOrg {
			writeError(w, http.StatusConflict, "CONFLICT", "プレイヤーは1組織あたり18人までです")
			return
		}

		var p player
		const q = `
			INSERT INTO players (organization_id, name, level)
			VALUES ($1, $2, $3)
			RETURNING id, name, level, created_at, updated_at`
		if err := pool.QueryRow(r.Context(), q, orgID, name, level).
			Scan(&p.ID, &p.Name, &p.Level, &p.CreatedAt, &p.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの追加に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": p})
	}
}

// UpdatePlayer はプレイヤーの名前・レベルを変更する。admin 必須。
// レベルが変わった場合は level_changes に履歴を残す（アンドゥ用）。更新とは1トランザクション。
func UpdatePlayer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		playerID := r.PathValue("id")
		if !uuidPattern.MatchString(playerID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "プレイヤーが見つかりません")
			return
		}

		orgID, ok, err := playerOrgID(r, pool, playerID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "プレイヤーが見つかりません")
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

		var body struct {
			Name  string `json:"name"`
			Level int    `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "プレイヤー名は必須です")
			return
		}
		if body.Level < 1 || body.Level > 10 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "レベルは1〜10で指定してください")
			return
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理を開始できませんでした")
			return
		}
		defer tx.Rollback(r.Context())

		// 現在のレベルを取得（履歴の old_level に使う）
		var oldLevel int
		if err := tx.QueryRow(r.Context(), `SELECT level FROM players WHERE id = $1`, playerID).Scan(&oldLevel); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの取得に失敗しました")
			return
		}

		// レベルが変わったときだけ履歴を残す（手動変更 = 'manual'）
		if oldLevel != body.Level {
			const insertHist = `
				INSERT INTO level_changes (player_id, old_level, new_level, change_type, changed_by_user_id)
				VALUES ($1, $2, $3, 'manual', $4)`
			if _, err := tx.Exec(r.Context(), insertHist, playerID, oldLevel, body.Level, user.ID); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "レベル履歴の記録に失敗しました")
				return
			}
		}

		var p player
		const updateQ = `
			UPDATE players SET name = $1, level = $2, updated_at = now()
			WHERE id = $3
			RETURNING id, name, level, created_at, updated_at`
		if err := tx.QueryRow(r.Context(), updateQ, name, body.Level, playerID).
			Scan(&p.ID, &p.Name, &p.Level, &p.CreatedAt, &p.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの更新に失敗しました")
			return
		}

		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理の確定に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": p})
	}
}

// DeletePlayer はプレイヤーを削除する。admin 必須。
// match_players / level_changes は FK の ON DELETE CASCADE で一緒に消える。
func DeletePlayer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		playerID := r.PathValue("id")
		if !uuidPattern.MatchString(playerID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "プレイヤーが見つかりません")
			return
		}

		orgID, ok, err := playerOrgID(r, pool, playerID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "プレイヤーが見つかりません")
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

		if _, err := pool.Exec(r.Context(), `DELETE FROM players WHERE id = $1`, playerID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの削除に失敗しました")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
