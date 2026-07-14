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

// gameMap はマップ・モードのレスポンス表現。
// 型名を map にすると Go の組み込み型 map と衝突するので gameMap にしている。
type gameMap struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	IncludeInRandom bool      `json:"include_in_random"` // ランダム抽選の母集団に含めるか
	CreatedAt       time.Time `json:"created_at"`
}

// mapOrgID はマップIDから所属組織IDを引く。存在しなければ ok=false。
// DELETE /maps/:id は URL に組織IDが無いので、ここで組織を特定してから権限判定する。
func mapOrgID(r *http.Request, pool *pgxpool.Pool, mapID string) (orgID string, ok bool, err error) {
	err = pool.QueryRow(r.Context(), `SELECT organization_id FROM maps WHERE id = $1`, mapID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return orgID, true, nil
}

// ListMaps は組織のマップ一覧を返す。member 必須。
func ListMaps(pool *pgxpool.Pool) http.HandlerFunc {
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

		const q = `SELECT id, name, include_in_random, created_at FROM maps WHERE organization_id = $1 ORDER BY created_at`
		rows, err := pool.Query(r.Context(), q, orgID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップ一覧の取得に失敗しました")
			return
		}
		defer rows.Close()

		items := []gameMap{}
		for rows.Next() {
			var m gameMap
			if err := rows.Scan(&m.ID, &m.Name, &m.IncludeInRandom, &m.CreatedAt); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "マップ一覧の読み取りに失敗しました")
				return
			}
			items = append(items, m)
		}
		if rows.Err() != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップ一覧の取得に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}
}

// CreateMap は組織にマップを追加する。admin 必須。
func CreateMap(pool *pgxpool.Pool) http.HandlerFunc {
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

		// include_in_random は任意。省略時は true（ランダム対象）。ポインタで「未指定」と「false指定」を区別。
		var body struct {
			Name            string `json:"name"`
			IncludeInRandom *bool  `json:"include_in_random"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "マップ名は必須です")
			return
		}
		includeInRandom := true
		if body.IncludeInRandom != nil {
			includeInRandom = *body.IncludeInRandom
		}

		var m gameMap
		const q = `
			INSERT INTO maps (organization_id, name, include_in_random)
			VALUES ($1, $2, $3)
			RETURNING id, name, include_in_random, created_at`
		if err := pool.QueryRow(r.Context(), q, orgID, name, includeInRandom).
			Scan(&m.ID, &m.Name, &m.IncludeInRandom, &m.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの追加に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": m})
	}
}

// UpdateMap はマップの名前・ランダム対象フラグを変更する。admin 必須。
// name / include_in_random はどちらも任意で、渡した項目だけ更新する（部分更新）。
// フロントのチェックON/OFFは include_in_random だけ渡してこのAPIを呼ぶ想定。
func UpdateMap(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		mapID := r.PathValue("id")
		if !uuidPattern.MatchString(mapID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "マップが見つかりません")
			return
		}

		orgID, ok, err := mapOrgID(r, pool, mapID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "マップが見つかりません")
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
			Name            *string `json:"name"`
			IncludeInRandom *bool   `json:"include_in_random"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		if body.Name == nil && body.IncludeInRandom == nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "更新する項目がありません")
			return
		}

		// 現在値を取得し、渡された項目だけ上書きしてから更新する（部分更新）。
		var m gameMap
		if err := pool.QueryRow(r.Context(),
			`SELECT id, name, include_in_random, created_at FROM maps WHERE id = $1`, mapID).
			Scan(&m.ID, &m.Name, &m.IncludeInRandom, &m.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの取得に失敗しました")
			return
		}
		if body.Name != nil {
			name := strings.TrimSpace(*body.Name)
			if name == "" {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "マップ名は必須です")
				return
			}
			m.Name = name
		}
		if body.IncludeInRandom != nil {
			m.IncludeInRandom = *body.IncludeInRandom
		}

		const q = `
			UPDATE maps SET name = $1, include_in_random = $2
			WHERE id = $3
			RETURNING id, name, include_in_random, created_at`
		if err := pool.QueryRow(r.Context(), q, m.Name, m.IncludeInRandom, mapID).
			Scan(&m.ID, &m.Name, &m.IncludeInRandom, &m.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの更新に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": m})
	}
}

// DeleteMap はマップを削除する。admin 必須。
func DeleteMap(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		mapID := r.PathValue("id")
		if !uuidPattern.MatchString(mapID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "マップが見つかりません")
			return
		}

		orgID, ok, err := mapOrgID(r, pool, mapID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの確認に失敗しました")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "マップが見つかりません")
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

		if _, err := pool.Exec(r.Context(), `DELETE FROM maps WHERE id = $1`, mapID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの削除に失敗しました")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
