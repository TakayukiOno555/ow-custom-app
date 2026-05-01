# API設計

ベースURL: `http://localhost:8080/api/v1`

認証: Cookie ベースのセッション（Google OAuth後にセッションCookieを発行）。

レスポンスはすべて JSON。エラーは `{ "error": { "code": "...", "message": "..." } }` 形式。

---

## エンドポイント一覧

### 認証

| メソッド | パス | 説明 |
|---------|------|------|
| GET | `/auth/google/login` | Googleログイン開始（リダイレクト） |
| GET | `/auth/google/callback` | OAuthコールバック |
| POST | `/auth/logout` | ログアウト |
| GET | `/auth/me` | 現在のユーザー情報 |

### 組織（カスタム会グループ）

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| POST | `/organizations` | 認証済 | 組織作成（作成者がadmin） |
| GET | `/organizations` | 認証済 | 自分が所属する組織一覧 |
| GET | `/organizations/:id` | member | 組織情報 |
| PUT | `/organizations/:id/level-display` | admin | 表示モード切替 (`visible`/`hidden`) |

### プレイヤー

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| GET | `/organizations/:orgId/players` | member | プレイヤー一覧（勝率・試合数・観戦数含む） |
| POST | `/organizations/:orgId/players` | admin | プレイヤー追加 |
| PUT | `/players/:id` | admin | 名前・レベル変更 |
| DELETE | `/players/:id` | admin | プレイヤー削除 |

レベル変更時は自動で `level_changes` に履歴を記録。

レスポンス例 (`GET /organizations/:orgId/players`):
```json
{
  "data": [
    {
      "id": "...",
      "name": "...",
      "level": 5,
      "match_count": 10,
      "win_count": 6,
      "win_rate": 0.6,
      "spectator_count": 2
    }
  ]
}
```

### マップ

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| GET | `/organizations/:orgId/maps` | member | マップ一覧 |
| POST | `/organizations/:orgId/maps` | admin | マップ追加 |
| DELETE | `/maps/:id` | admin | マップ削除 |

### セッション

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| POST | `/organizations/:orgId/sessions` | admin | セッション開始（team_size, map_selection_mode指定） |
| POST | `/sessions/:id/maps` | admin | 使用するマップを選択（IDの配列） |
| POST | `/sessions/:id/end` | admin | セッション終了 |
| GET | `/sessions/:id` | member | セッション情報 |
| GET | `/sessions/:id/summary` | member | 結果サマリー |
| GET | `/sessions/:id/level-suggestion` | admin | レベル自動調整提案を取得 |
| POST | `/sessions/:id/apply-level-changes` | admin | 提案を適用 |

レスポンス例 (`GET /sessions/:id/level-suggestion`):
```json
{
  "data": [
    {
      "player_id": "...",
      "name": "...",
      "current_level": 5,
      "session_match_count": 4,
      "session_win_count": 3,
      "session_win_rate": 0.75,
      "session_spectator_count": 1,
      "suggested_level": 6
    }
  ]
}
```

> プレイ試合数0（観戦のみ・不参加）のプレイヤーは `suggested_level = current_level` で返す。

### チーム分け

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| POST | `/sessions/:id/teams/auto` | admin | 自動チーム分け（参加プレイヤーIDの配列を渡す。team_size×2 を超えた分は観戦者へ自動振り分け） |

リクエスト例:
```json
{
  "player_ids": ["...", "...", "...", "..."]
}
```
※ session の `team_size` を参照し、`team_size × 2` 名がプレイ枠、残りが観戦者枠。

レスポンス例:
```json
{
  "blue":       [{"id": "...", "name": "...", "level": 7}],
  "red":        [{"id": "...", "name": "...", "level": 6}],
  "spectators": [{"id": "...", "name": "...", "level": 5, "spectator_count": 3}]
}
```

> 観戦者選定のヒント: 過去の観戦数が少ない順に選ぶと公平。アルゴリズム詳細は実装時に検討。

