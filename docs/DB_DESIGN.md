# DB設計

PostgreSQL を使用。

---

## ER図（概念）

```
users ──┬── organizations ──┬── players
        │                    ├── maps
        │                    ├── sessions ──┬── matches ── match_players
        │                    │              └── level_changes
        │                    └── share_codes
        └── (organization_membersで多対多)
```

---

## テーブル一覧

### users
認証ユーザー。Google OAuth でログイン。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| google_id | TEXT UNIQUE NOT NULL | Google のサブジェクトID |
| email | TEXT NOT NULL | |
| name | TEXT NOT NULL | |
| created_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

### organizations
データの所属単位。共有コードで複製される単位＝1つの「カスタム会グループ」。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| name | TEXT NOT NULL | グループ名 |
| owner_user_id | UUID NOT NULL FK→users(id) | 作成者 |
| level_display_mode | TEXT NOT NULL DEFAULT 'visible' | 'visible' / 'hidden' |
| created_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

### organization_members
ユーザーと組織の多対多。権限もここで持つ。

| カラム | 型 | 説明 |
|--------|----|----|
| organization_id | UUID FK→organizations(id) | |
| user_id | UUID FK→users(id) | |
| role | TEXT NOT NULL | 'admin' / 'member' |
| created_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |
| PK | (organization_id, user_id) | |

### players
プレイヤー。1組織あたり最大18人（アプリ側で制御）。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| organization_id | UUID NOT NULL FK→organizations(id) | |
| name | TEXT NOT NULL | |
| level | SMALLINT NOT NULL DEFAULT 5 | 1〜10（CHECK制約） |
| created_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |
| updated_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

> 勝率・試合数は `match_players` から集計するため列を持たない。

### maps
マップ・モード。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| organization_id | UUID NOT NULL FK→organizations(id) | |
| name | TEXT NOT NULL | 例: "Hanamura - Assault" |
| include_in_random | BOOLEAN NOT NULL DEFAULT true | ランダム抽選の母集団に含めるか（永続フラグ） |
| created_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

### sessions
1日のカスタム会。`started_at` 〜 `ended_at` の期間で1つ。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| organization_id | UUID NOT NULL FK→organizations(id) | |
| started_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |
| ended_at | TIMESTAMPTZ NULL | NULL のとき進行中 |
| team_size | SMALLINT NOT NULL DEFAULT 5 | 5 or 6 |
| map_selection_mode | TEXT NOT NULL DEFAULT 'rotation' | 'rotation' / 'random' |

> 仮定: セッションは管理者が明示的に「開始 / 終了」する。

> ~~session_maps（セッションごとのマップ都度選択）は廃止~~（migration 000006）。
> 「使うマップ」は `maps.include_in_random`（永続フラグ）で管理する方式に一本化した。

### matches
試合。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| session_id | UUID NOT NULL FK→sessions(id) | |
| map_id | UUID NULL FK→maps(id) | |
| started_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |
| winner_team | TEXT NULL | 'blue' / 'red' / NULL（未決定／キャンセル） |
| status | TEXT NOT NULL DEFAULT 'in_progress' | 'in_progress' / 'completed' / 'cancelled' |

### match_players
試合に参加したプレイヤー（プレイヤー＋観戦者）。

| カラム | 型 | 説明 |
|--------|----|----|
| match_id | UUID FK→matches(id) | |
| player_id | UUID FK→players(id) | |
| team | TEXT NOT NULL | 'blue' / 'red' / **'spectator'** |
| PK | (match_id, player_id) | |

> **観戦者の扱い**: `team = 'spectator'` のレコードは「その試合に居たが、プレイしていない」ことを表す。
> - 勝率・試合数の集計対象外
> - 観戦数のカウント対象（誰が何回観戦したか把握できる）
> - レベル自動調整の対象外（プレイしていないため）

### level_changes
レベル変更履歴（アンドゥ用）。手動・自動どちらも残す。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| player_id | UUID NOT NULL FK→players(id) | |
| old_level | SMALLINT NOT NULL | |
| new_level | SMALLINT NOT NULL | |
| change_type | TEXT NOT NULL | 'manual' / 'auto' |
| changed_by_user_id | UUID NOT NULL FK→users(id) | |
| session_id | UUID NULL FK→sessions(id) | 自動調整時のみ |
| reverted_at | TIMESTAMPTZ NULL | アンドゥされたら時刻を入れる |
| created_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

