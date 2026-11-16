-- +goose Up
-- The user asked to stop a running job early: the worker stops scrolling, skips the remaining
-- aliases and sends what it has; analysis and exports then run as usual.
ALTER TABLE jobs ADD COLUMN stop_requested BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE jobs DROP COLUMN stop_requested;
