-- session_maps（セッションごとの都度マップ選択）を廃止する。
-- 「使うマップ」は maps.include_in_random（永続フラグ）で管理する方式に一本化したため不要。
DROP TABLE IF EXISTS session_maps;
