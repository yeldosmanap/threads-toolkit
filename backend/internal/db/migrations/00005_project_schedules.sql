-- +goose Up
-- A schedule runs either a keyword search or a whole project (all aliases + analysis + comments).
ALTER TABLE schedules ADD COLUMN project_id BIGINT REFERENCES projects (id) ON DELETE CASCADE;
CREATE INDEX schedules_project ON schedules (project_id) WHERE project_id IS NOT NULL;

-- +goose Down
ALTER TABLE schedules DROP COLUMN project_id;
