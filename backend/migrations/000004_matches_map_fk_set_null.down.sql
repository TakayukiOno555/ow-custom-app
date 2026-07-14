ALTER TABLE matches DROP CONSTRAINT matches_map_id_fkey;
ALTER TABLE matches ADD CONSTRAINT matches_map_id_fkey
  FOREIGN KEY (map_id) REFERENCES maps(id);
