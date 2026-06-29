-- player_stats: プレイヤーごとの勝率・試合数・観戦数を集計するビュー。
-- 実テーブルではなく「毎回 match_players / matches から計算する仮想テーブル」。
-- players を起点に LEFT JOIN するので、1試合もしていないプレイヤーも 0 で必ず1行出る。
CREATE VIEW player_stats AS
SELECT
  p.id AS player_id,
  -- プレイした試合数（観戦は除外、完了した試合のみ）
  COUNT(*) FILTER (WHERE mp.team IN ('blue', 'red') AND m.status = 'completed') AS match_count,
  -- 勝利数（自分のチーム = 勝者チーム）
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
