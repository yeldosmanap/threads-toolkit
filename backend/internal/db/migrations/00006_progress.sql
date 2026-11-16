-- +goose Up
-- Project runs search several aliases in turn: which one is running and how far it got.
ALTER TABLE jobs
    ADD COLUMN progress_keyword_index INT,
    ADD COLUMN progress_alias_seen    INT;

-- +goose Down
ALTER TABLE jobs DROP COLUMN progress_keyword_index, DROP COLUMN progress_alias_seen;
