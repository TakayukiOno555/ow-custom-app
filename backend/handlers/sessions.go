package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// session は1日のカスタム会（開始〜終了）のレスポンス表現。
type session struct {
	ID               string     `json:"id"`
	OrganizationID   string     `json:"organization_id"`
	StartedAt        time.Time  `json:"started_at"`
	EndedAt          *time.Time `json:"ended_at"` // NULL のとき進行中なのでポインタ
	TeamSize         int        `json:"team_size"`
	MapSelectionMode string     `json:"map_selection_mode"`
}

// sessionOrgID はセッションIDから所属組織IDと終了済みかを引く。存在しなければ ok=false。
// /sessions/:id 系は URL に組織IDが無いので、ここで組織を特定してから権限判定する。
func sessionOrgID(r *http.Request, pool *pgxpool.Pool, sessionID string) (orgID string, ended bool, ok bool, err error) {
	var endedAt *time.Time
	err = pool.QueryRow(r.Context(),
		`SELECT organization_id, ended_at FROM sessions WHERE id = $1`, sessionID).Scan(&orgID, &endedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return orgID, endedAt != nil, true, nil
}

// CreateSession は新しいセッションを開始する。admin 必須。
// 同時に進行中（ended_at IS NULL）のセッションは1組織につき1つまで。
func CreateSession(pool *pgxpool.Pool) http.HandlerFunc {
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

		// team_size / map_selection_mode は任意。省略時は DB のデフォルト（5 / rotation）に任せる。
		var body struct {
			TeamSize         *int    `json:"team_size"`
			MapSelectionMode *string `json:"map_selection_mode"`
		}
		// 空ボディ（全部デフォルトで開始）は許容する。その場合 Decode は io.EOF を返すので無視する。
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		teamSize := 5
		if body.TeamSize != nil {
			teamSize = *body.TeamSize
		}
		if teamSize != 5 && teamSize != 6 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "team_size は 5 か 6 を指定してください")
			return
		}
		mode := "rotation"
		if body.MapSelectionMode != nil {
			mode = *body.MapSelectionMode
		}
		if mode != "rotation" && mode != "random" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "map_selection_mode は rotation か random を指定してください")
			return
		}

		// 進行中セッションが既にあるか
		var active bool
		if err := pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM sessions WHERE organization_id = $1 AND ended_at IS NULL)`, orgID).
			Scan(&active); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッション状態の確認に失敗しました")
			return
		}
		if active {
			writeError(w, http.StatusConflict, "CONFLICT", "進行中のセッションが既にあります")
			return
		}

		var s session
		const q = `
			INSERT INTO sessions (organization_id, team_size, map_selection_mode)
			VALUES ($1, $2, $3)
			RETURNING id, organization_id, started_at, ended_at, team_size, map_selection_mode`
		if err := pool.QueryRow(r.Context(), q, orgID, teamSize, mode).
			Scan(&s.ID, &s.OrganizationID, &s.StartedAt, &s.EndedAt, &s.TeamSize, &s.MapSelectionMode); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッションの開始に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": s})
	}
}

// GetSession はセッション情報を返す。member 必須。
func GetSession(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		sessionID := r.PathValue("id")
		if !uuidPattern.MatchString(sessionID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "セッションが見つかりません")
			return
		}

		orgID, _, ok, err := sessionOrgID(r, pool, sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッションの確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "セッションが見つかりません")
			return
		}
		_, isMember, err := orgRole(r, pool, orgID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "権限の確認に失敗しました")
			return
		}
		if !isMember {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "このセッションにアクセスする権限がありません")
			return
		}

		var s session
		const q = `
			SELECT id, organization_id, started_at, ended_at, team_size, map_selection_mode
			FROM sessions WHERE id = $1`
		if err := pool.QueryRow(r.Context(), q, sessionID).
			Scan(&s.ID, &s.OrganizationID, &s.StartedAt, &s.EndedAt, &s.TeamSize, &s.MapSelectionMode); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッションの取得に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": s})
	}
}

// EndSession はセッションを終了する（ended_at に現在時刻をセット）。admin 必須。
// 既に終了済みなら 409。
func EndSession(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		sessionID := r.PathValue("id")
		if !uuidPattern.MatchString(sessionID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "セッションが見つかりません")
			return
		}

		orgID, ended, ok, err := sessionOrgID(r, pool, sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッションの確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "セッションが見つかりません")
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
		if ended {
			writeError(w, http.StatusConflict, "CONFLICT", "このセッションは既に終了しています")
			return
		}

		var s session
		const q = `
			UPDATE sessions SET ended_at = now()
			WHERE id = $1
			RETURNING id, organization_id, started_at, ended_at, team_size, map_selection_mode`
		if err := pool.QueryRow(r.Context(), q, sessionID).
			Scan(&s.ID, &s.OrganizationID, &s.StartedAt, &s.EndedAt, &s.TeamSize, &s.MapSelectionMode); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッションの終了に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": s})
	}
}

// dedupStrings はスライスから重複を除いた新しいスライスを返す（順序は保つ）。
func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
