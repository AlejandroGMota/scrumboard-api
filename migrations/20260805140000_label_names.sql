-- Per-board meanings for the six palette colours, so a legend can say what
-- "blue" stands for on this board.
--
-- Stored as a JSON object keyed by colour ({"blue":"Admin"}). A table would
-- be the orthodox choice, but this is a bounded map of at most six short
-- strings that is always read and written whole, with the board — a join for
-- that buys nothing and costs a query on the call that paints the screen.

ALTER TABLE boards ADD COLUMN label_names TEXT NOT NULL DEFAULT '{}';
