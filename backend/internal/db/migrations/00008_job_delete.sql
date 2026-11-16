-- +goose Up
-- A running job deleted by the user: hidden at once, the worker is asked to stop, and the
-- row is removed when the worker reports back (or when it goes stale).
ALTER TABLE jobs ADD COLUMN deleted_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE jobs DROP COLUMN deleted_at;
