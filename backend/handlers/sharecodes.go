package handlers

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// shareCodeChars は共有コードに使う文字（A-Z, 0-9 の36種）。紛らわしさよりも仕様どおり4桁36進を優先。
const shareCodeChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// shareCodeTTL は共有コードの有効期間（発行から7日）。
const shareCodeTTL = 7 * 24 * time.Hour

// generateShareCode は4桁の英数字コードをランダム生成する。
func generateShareCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 4)
	for i, v := range b {
		out[i] = shareCodeChars[int(v)%len(shareCodeChars)]
	}
	return string(out), nil
}

// CreateShareCode は組織の共有コードを発行する。admin 必須。
// コードが既存と衝突したら数回まで再生成する。
func CreateShareCode(pool *pgxpool.Pool) http.HandlerFunc {
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

		expiresAt := time.Now().Add(shareCodeTTL)

		// 衝突（UNIQUE制約）したら再生成。最大10回。
		var code string
		var createdAt time.Time
		for attempt := 0; attempt < 10; attempt++ {
			c, gerr := generateShareCode()
			if gerr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "コードの生成に失敗しました")
				return
			}
			const q = `
				INSERT INTO share_codes (organization_id, code, created_by_user_id, expires_at)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (code) DO NOTHING
				RETURNING code, created_at`
			err := pool.QueryRow(r.Context(), q, orgID, c, user.ID, expiresAt).Scan(&code, &createdAt)
			if errors.Is(err, pgx.ErrNoRows) {
				continue // コード衝突 → 別のコードで再試行
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "共有コードの発行に失敗しました")
				return
			}
			break // 成功
		}
		if code == "" {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "コードの発行に失敗しました。もう一度お試しください")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"code":       code,
			"expires_at": expiresAt,
			"created_at": createdAt,
		}})
	}
}

// ImportShareCode はコードを入力して別組織へ取り込む。認証済みなら誰でも可。
// 新しい組織を作り実行者を admin にし、元組織の players / maps だけを複製する
// （試合履歴・レベル変更履歴・勝率は複製しない）。
func ImportShareCode(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := UserFromContext(r.Context())

		var body struct {
			Code string `json:"code"`
			Name string `json:"name"` // 新組織名（任意。省略時は元組織名）
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "リクエストの形式が不正です")
			return
		}
		code := strings.ToUpper(strings.TrimSpace(body.Code))
		if len(code) != 4 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "コードは4桁で入力してください")
			return
		}

		// 有効なコードか（期限切れは無効扱い）。元組織IDと元組織名を取得。
		var srcOrgID, srcName string
		const findQ = `
			SELECT sc.organization_id, o.name
			FROM share_codes sc
			JOIN organizations o ON o.id = sc.organization_id
			WHERE sc.code = $1 AND sc.expires_at > now()`
		err := pool.QueryRow(r.Context(), findQ, code).Scan(&srcOrgID, &srcName)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "コードが無効か、期限切れです")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "コードの確認に失敗しました")
			return
		}

		newName := strings.TrimSpace(body.Name)
		if newName == "" {
			newName = srcName
		}

		// 新組織作成＋実行者をadmin＋players/maps複製を1トランザクションで
		tx, err := pool.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理を開始できませんでした")
			return
		}
		defer tx.Rollback(r.Context())

		var org organization
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO organizations (name, owner_user_id)
			 VALUES ($1, $2)
			 RETURNING id, name, level_display_mode, created_at`, newName, user.ID).
			Scan(&org.ID, &org.Name, &org.LevelDisplayMode, &org.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "組織の作成に失敗しました")
			return
		}
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'admin')`,
			org.ID, user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "メンバー登録に失敗しました")
			return
		}
		// players 複製（名前・レベルのみ。勝率などは複製しない）
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO players (organization_id, name, level)
			 SELECT $1, name, level FROM players WHERE organization_id = $2`, org.ID, srcOrgID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "プレイヤーの複製に失敗しました")
			return
		}
		// maps 複製（include_in_random フラグも引き継ぐ）
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO maps (organization_id, name, include_in_random)
			 SELECT $1, name, include_in_random FROM maps WHERE organization_id = $2`, org.ID, srcOrgID); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "マップの複製に失敗しました")
			return
		}

		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "処理の確定に失敗しました")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": org})
	}
}
