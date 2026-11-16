-- +goose Up
-- Recurring searches. The backend scheduler turns due schedules into ordinary jobs.
CREATE TABLE schedules (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    keyword      TEXT        NOT NULL,
    keyword_norm TEXT        NOT NULL,
    filter       TEXT        NOT NULL CHECK (filter IN ('recent', 'top')),
    max_items    INT         NOT NULL CHECK (max_items > 0),
    -- 'every_hours': every `every_hours` hours; 'daily': every day at `daily_time` in `timezone`
    kind         TEXT        NOT NULL CHECK (kind IN ('every_hours', 'daily')),
    every_hours  INT,
    daily_time   TEXT,
    timezone     TEXT        NOT NULL DEFAULT 'UTC',
    enabled      BOOLEAN     NOT NULL DEFAULT true,
    next_run_at  TIMESTAMPTZ NOT NULL,
    last_run_at  TIMESTAMPTZ,
    -- Why the last occurrence did not start a job (quota, previous run still active), NULL if it did
    last_error   TEXT,
    last_job_id  BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((kind = 'every_hours' AND every_hours IS NOT NULL) OR (kind = 'daily' AND daily_time IS NOT NULL))
);
CREATE INDEX schedules_user ON schedules (user_id, created_at DESC);
CREATE INDEX schedules_due ON schedules (next_run_at) WHERE enabled;

ALTER TABLE jobs ADD COLUMN schedule_id BIGINT REFERENCES schedules (id) ON DELETE SET NULL;
CREATE INDEX jobs_schedule ON jobs (schedule_id) WHERE schedule_id IS NOT NULL;

-- +goose Down
ALTER TABLE jobs DROP COLUMN schedule_id;
DROP TABLE schedules;
