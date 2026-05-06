# ow-custom-app

オーバーウォッチのカスタムゲーム用チーム管理・試合記録アプリ。

## ドキュメント

- [SPEC.md](SPEC.md) — 詳細仕様
- [docs/DB_DESIGN.md](docs/DB_DESIGN.md) — DB設計
- [docs/API_DESIGN.md](docs/API_DESIGN.md) — API設計
- [TASKS.md](TASKS.md) — 開発タスク・進捗

## 技術スタック

| 役割 | 技術 |
|------|------|
| フロントエンド | Next.js (TypeScript) + Tailwind CSS |
| バックエンド | Go |
| DB | PostgreSQL |
| 認証 | Google OAuth |

## 開発環境のセットアップ

### 前提：必要なソフト

以下を事前にインストールしてください。

| ソフト | 用途 | ダウンロード |
|--------|------|-------------|
| **Docker Desktop** | PostgreSQL をローカルで動かす | https://www.docker.com/products/docker-desktop/ |
| **Node.js**（v20 以上推奨） | フロントエンド（Next.js）の実行 | https://nodejs.org/ |
| **Go**（v1.21 以上推奨） | バックエンドの実行 | https://go.dev/dl/ |

> 💡 Docker Desktop はインストール後、**アプリを起動して常駐させた状態**で開発する必要があります（タスクバーに🐳アイコンが出ていればOK）。

### 初回セットアップ手順

#### 1. リポジトリ取得後、環境変数ファイルを作成

```powershell
Copy-Item .env.example .env
```

`.env.example` は Git 管理されているサンプル。`.env` は実際の値を入れる本物（Git 管理外）。

#### 2. PostgreSQL を起動

```powershell
docker compose up -d
```

`-d` はバックグラウンド実行の意味。初回はイメージのダウンロードで数分かかる。

> ⚠️ ホストPCの 5432 番にすでに別のPostgreSQLがいる場合と競合させないため、本プロジェクトは **ホスト側 5433 → コンテナ内 5432** にマッピングしている。接続URLは `localhost:5433` になる。

起動確認：

```powershell
docker compose ps
```

`postgres` の STATUS が `Up (healthy)` になっていれば成功。

#### 3. DBマイグレーション適用（テーブル作成）

`golang-migrate` の CLI をインストール（初回のみ）：

```powershell
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

マイグレーション実行：

```powershell
migrate -path backend/migrations -database "postgres://ow_user:ow_password@localhost:5433/ow_custom_app?sslmode=disable" up
```

> 💡 PowerShell で `migrate.exe` が「アクセスが拒否されました」になる場合は Windows Defender のブロック。Git Bash から実行するか、`%USERPROFILE%\go\bin` を Defender の除外フォルダに追加。

確認：

```powershell
docker compose exec postgres psql -U ow_user -d ow_custom_app -c "\dt"
```

11個のテーブル + `schema_migrations` が表示されればOK。

#### 4. フロントエンドの依存パッケージをインストール

```powershell
npm install
```

#### 5. バックエンドの依存パッケージをインストール

```powershell
cd backend
go mod tidy
cd ..
```

## サーバー起動方法

### フロントエンド（Next.js）

```powershell
npm run dev
```

→ http://localhost:3000

### バックエンド（Go）

```powershell
cd backend
go run main.go
```

→ http://localhost:8080

### PostgreSQL（停止・再起動）

```powershell
# 停止
docker compose stop

# 再起動
docker compose start

# 完全削除（データも消える点に注意）
docker compose down -v
```

## 開発方針

- コードレビュー時はポイント解説とコメントをつける
- AI駆動開発（Claude Code活用）で進める
- レスポンシブ対応（PC・スマホ両対応）