### share_codes
共有コード（プレイヤーデータ複製用）。**4桁の英数字、発行から1週間で失効**。

| カラム | 型 | 説明 |
|--------|----|----|
| id | UUID PK | |
| organization_id | UUID NOT NULL FK→organizations(id) | |
| code | CHAR(4) UNIQUE NOT NULL | 4桁英数字（A-Z, 0-9 など） |
| created_by_user_id | UUID NOT NULL FK→users(id) | |
| expires_at | TIMESTAMPTZ NOT NULL | 発行から +7日 |
| created_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

> 4桁の英数字 = 約160万通り（36^4）。衝突時は再生成。
> インポート時は `expires_at > now()` を必須チェック。

---

## インデックス

- `players (organization_id)`
- `matches (session_id)`
- `match_players (player_id)` … 統計集計用
- `level_changes (player_id, created_at DESC)` … アンドゥ用
- `share_codes (code)` UNIQUE

---

## 集計ビュー（候補）

### player_stats
プレイヤーごとの勝率・試合数。

```sql
CREATE VIEW player_stats AS
SELECT
  p.id AS player_id,
  -- プレイした試合数（観戦は除外）
  COUNT(*) FILTER (WHERE mp.team IN ('blue', 'red') AND m.status = 'completed') AS match_count,
  -- 勝利数（観戦は除外）
  COUNT(*) FILTER (WHERE mp.team IN ('blue', 'red')
                     AND m.status = 'completed'
                     AND mp.team = m.winner_team) AS win_count,
  -- 観戦数
  COUNT(*) FILTER (WHERE mp.team = 'spectator' AND m.status = 'completed') AS spectator_count,
  -- 勝率（プレイした試合数が0なら0）
  CASE WHEN COUNT(*) FILTER (WHERE mp.team IN ('blue', 'red') AND m.status = 'completed') = 0
       THEN 0
       ELSE COUNT(*) FILTER (WHERE mp.team IN ('blue', 'red')
                               AND m.status = 'completed'
                               AND mp.team = m.winner_team)::float
            / COUNT(*) FILTER (WHERE mp.team IN ('blue', 'red') AND m.status = 'completed')
  END AS win_rate
FROM players p
LEFT JOIN match_players mp ON mp.player_id = p.id
LEFT JOIN matches m ON m.id = mp.match_id
GROUP BY p.id;
```

> 例: 2勝0敗1観戦 → match_count=2, win_count=2, spectator_count=1, **win_rate=1.0（100%）**

---

## 仕様確定事項

### レベル自動調整アルゴリズム
セッション内の勝率をもとに、複数レベル動く形で提案する。

| そのセッションでの勝率 | 提案するレベル変化 |
|----------------------|------------------|
| ≥ 70% | +2 |
| ≥ 60% かつ < 70% | +1 |
| > 40% かつ < 60% | 変化なし |
| ≤ 40% かつ > 30% | -1 |
| ≤ 30% | -2 |

**重要: レベルは必ず 1〜10 の範囲内に収める**
- 計算結果が 10 を超える場合 → 10 にクランプ
- 計算結果が 1 を下回る場合 → 1 にクランプ
- 例: レベル9のプレイヤーが勝率80%でも +2 ではなく **+1（→10）** に丸める
- 例: レベル1のプレイヤーが勝率20%でも **変化なし（1のまま）**

その他:
- セッション中の**プレイ試合数が0**なら自動調整の対象外（観戦のみのプレイヤーも含む）
- 勝率は `team IN ('blue', 'red')` の試合のみで計算（観戦試合は分母に含めない）
- セッション中の試合数が少なすぎる場合（例: 1試合のみ）の扱いは要検討

### 共有コードの有効期限
- 発行から **1週間（7日）** で自動失効
- インポート時に `expires_at > now()` をチェック

### セッションの区切り
- 管理者が明示的に「**カスタムスタート**」「**カスタム終了**」ボタンで区切る
- 「カスタム終了」を押したタイミングでレベル自動調整ポップアップが表示される
- 同時に複数のセッションは持たない（1組織につき進行中は最大1つ）
