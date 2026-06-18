-- user_sessions: ログインセッション（Google OAuth 後に発行）
-- Cookie には id（ランダムトークン）だけを入れ、毎リクエストここを照合してユーザーを特定する。
-- ログアウト = この行を削除。期限切れ = expires_at を過ぎた行。
CREATE TABLE user_sessions (
  id TEXT PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_user_sessions_user_id ON user_sessions (user_id);
