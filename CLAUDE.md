@AGENTS.md

# ow-custom-app

オーバーウォッチのカスタムゲーム用チーム管理・試合記録アプリ。
詳細仕様は SPEC.md を参照。

## 技術スタック

| 役割 | 技術 |
|------|------|
| フロントエンド | Next.js (TypeScript) + Tailwind CSS |
| バックエンド | Go |
| DB | PostgreSQL |
| 認証 | Google OAuth |

## プロジェクト構成

```
ow-custom-app/
├── src/                      # Next.js フロントエンド
├── backend/                  # Go APIサーバー
├── docs/
│   ├── DB_DESIGN.md          # DB設計（テーブル定義・集計ビュー）
│   └── API_DESIGN.md         # API設計（エンドポイント一覧）
├── CLAUDE.md
├── SPEC.md                   # 詳細仕様書
└── TASKS.md                  # 開発タスク・進捗管理
```

## 設計ドキュメント

- **仕様**: [SPEC.md](SPEC.md)
- **DB設計**: [docs/DB_DESIGN.md](docs/DB_DESIGN.md)
- **API設計**: [docs/API_DESIGN.md](docs/API_DESIGN.md)
- **タスク管理**: [TASKS.md](TASKS.md)

## サーバー起動方法

### フロントエンド（Next.js）
```bash
npm run dev
# http://localhost:3000
```

### バックエンド（Go）
```bash
cd backend
go run main.go
# http://localhost:8080
```

## 開発方針

- コードレビュー時はポイント解説とコメントをつける
- AI駆動開発（Claude Code活用）で進める
- レスポンシブ対応（PC・スマホ両対応）
