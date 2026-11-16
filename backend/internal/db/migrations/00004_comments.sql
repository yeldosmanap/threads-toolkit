-- +goose Up
-- 'comments' jobs are created by the analyzer after a project run: collect the comments under
-- the most discussed negative posts. They are system jobs - not counted in user quotas or listed.
ALTER TABLE jobs
    ADD COLUMN kind          TEXT NOT NULL DEFAULT 'search' CHECK (kind IN ('search', 'comments')),
    ADD COLUMN parent_job_id BIGINT REFERENCES jobs (id) ON DELETE CASCADE,
    ADD COLUMN post_urls     TEXT[];
CREATE INDEX jobs_parent ON jobs (parent_job_id) WHERE parent_job_id IS NOT NULL;

-- A comment points to the post it was collected under
ALTER TABLE posts ADD COLUMN parent_id TEXT;
CREATE INDEX posts_parent ON posts (parent_id) WHERE parent_id IS NOT NULL;

-- +goose Down
ALTER TABLE posts DROP COLUMN parent_id;
ALTER TABLE jobs DROP COLUMN kind, DROP COLUMN parent_job_id, DROP COLUMN post_urls;
