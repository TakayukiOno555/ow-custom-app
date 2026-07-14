-- maps.include_in_random: ランダム抽選の母集団に含めるかの永続フラグ。
-- ランダムモードではこのフラグが true のマップだけから抽選する。
-- 直接指定（選択モード）はフラグに関係なく任意のマップを選べる。
-- 既存マップは全て対象扱い（true）にしておく。
ALTER TABLE maps ADD COLUMN include_in_random BOOLEAN NOT NULL DEFAULT true;
