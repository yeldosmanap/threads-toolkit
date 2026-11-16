package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Project struct {
	ID               int64
	UserID           int64
	Name             string
	Market           string
	Aliases          []string
	OfficialAccounts []string
	Competitors      []string
	Notes            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// Latest run (joined), nil if never run
	LastJobID     *int64
	LastJobStatus *string
	RunsCount     int
}

type ProjectInput struct {
	Name             string
	Market           string
	Aliases          []string
	OfficialAccounts []string
	Competitors      []string
	Notes            string
}

const projectColumns = `p.id, p.user_id, p.name, p.market, p.aliases, p.official_accounts, p.competitors, p.notes,
	p.created_at, p.updated_at,
	(SELECT j.id FROM jobs j WHERE j.project_id = p.id AND j.kind = 'search' AND j.deleted_at IS NULL ORDER BY j.created_at DESC LIMIT 1),
	(SELECT j.status FROM jobs j WHERE j.project_id = p.id AND j.kind = 'search' AND j.deleted_at IS NULL ORDER BY j.created_at DESC LIMIT 1),
	(SELECT count(*)::int FROM jobs j WHERE j.project_id = p.id AND j.kind = 'search' AND j.deleted_at IS NULL)`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Market, &p.Aliases, &p.OfficialAccounts, &p.Competitors, &p.Notes,
		&p.CreatedAt, &p.UpdatedAt, &p.LastJobID, &p.LastJobStatus, &p.RunsCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ProjectKeywordNorm makes all runs of a project "the same search" for new-post detection.
func ProjectKeywordNorm(projectID int64) string {
	return fmt.Sprintf("project:%d", projectID)
}

func (s *Store) CountProjects(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

func (s *Store) CreateProject(ctx context.Context, userID int64, in ProjectInput) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		`INSERT INTO projects AS p (user_id, name, market, aliases, official_accounts, competitors, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+projectColumns,
		userID, in.Name, in.Market, in.Aliases, nonNil(in.OfficialAccounts), nonNil(in.Competitors), in.Notes))
}

func (s *Store) UpdateProject(ctx context.Context, userID, id int64, in ProjectInput) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		`UPDATE projects p SET name = $3, market = $4, aliases = $5, official_accounts = $6, competitors = $7,
			notes = $8, updated_at = now()
		 WHERE p.id = $1 AND p.user_id = $2
		 RETURNING `+projectColumns,
		id, userID, in.Name, in.Market, in.Aliases, nonNil(in.OfficialAccounts), nonNil(in.Competitors), in.Notes))
}

func (s *Store) ListProjects(ctx context.Context, userID int64) ([]Project, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+projectColumns+` FROM projects p WHERE p.user_id = $1 ORDER BY p.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, userID, id int64) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects p WHERE p.id = $1 AND p.user_id = $2`, id, userID))
}

// GetProjectByID is for internal use (analyzer) - no ownership check.
func (s *Store) GetProjectByID(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects p WHERE p.id = $1`, id))
}

// DeleteProject removes the project and its labels; its runs stay in the history as plain searches.
func (s *Store) DeleteProject(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1 AND user_id = $2`, id, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CreateProjectJob queues a run that searches every alias of the project.
func (s *Store) CreateProjectJob(ctx context.Context, p Project, filter string, maxItems int, scheduleID *int64) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx,
		`INSERT INTO jobs AS j (user_id, keyword, keyword_norm, filter, max_items, project_id, keywords, schedule_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+jobColumns,
		p.UserID, p.Name, ProjectKeywordNorm(p.ID), filter, maxItems, p.ID, p.Aliases, scheduleID))
}
