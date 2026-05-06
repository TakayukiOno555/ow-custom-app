-- UUID 生成関数 gen_random_uuid() を有効化
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- users: 認証ユーザー
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  google_id TEXT UNIQUE NOT NULL,
  email TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- organizations: データの所属単位（カスタム会グループ）
CREATE TABLE organizations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  owner_user_id UUID NOT NULL REFERENCES users(id),
  level_display_mode TEXT NOT NULL DEFAULT 'visible'
    CHECK (level_display_mode IN ('visible', 'hidden')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- organization_members: ユーザー × 組織 の多対多 + 権限
CREATE TABLE organization_members (
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, user_id)
);

-- players: プレイヤー
CREATE TABLE players (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  level SMALLINT NOT NULL DEFAULT 5 CHECK (level >= 1 AND level <= 10),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_players_organization_id ON players (organization_id);

-- maps: マップ・モード
CREATE TABLE maps (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- sessions: 1日のカスタム会
CREATE TABLE sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ NULL,
  team_size SMALLINT NOT NULL DEFAULT 5 CHECK (team_size IN (5, 6)),
  map_selection_mode TEXT NOT NULL DEFAULT 'rotation'
    CHECK (map_selection_mode IN ('rotation', 'random'))
);

-- session_maps: そのセッションで使うマップ
CREATE TABLE session_maps (
  session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  map_id UUID NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
  PRIMARY KEY (session_id, map_id)
);

-- matches: 試合
CREATE TABLE matches (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  map_id UUID NULL REFERENCES maps(id),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  winner_team TEXT NULL
    CHECK (winner_team IS NULL OR winner_team IN ('blue', 'red')),
  status TEXT NOT NULL DEFAULT 'in_progress'
    CHECK (status IN ('in_progress', 'completed', 'cancelled'))
);
CREATE INDEX idx_matches_session_id ON matches (session_id);

-- match_players: 試合に参加したプレイヤー（観戦者含む）
CREATE TABLE match_players (
  match_id UUID NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
  player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
  team TEXT NOT NULL CHECK (team IN ('blue', 'red', 'spectator')),
  PRIMARY KEY (match_id, player_id)
);
CREATE INDEX idx_match_players_player_id ON match_players (player_id);

-- level_changes: レベル変更履歴（アンドゥ用）
CREATE TABLE level_changes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
  old_level SMALLINT NOT NULL,
  new_level SMALLINT NOT NULL,
  change_type TEXT NOT NULL CHECK (change_type IN ('manual', 'auto')),
  changed_by_user_id UUID NOT NULL REFERENCES users(id),
  session_id UUID NULL REFERENCES sessions(id) ON DELETE SET NULL,
  reverted_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_level_changes_player_created
  ON level_changes (player_id, created_at DESC);

-- share_codes: 共有コード（4桁英数字、1週間で失効）
CREATE TABLE share_codes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  code CHAR(4) UNIQUE NOT NULL,
  created_by_user_id UUID NOT NULL REFERENCES users(id),
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_share_codes_code ON share_codes (code);
