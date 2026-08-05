-- Initial schema: boards, columns and cards, all with soft deletes.

CREATE TABLE boards (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT
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
