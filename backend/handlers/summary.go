package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// summaryPlayer はセッション結果サマリーの1プレイヤー分の成績（そのセッション限定）。
type summaryPlayer struct {
	PlayerID       string  `json:"player_id"`
	Name           string  `json:"name"`
	MatchCount     int     `json:"match_count"`
	WinCount       int     `json:"win_count"`
	WinRate        float64 `json:"win_rate"`
	SpectatorCount int     `json:"spectator_count"`
}

// GetSessionSummary はセッションの結果サマリーを返す。member 必須。
// セッション情報＋完了試合数＋各参加者のそのセッション限定の成績（勝率・観戦数）を返す読み取り専用。
func GetSessionSummary(pool *pgxpool.Pool) http.HandlerFunc {
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

		// セッション情報
		var s session
		if err := pool.QueryRow(r.Context(),
			`SELECT id, organization_id, started_at, ended_at, team_size, map_selection_mode
			 FROM sessions WHERE id = $1`, sessionID).
			Scan(&s.ID, &s.OrganizationID, &s.StartedAt, &s.EndedAt, &s.TeamSize, &s.MapSelectionMode); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッションの取得に失敗しました")
			return
		}

		// 完了試合数
		var totalMatches int
		if err := pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM matches WHERE session_id = $1 AND status = 'completed'`, sessionID).
			Scan(&totalMatches); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "試合数の取得に失敗しました")
			return
		}

		// 参加者ごとのそのセッション限定の成績
		const q = `
			SELECT p.id, p.name,
			  COUNT(*) FILTER (WHERE mp.team IN ('blue','red') AND m.status = 'completed') AS match_count,
			  COUNT(*) FILTER (WHERE mp.team IN ('blue','red') AND m.status = 'completed'
			                     AND mp.team = m.winner_team) AS win_count,
			  COUNT(*) FILTER (WHERE mp.team = 'spectator' AND m.status = 'completed') AS spectator_count
			FROM players p
			JOIN match_players mp ON mp.player_id = p.id
			JOIN matches m ON m.id = mp.match_id
			WHERE m.session_id = $1
			GROUP BY p.id, p.name
			ORDER BY p.name`
		rows, err := pool.Query(r.Context(), q, sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "サマリーの集計に失敗しました")
			return
		}
		defer rows.Close()

		players := []summaryPlayer{}
		for rows.Next() {
			var sp summaryPlayer
			if err := rows.Scan(&sp.PlayerID, &sp.Name, &sp.MatchCount, &sp.WinCount, &sp.SpectatorCount); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "サマリーの読み取りに失敗しました")
				return
			}
			if sp.MatchCount > 0 {
				sp.WinRate = float64(sp.WinCount) / float64(sp.MatchCount)
			}
			players = append(players, sp)
		}
		if rows.Err() != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "サマリーの集計に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"session":       s,
			"total_matches": totalMatches,
			"players":       players,
		}})
	}
}
