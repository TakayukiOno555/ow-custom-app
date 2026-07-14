package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// match は試合のレスポンス表現。
type match struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	MapID      *string   `json:"map_id"` // マップ未指定もありうるので nullable
	StartedAt  time.Time `json:"started_at"`
	WinnerTeam *string   `json:"winner_team"` // 未決着は null
	Status     string    `json:"status"`      // in_progress / completed / cancelled
}

// playerBrief は試合参加者の簡易表現（id と名前だけ）。
type playerBrief struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// matchInfo は試合IDから、所属組織ID・その試合の状態・存在有無を引く。
// /matches/:id 系は URL に組織IDが無いので、ここで組織を特定してから権限判定する。
func matchInfo(r *http.Request, pool *pgxpool.Pool, matchID string) (orgID, status string, ok bool, err error) {
	const q = `
		SELECT s.organization_id, m.status
		FROM matches m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.id = $1`
	err = pool.QueryRow(r.Context(), q, matchID).Scan(&orgID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return orgID, status, true, nil
}

// CreateMatch は試合を開始する。admin 必須。
// 青チーム・赤チームは各1人以上、同じプレイヤーが複数の区分に重複してはいけない。
// 参加者・マップはすべてそのセッションの組織のものであること。終了済みセッションには追加不可。
func CreateMatch(pool *pgxpool.Pool) http.HandlerFunc {
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
			writeError(w, http.StatusConflict, "CONFLICT", "終了したセッションには試合を追加できません")
			return
		}

		var body struct {
			MapID              *string  `json:"map_id"`
			BluePlayerIDs      []string `json:"blue_player_ids"`
			RedPlayerIDs       []string `json:"red_player_ids"`
			SpectatorPlayerIDs []string `json:"spectator_player_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		blue := dedupStrings(body.BluePlayerIDs)
		red := dedupStrings(body.RedPlayerIDs)
		spec := dedupStrings(body.SpectatorPlayerIDs)
		if len(blue) == 0 || len(red) == 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "青チーム・赤チームには1人以上必要です")
			return
		}

		// 同じプレイヤーが複数区分に入っていないか（青∩赤、青∩観戦、赤∩観戦）
		all := map[string]struct{}{}
		for _, id := range append(append(append([]string{}, blue...), red...), spec...) {
			if _, dup := all[id]; dup {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "同じプレイヤーが複数の区分に指定されています")
				return
			}
			all[id] = struct{}{}
		}

		// 参加者が全員この組織のプレイヤーか（件数一致でチェック）
		allIDs := make([]string, 0, len(all))
		for id := range all {
			allIDs = append(allIDs, id)
		}
		var validCount int
		if err := pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM players WHERE organization_id = $1 AND id = ANY($2)`, orgID, allIDs).
			Scan(&validCount); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの確認に失敗しました")
			return
		}
		if validCount != len(allIDs) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "この組織に存在しないプレイヤーが含まれています")
			return
		}

		// マップ指定があれば、この組織のマップか確認
		if body.MapID != nil {
			if !uuidPattern.MatchString(*body.MapID) {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "map_id の形式が不正です")
				return
			}
			var mapOK bool
			if err := pool.QueryRow(r.Context(),
				`SELECT EXISTS(SELECT 1 FROM maps WHERE id = $1 AND organization_id = $2)`, *body.MapID, orgID).
				Scan(&mapOK); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの確認に失敗しました")
				return
			}
			if !mapOK {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "この組織に存在しないマップです")
				return
			}
		}

		// match と match_players をまとめて作る
		tx, err := pool.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理を開始できませんでした")
			return
		}
		defer tx.Rollback(r.Context())

		var m match
		const insertMatch = `
			INSERT INTO matches (session_id, map_id)
			VALUES ($1, $2)
			RETURNING id, session_id, map_id, started_at, winner_team, status`
		if err := tx.QueryRow(r.Context(), insertMatch, sessionID, body.MapID).
			Scan(&m.ID, &m.SessionID, &m.MapID, &m.StartedAt, &m.WinnerTeam, &m.Status); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "試合の作成に失敗しました")
			return
		}

		insertTeam := func(ids []string, team string) error {
			for _, pid := range ids {
				if _, err := tx.Exec(r.Context(),
					`INSERT INTO match_players (match_id, player_id, team) VALUES ($1, $2, $3)`, m.ID, pid, team); err != nil {
					return err
				}
			}
			return nil
		}
		if err := insertTeam(blue, "blue"); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "参加者の登録に失敗しました")
			return
		}
		if err := insertTeam(red, "red"); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "参加者の登録に失敗しました")
			return
		}
		if err := insertTeam(spec, "spectator"); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "参加者の登録に失敗しました")
			return
		}

		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理の確定に失敗しました")
			return
		}

		teams, err := matchTeams(r, pool, m.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "参加者の取得に失敗しました")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"match": m, "teams": teams}})
	}
}

// SetMatchResult は試合の勝敗を記録する。admin 必須。in_progress の試合のみ。
func SetMatchResult(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		matchID := r.PathValue("id")
		if !uuidPattern.MatchString(matchID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "試合が見つかりません")
			return
		}

		orgID, status, ok, err := matchInfo(r, pool, matchID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "試合の確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "試合が見つかりません")
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
		if status != "in_progress" {
			writeError(w, http.StatusConflict, "CONFLICT", "進行中の試合ではありません")
			return
		}

		var body struct {
			WinnerTeam string `json:"winner_team"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		if body.WinnerTeam != "blue" && body.WinnerTeam != "red" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "winner_team は blue か red を指定してください")
			return
		}

		var m match
		const q = `
			UPDATE matches SET winner_team = $1, status = 'completed'
			WHERE id = $2
			RETURNING id, session_id, map_id, started_at, winner_team, status`
		if err := pool.QueryRow(r.Context(), q, body.WinnerTeam, matchID).
			Scan(&m.ID, &m.SessionID, &m.MapID, &m.StartedAt, &m.WinnerTeam, &m.Status); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "勝敗の記録に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": m})
	}
}

// CancelMatch は試合をキャンセルする（status→cancelled）。admin 必須。
// 集計は status='completed' のみ対象なので、キャンセルすると勝率などから除外される。
func CancelMatch(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		matchID := r.PathValue("id")
		if !uuidPattern.MatchString(matchID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "試合が見つかりません")
			return
		}

		orgID, status, ok, err := matchInfo(r, pool, matchID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "試合の確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "試合が見つかりません")
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
		if status == "cancelled" {
			writeError(w, http.StatusConflict, "CONFLICT", "この試合は既にキャンセル済みです")
			return
		}

		if _, err := pool.Exec(r.Context(),
			`UPDATE matches SET status = 'cancelled', winner_team = NULL WHERE id = $1`, matchID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "試合のキャンセルに失敗しました")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// matchTeams はその試合の参加者を blue/red/spectators に分けて返す。
func matchTeams(r *http.Request, pool *pgxpool.Pool, matchID string) (map[string][]playerBrief, error) {
	const q = `
		SELECT p.id, p.name, mp.team
		FROM match_players mp
		JOIN players p ON p.id = mp.player_id
		WHERE mp.match_id = $1
		ORDER BY p.name`
	rows, err := pool.Query(r.Context(), q, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := map[string][]playerBrief{"blue": {}, "red": {}, "spectators": {}}
	for rows.Next() {
		var pb playerBrief
		var team string
		if err := rows.Scan(&pb.ID, &pb.Name, &team); err != nil {
			return nil, err
		}
		key := team
		if team == "spectator" {
			key = "spectators"
		}
		teams[key] = append(teams[key], pb)
	}
	return teams, rows.Err()
}
