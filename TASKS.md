# タスク管理

## デプロイ方針（決定済み）

| 役割 | サービス | 備考 |
|------|----------|------|
| フロントエンド | Vercel | 無料・自動スケール |
| バックエンド（Go） | Google Cloud Run | 月200万リクエスト無料・ゼロスケール |
| DB（PostgreSQL） | Supabase | 500MB無料 |

- 基本無料で運用
- ユーザー増加に伴い自動スケール
- Google OAuth との相性も考慮済み
- 開発完了後にセットアップ予定

---

## 開発方針（決定済み）

- **進め方**: バックエンドから実装
- **設計**: 全体のDB設計・API設計をまとめてから実装に入る
- **開発環境**: Docker で PostgreSQL を立ち上げる

---

## 開発タスク

### Phase 1: 設計
- [x] DB設計（テーブル定義） → [docs/DB_DESIGN.md](docs/DB_DESIGN.md)
- [x] API設計（エンドポイント一覧） → [docs/API_DESIGN.md](docs/API_DESIGN.md)
- [x] 未決事項の確定
  - レベル自動調整: 勝率ベースで±2まで動く（1〜10にクランプ）
  - 共有コード: 4桁英数字、1週間で失効
  - 複製範囲: プレイヤーとマップのみ
  - セッション: 「カスタムスタート/終了」ボタンで明示的に区切る

### Phase 2: 環境構築
- [x] Docker Compose で PostgreSQL 起動（ホスト側 5433 → コンテナ 5432）
- [x] DBマイグレーション基盤導入（`golang-migrate`）と初期スキーマ適用（11テーブル）
- [x] Go バックエンドの基本構成（ルーティング・DB接続）
- [x] Google OAuth ログイン最小版（login/callback ハンドラ・userinfo 取得）
- [x] users テーブル upsert + セッション発行（3D）／`/auth/me`・`/auth/logout` 実装

### Phase 3: バックエンド実装
- [x] セッション検証ミドルウェア（`RequireAuth`）＋ `/auth/me` をミドルウェア経由に
- [x] 組織作成 API（`POST /organizations`／作成者を admin として所属）
- [x] 組織 API 残り（一覧 `GET /organizations`・取得 `GET /organizations/:id`・表示モード切替 `PUT .../level-display`）＋ member/admin 権限チェック（`orgRole` ヘルパー）
- [x] プレイヤー管理 API（一覧=member/追加=admin/変更=admin+level_changes履歴/削除=admin、18人上限、`player_stats` ビュー追加）
- [x] マップ管理 API（一覧=member/追加=admin/削除=admin、`mapOrgID` ヘルパー）
- [x] マップ「ランダム対象フラグ」(`include_in_random`) 導入＋`PUT /maps/:id`（部分更新）／セッション別マップ選択(session_maps)を廃止して永続フラグに一本化（migration 000005/000006）
  - フォロー: ランダム抽選の実処理・ランダム/選択モード切替（フロント）、全マップ一括リセットAPI、`map_selection_mode` enum見直し
- [x] セッション API（開始=admin/進行中1つ制限・取得=member/終了=admin、`sessionOrgID` ヘルパー）
- [x] セッション結果サマリー API（`GET /sessions/:id/summary`、member。完了試合のみ集計・キャンセル除外、`summary.go`）
- [x] 試合記録 API（開始=admin/勝敗記録=admin/キャンセル=admin、`player_stats` へ実データ反映を確認、`matchInfo`/`matchTeams` ヘルパー）
  - ついでに `matches.map_id` FK を ON DELETE SET NULL に修正（使用中マップの削除が500になる問題、migration 000004）
- [x] チーム分け API（`POST /sessions/:id/teams/auto`、admin。レベル均等の貪欲振り分け＋観戦数の少ない人から観戦へ。提案を返すのみ・DB保存なし、`teams.go`）
- [x] レベル自動調整 API（`GET /sessions/:id/level-suggestion` 提案・`POST /sessions/:id/apply-level-changes` 適用、admin。勝率で±2クランプ、二重適用は409、`levels.go`）※手動変更は PUT /players/:id で実装済み。履歴一覧・アンドゥは残り
- [ ] レベル変更履歴一覧・アンドゥ API（`GET /organizations/:orgId/level-changes`・`POST /level-changes/:id/undo`）
- [x] 共有コード API（発行=admin/4桁英数字・7日失効・衝突再生成、インポート=認証済/新org作成＋players・maps複製(include_in_random含む)・期限切れ404、`sharecodes.go`）
- [ ] **セッション「カスタム終了」のアンドゥ API**
  - 誤って「カスタム終了」を押した場合、セッションを再開できるようにする
  - `ended_at` を NULL に戻す
  - レベル調整がすでに適用されていた場合は併せてロールバック

### Phase 4: フロントエンド実装
- [ ] 認証画面（Google OAuth）
- [ ] プレイヤー管理画面
- [ ] チーム分け・試合画面
- [ ] 統計・結果画面
- [ ] **「カスタム終了」誤操作時のリカバリーUI**
  - 終了直後（レベル調整ポップアップ表示中・確定前）はキャンセルボタンで戻せる
  - 確定後でも「セッションを再開」ボタンで戻せるようにする（要確認: 戻せる時間制限を設けるか）

### Phase 5: デプロイ
- [ ] Vercel（フロントエンド）
- [ ] Google Cloud Run（バックエンド）
- [ ] Supabase（PostgreSQL）
