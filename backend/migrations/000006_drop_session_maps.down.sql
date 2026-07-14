-- ロールバック用: session_maps を 000001 と同じ定義で作り直す。
CREATE TABLE session_maps (
  session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  map_id UUID NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
  PRIMARY KEY (session_id, map_id)
);
