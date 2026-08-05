-- The idempotency ledger for the offline write queue.

CREATE TABLE op_log (
  op_id      TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
);
