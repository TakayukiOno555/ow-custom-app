-- matches.map_id の外部キーを ON DELETE SET NULL に変更する。
-- 試合で使ったマップを後から削除しても、試合記録は残し map_id だけ NULL にする
-- （元は削除時の挙動が未指定＝参照されていると maps の削除が拒否され500になっていた）。
ALTER TABLE matches DROP CONSTRAINT matches_map_id_fkey;
ALTER TABLE matches ADD CONSTRAINT matches_map_id_fkey
  FOREIGN KEY (map_id) REFERENCES maps(id) ON DELETE SET NULL;
