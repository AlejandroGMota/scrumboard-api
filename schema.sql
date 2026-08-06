-- The DESIRED schema, hand-written. Atlas diffs against this file to generate
-- the versioned migrations in migrations/ (01-structure.md §1.8).
--
-- Two conventions run through it:
--   * Timestamps are TEXT in RFC 3339, always UTC. Conversion to local time
--     happens in the browser and nowhere else (00-scope.md §11 question 5).
--   * deleted_at on every entity. With deferred sync, removing a row makes
--     "this was deleted" indistinguishable from "this has not arrived yet"
--     (00-scope.md §5).

CREATE TABLE boards (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  -- What each palette colour means on this board, as a JSON object keyed by
  -- colour. Bounded (six short strings), always read and written with the
  -- board, so a table would cost a join and buy nothing.
  label_names TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE columns (
  id         TEXT PRIMARY KEY,
  board_id   TEXT NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  position   INTEGER NOT NULL,
  wip_limit  INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT
);

-- Live columns of a board, already in the order the board is painted in.
CREATE INDEX idx_columns_board_position ON columns (board_id, position);

CREATE TABLE cards (
  id          TEXT PRIMARY KEY,
  column_id   TEXT NOT NULL REFERENCES columns (id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  label       TEXT NOT NULL DEFAULT '',
  position    INTEGER NOT NULL,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  deleted_at  TEXT
);

CREATE INDEX idx_cards_column_position ON cards (column_id, position);

-- Idempotency ledger for the offline queue: which client operations have
-- already been applied. A phone that lost signal mid-POST resends the whole
-- batch, and this is what makes that harmless (01-structure.md §1.5).
CREATE TABLE op_log (
  op_id      TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
);
