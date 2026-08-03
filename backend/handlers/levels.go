package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// levelSuggestion はプレイヤー1人分のレベル自動調整提案。
type levelSuggestion struct {
	PlayerID              string  `json:"player_id"`
	Name                  string  `json:"name"`
	CurrentLevel          int     `json:"current_level"`
	SessionMatchCount     int     `json:"session_match_count"`
	SessionWinCount       int     `json:"session_win_count"`
	SessionWinRate        float64 `json:"session_win_rate"`
	SessionSpectatorCount int     `json:"session_spectator_count"`
	SuggestedLevel        int     `json:"suggested_level"`
}

// suggestLevel はそのセッションの勝率から提案レベルを計算する（DB_DESIGN.md の表）。
// プレイ試合数が0なら変化なし。結果は必ず 1〜10 にクランプ。
func suggestLevel(current, matchCount, winCount int) (suggested int, winRate float64) {
	if matchCount == 0 {
		return current, 0
	}
	winRate = float64(winCount) / float64(matchCount)

	delta := 0
	switch {
	case winRate >= 0.70:
		delta = 2
	case winRate >= 0.60:
		delta = 1
	case winRate > 0.40:
		delta = 0
	case winRate > 0.30:
		delta = -1
	default:
		delta = -2
	}

	suggested = current + delta
	if suggested > 10 {
		suggested = 10
	}
	if suggested < 1 {
		suggested = 1
	}
	return suggested, winRate
}

// computeLevelSuggestions はそのセッションに参加した全プレイヤー（プレイ or 観戦）の提案を計算する。
// 集計はこのセッションの completed 試合に限定する（組織全体の player_stats とは別物）。
func computeLevelSuggestions(r *http.Request, pool *pgxpool.Pool, sessionID string) ([]levelSuggestion, error) {
	const q = `
		SELECT p.id, p.name, p.level,
		  COUNT(*) FILTER (WHERE mp.team IN ('blue','red') AND m.status = 'completed') AS match_count,
		  COUNT(*) FILTER (WHERE mp.team IN ('blue','red') AND m.status = 'completed'
		                     AND mp.team = m.winner_team) AS win_count,
		  COUNT(*) FILTER (WHERE mp.team = 'spectator' AND m.status = 'completed') AS spectator_count
		FROM players p
		JOIN match_players mp ON mp.player_id = p.id
		JOIN matches m ON m.id = mp.match_id
		WHERE m.session_id = $1
		GROUP BY p.id, p.name, p.level
		ORDER BY p.name`
	rows, err := pool.Query(r.Context(), q, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	suggestions := []levelSuggestion{}
	for rows.Next() {
		var s levelSuggestion
		if err := rows.Scan(&s.PlayerID, &s.Name, &s.CurrentLevel,
			&s.SessionMatchCount, &s.SessionWinCount, &s.SessionSpectatorCount); err != nil {
			return nil, err
		}
		s.SuggestedLevel, s.SessionWinRate = suggestLevel(s.CurrentLevel, s.SessionMatchCount, s.SessionWinCount)
		suggestions = append(suggestions, s)
	}
	return suggestions, rows.Err()
}

// GetLevelSuggestion はセッションのレベル自動調整提案を返す（計算のみ・DB変更なし）。admin 必須。
func GetLevelSuggestion(pool *pgxpool.Pool) http.HandlerFunc {
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
		role, isMember, err := orgRole(r, pool, orgID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "権限の確認に失敗しました")
			return
		}
		if !isMember || role != "admin" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "管理者権限が必要です")
			return
		}

		suggestions, err := computeLevelSuggestions(r, pool, sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "提案の計算に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": suggestions})
	}
}

// appliedChange は適用したレベル変更1件（レスポンス用）。
type appliedChange struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
	OldLevel int    `json:"old_level"`
	NewLevel int    `json:"new_level"`
}

// ApplyLevelChanges は提案を実際に適用する。admin 必須。
// サーバー側で提案を再計算し、変化があるプレイヤーだけ players.level を更新し level_changes に auto 履歴を残す。
// 同一セッションで適用済み（未アンドゥ）の場合は二重適用を防ぐため 409。
func ApplyLevelChanges(pool *pgxpool.Pool) http.HandlerFunc {
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
		role, isMember, err := orgRole(r, pool, orgID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "権限の確認に失敗しました")
			return
		}
		if !isMember || role != "admin" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "管理者権限が必要です")
			return
		}

		// 二重適用チェック：このセッションで未アンドゥの auto 履歴があれば適用済み
		var alreadyApplied bool
		if err := pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM level_changes
			  WHERE session_id = $1 AND change_type = 'auto' AND reverted_at IS NULL)`, sessionID).
			Scan(&alreadyApplied); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "適用状態の確認に失敗しました")
			return
		}
		if alreadyApplied {
			writeError(w, http.StatusConflict, "CONFLICT", "このセッションのレベル調整は既に適用済みです")
			return
		}

		suggestions, err := computeLevelSuggestions(r, pool, sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "提案の計算に失敗しました")
			return
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理を開始できませんでした")
			return
		}
		defer tx.Rollback(r.Context())

		applied := []appliedChange{}
		for _, s := range suggestions {
			if s.SuggestedLevel == s.CurrentLevel {
				continue // 変化なしは記録しない
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE players SET level = $1, updated_at = now() WHERE id = $2`,
				s.SuggestedLevel, s.PlayerID); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "レベルの更新に失敗しました")
				return
			}
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO level_changes (player_id, old_level, new_level, change_type, changed_by_user_id, session_id)
				 VALUES ($1, $2, $3, 'auto', $4, $5)`,
				s.PlayerID, s.CurrentLevel, s.SuggestedLevel, user.ID, sessionID); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "レベル履歴の記録に失敗しました")
				return
			}
			applied = append(applied, appliedChange{
				PlayerID: s.PlayerID, Name: s.Name, OldLevel: s.CurrentLevel, NewLevel: s.SuggestedLevel,
			})
		}

		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理の確定に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": applied})
	}
}
