package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// organization は組織（カスタム会グループ）のレスポンス表現。
type organization struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	LevelDisplayMode string    `json:"level_display_mode"`
	CreatedAt        time.Time `json:"created_at"`
}

// uuidPattern は path から来た id がUUID形式かをざっくり確認するための正規表現。
// 不正な文字列をそのままDBに投げると 500 になるので、先に弾いて 404 にするために使う。
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// orgRole は user が org に所属していれば、その役割（admin/member）を返す。
// 未所属（または組織が存在しない）なら ok=false。DBエラーは err で返す。
// 「この組織で何ができる人か」を判定する共通部品。
func orgRole(r *http.Request, pool *pgxpool.Pool, orgID, userID string) (role string, ok bool, err error) {
	const q = `SELECT role FROM organization_members WHERE organization_id = $1 AND user_id = $2`
	err = pool.QueryRow(r.Context(), q, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
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

// organizationListItem は一覧用。組織情報＋ログインユーザーのその組織での役割。
// organization を埋め込むと JSON では id/name/... が role と同じ階層に並ぶ。
type organizationListItem struct {
	organization
	Role string `json:"role"`
}

// ListOrganizations は自分が所属している組織の一覧を返す。
func ListOrganizations(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())

		const q = `
			SELECT o.id, o.name, o.level_display_mode, o.created_at, m.role
			FROM organizations o
			JOIN organization_members m ON m.organization_id = o.id
			WHERE m.user_id = $1
			ORDER BY o.created_at`
		rows, err := pool.Query(r.Context(), q, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "組織一覧の取得に失敗しました")
			return
		}
		defer rows.Close()

		// 0件でも null ではなく [] を返したいので、空スライスで初期化しておく。
		items := []organizationListItem{}
		for rows.Next() {
			var it organizationListItem
			if err := rows.Scan(&it.ID, &it.Name, &it.LevelDisplayMode, &it.CreatedAt, &it.Role); err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "組織一覧の読み取りに失敗しました")
				return
			}
			items = append(items, it)
		}
		if rows.Err() != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "組織一覧の取得に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}
}

// GetOrganization は組織情報を返す。その組織のメンバーであることが必要。
func GetOrganization(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		orgID := r.PathValue("id")
		if !uuidPattern.MatchString(orgID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "組織が見つかりません")
			return
		}

		// メンバーかどうか（＝アクセス権）を先に確認する。
		_, isMember, err := orgRole(r, pool, orgID, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "権限の確認に失敗しました")
			return
		}
		if !isMember {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "この組織にアクセスする権限がありません")
			return
		}

		var org organization
		const q = `SELECT id, name, level_display_mode, created_at FROM organizations WHERE id = $1`
		if err := pool.QueryRow(r.Context(), q, orgID).
			Scan(&org.ID, &org.Name, &org.LevelDisplayMode, &org.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "組織の取得に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": org})
	}
}

// UpdateLevelDisplay は組織のレベル表示モード（visible/hidden）を切り替える。admin のみ。
func UpdateLevelDisplay(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())
		orgID := r.PathValue("id")
		if !uuidPattern.MatchString(orgID) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "組織が見つかりません")
			return
		}

		// admin だけが変更できる。
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

		var body struct {
			LevelDisplayMode string `json:"level_display_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		if body.LevelDisplayMode != "visible" && body.LevelDisplayMode != "hidden" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "level_display_mode は visible か hidden を指定してください")
			return
		}

		var org organization
		const q = `
			UPDATE organizations SET level_display_mode = $1
			WHERE id = $2
			RETURNING id, name, level_display_mode, created_at`
		if err := pool.QueryRow(r.Context(), q, body.LevelDisplayMode, orgID).
			Scan(&org.ID, &org.Name, &org.LevelDisplayMode, &org.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "表示モードの更新に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": org})
	}
}