### 試合

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| POST | `/sessions/:id/matches` | admin | 試合開始（チーム構成・マップ指定） |
| POST | `/matches/:id/result` | admin | 勝敗記録 (`winner_team`: blue/red) |
| DELETE | `/matches/:id` | admin | 試合キャンセル（`status`→cancelled） |

リクエスト例 (`POST /sessions/:id/matches`):
```json
{
  "map_id": "...",
  "blue_player_ids":      ["...", "..."],
  "red_player_ids":       ["...", "..."],
  "spectator_player_ids": ["...", "..."]
}
```

> 観戦者は試合に居合わせるが、勝率・試合数のカウント対象外（観戦数のみカウント）。

### 統計

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| GET | `/organizations/:orgId/stats` | member | 全プレイヤーの勝率・試合数・観戦数・レベル |

レベル表示モードが `hidden` の場合、レスポンスから `level` を除外（admin以外）。

レスポンスには `match_count` / `win_count` / `win_rate` / `spectator_count` を含む。

### レベル変更履歴・アンドゥ

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| GET | `/organizations/:orgId/level-changes` | admin | 変更履歴一覧 |
| POST | `/level-changes/:id/undo` | admin | 指定の変更を取り消し |

### 共有コード

| メソッド | パス | 権限 | 説明 |
|---------|------|------|------|
| POST | `/organizations/:orgId/share-codes` | admin | 共有コード発行 |
| POST | `/share-codes/import` | 認証済 | コードを入力して別組織へインポート |

`import` 時の挙動:
- 新しい organization を作成（実行者がadmin）
- 元組織の players / maps を複製
- 試合履歴・レベル変更履歴は複製しない（要確認）

---

## 共通ルール

### レスポンス形式

成功:
```json
{ "data": { ... } }
```

エラー:
```json
{ "error": { "code": "FORBIDDEN", "message": "管理者権限が必要です" } }
```

### エラーコード（例）
- `UNAUTHORIZED` (401)
- `FORBIDDEN` (403)
- `NOT_FOUND` (404)
- `VALIDATION_ERROR` (400)
- `CONFLICT` (409)（プレイヤー上限18人超過など）

### 権限
- `admin`: 組織のオーナー＋管理者ロール
- `member`: 組織所属の一般ユーザー
- `認証済`: ログインしていれば誰でも

---

## 仕様確定事項

### レベル自動調整
- サーバー側で計算（DB_DESIGN.md の表参照）
- 勝率に応じて ±2 まで動く
- **レベルは必ず 1〜10 の範囲内に収める**（範囲外にならないようクランプ）
- 提案を返すだけ → 管理者が確認 → `apply-level-changes` で確定

### 共有コード
- 4桁英数字、発行から1週間で失効
- 複製範囲: **プレイヤー（名前・レベル）とマップのみ**
- 試合履歴・勝率・レベル変更履歴は複製しない（複製先でゼロから記録）

### セッション
- 「カスタムスタート」/「カスタム終了」ボタンで明示的に区切る
- 「カスタム終了」押下時にレベル調整ポップアップを表示
- 同時に進行中のセッションは1組織につき最大1つ
- チーム編成は試合ごと（1試合ごとに自動/手動でメンバー指定）

### 観戦者の扱い
- 1試合に参加するプレイヤーは「**青チーム**」「**赤チーム**」「**観戦者**」の3区分
- 観戦者は試合数・勝率の集計対象外
- **観戦数（spectator_count）はカウント・表示対象**
  - チーム分け画面でプレイヤー一覧に観戦数を表示（誰の観戦が多いか把握しやすくする）
  - レベル調整画面（セッション終了時）にも観戦数を表示
- レベル自動調整は **プレイした試合の勝率** のみで判定（観戦試合は分母に含めない）
- 例: 2勝0敗1観戦 → 勝率 **100%（2/2）**
