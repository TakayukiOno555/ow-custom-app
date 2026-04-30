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
├── src/          # Next.js フロントエンド
├── backend/      # Go APIサーバー
├── CLAUDE.md
└── SPEC.md       # 詳細仕様書
```

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
