package handlers

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// teamPlayer は青/赤チームに振り分けられたプレイヤー。
type teamPlayer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Level int    `json:"level"`
}

// spectatorPlayer は観戦者。選定理由が分かるよう観戦数も返す。
type spectatorPlayer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Level          int    `json:"level"`
	SpectatorCount int    `json:"spectator_count"`
}

// candidate は振り分け計算に使う内部表現。
type candidate struct {
	id             string
	name           string
	level          int
	spectatorCount int
}

// AutoAssignTeams は参加プレイヤーを青/赤チームと観戦者に自動で振り分けて提案を返す。admin 必須。
// - プレイ枠は team_size×2。あふれた分は観戦へ（過去の観戦数が少ない＝これまで多くプレイした人から観戦に回す）
// - 青/赤はレベル合計が釣り合うよう、レベル降順で合計の低いチームへ貪欲に割り当てる
// - この API は提案を返すだけで DB には保存しない（実際の試合開始は CreateMatch）
func AutoAssignTeams(pool *pgxpool.Pool) http.HandlerFunc {
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
			writeError(w, http.StatusConflict, "CONFLICT", "終了したセッションではチーム分けできません")
			return
		}

		// セッションの team_size を取得（プレイ枠 = team_size×2）
		var teamSize int
		if err := pool.QueryRow(r.Context(),
			`SELECT team_size FROM sessions WHERE id = $1`, sessionID).Scan(&teamSize); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "セッションの取得に失敗しました")
			return
		}

		var body struct {
			PlayerIDs []string `json:"player_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		ids := dedupStrings(body.PlayerIDs)
		if len(ids) < 2 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "プレイヤーを2人以上指定してください")
			return
		}

		// プレイヤーのレベルと観戦数をまとめて取得（この組織のプレイヤーか検証も兼ねる）
		const q = `
			SELECT p.id, p.name, p.level, s.spectator_count
			FROM players p
			JOIN player_stats s ON s.player_id = p.id
			WHERE p.organization_id = $1 AND p.id = ANY($2)`
		rows, err := pool.Query(r.Context(), q, orgID, ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの取得に失敗しました")
			return
		}
		defer rows.Close()

		cands := []candidate{}
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.name, &c.level, &c.spectatorCount); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの読み取りに失敗しました")
				return
			}
			cands = append(cands, c)
		}
		if rows.Err() != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの取得に失敗しました")
			return
		}
		if len(cands) != len(ids) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "この組織に存在しないプレイヤーが含まれています")
			return
		}

		playing, spectators := splitSpectators(cands, teamSize*2)
		blue, red := balanceTeams(playing, teamSize)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"blue":       blue,
			"red":        red,
			"spectators": spectators,
		}})
	}
}

// splitSpectators は capacity 人をプレイ枠に残し、あふれた分を観戦者に回す。
// 観戦者は「過去の観戦数が少ない順」に選ぶ（これまで多くプレイした人を優先的に休ませ、観戦が偏らないようにする）。
func splitSpectators(cands []candidate, capacity int) (playing []candidate, spectators []spectatorPlayer) {
	if len(cands) <= capacity {
		return cands, []spectatorPlayer{} // 全員プレイ
	}

	// 観戦数の少ない順に並べ、先頭 (len-capacity) 人を観戦者にする。
	// 同数のときは id 順で安定させる。
	sorted := make([]candidate, len(cands))
	copy(sorted, cands)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].spectatorCount != sorted[j].spectatorCount {
			return sorted[i].spectatorCount < sorted[j].spectatorCount
		}
		return sorted[i].id < sorted[j].id
	})

	numSpectators := len(cands) - capacity
	spectators = []spectatorPlayer{}
	for _, c := range sorted[:numSpectators] {
		spectators = append(spectators, spectatorPlayer{
			ID: c.id, Name: c.name, Level: c.level, SpectatorCount: c.spectatorCount,
		})
	}
	playing = sorted[numSpectators:]
	return playing, spectators
}

// balanceTeams はプレイヤーをレベル合計が釣り合うよう青/赤に振り分ける。
// レベル降順に見て、そのつど合計の小さいチームへ入れる貪欲法。各チームは最大 teamSize 人。
func balanceTeams(playing []candidate, teamSize int) (blue, red []teamPlayer) {
	sorted := make([]candidate, len(playing))
	copy(sorted, playing)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].level != sorted[j].level {
			return sorted[i].level > sorted[j].level // レベル降順
		}
		return sorted[i].id < sorted[j].id
	})

	blue, red = []teamPlayer{}, []teamPlayer{}
	blueSum, redSum := 0, 0
	for _, c := range sorted {
		tp := teamPlayer{ID: c.id, Name: c.name, Level: c.level}
		// 満員のチームには入れない。両方空きがあれば合計の小さい方へ（同点なら青へ）。
		switch {
		case len(blue) >= teamSize:
			red = append(red, tp)
			redSum += c.level
		case len(red) >= teamSize:
			blue = append(blue, tp)
			blueSum += c.level
		case blueSum <= redSum:
			blue = append(blue, tp)
			blueSum += c.level
		default:
			red = append(red, tp)
			redSum += c.level
		}
	}
	return blue, red
}
