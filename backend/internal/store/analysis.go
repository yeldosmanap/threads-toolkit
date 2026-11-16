package store

import (
	"context"
)

// ClaimAnalysis takes the oldest project run waiting for analysis and marks it running.
func (s *Store) ClaimAnalysis(ctx context.Context) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx,
		`WITH next AS (
			SELECT id FROM jobs WHERE analysis_status = 'pending'
			ORDER BY finished_at LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE jobs j SET analysis_status = 'running', analysis_error = NULL
		FROM next WHERE j.id = next.id
		RETURNING `+jobColumns))
}

// ResetRunningAnalyses puts analyses interrupted by a backend restart back in the queue.
func (s *Store) ResetRunningAnalyses(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET analysis_status = 'pending' WHERE analysis_status = 'running'`)
	return tag.RowsAffected(), err
}

// PostsToLabel returns the job's posts that have no label for the project yet
// (or one made with an older prompt version).
func (s *Store) PostsToLabel(ctx context.Context, jobID, projectID int64, promptVersion string) ([]Post, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.author_username, p.content, coalesce(p.context, '')
		 FROM job_posts jp JOIN posts p ON p.id = jp.post_id
		 LEFT JOIN post_labels pl ON pl.post_id = p.id AND pl.project_id = $2
		 WHERE jp.job_id = $1 AND (pl.post_id IS NULL OR pl.prompt_version <> $3)
		 ORDER BY jp.position`, jobID, projectID, promptVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.AuthorUsername, &p.Content, &p.Context); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SaveLabel(ctx context.Context, projectID int64, postID string, l PostLabel, model, promptVersion string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO post_labels (project_id, post_id, category, market, sentiment, aspects, language, reason,
			excluded, model, prompt_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (project_id, post_id) DO UPDATE SET
			category = EXCLUDED.category, market = EXCLUDED.market, sentiment = EXCLUDED.sentiment,
			aspects = EXCLUDED.aspects, language = EXCLUDED.language, reason = EXCLUDED.reason,
			excluded = EXCLUDED.excluded, model = EXCLUDED.model, prompt_version = EXCLUDED.prompt_version,
			created_at = now()`,
		projectID, postID, l.Category, l.Market, l.Sentiment, nonNil(l.Aspects), l.Language, l.Reason,
		l.Excluded, model, promptVersion)
	return err
}

// LabeledPost is a job post with its (optional) label, for exports.
type LabeledPost struct {
	SavedPost
	Label *PostLabel
	// Set for comments: the post they were collected under
	Parent *ParentRef
}

type ParentRef struct {
	URL            string
	AuthorUsername string
	Content        string
}

// JobLabeledPosts returns every post of a job with its label for the job's project.
func (s *Store) JobLabeledPosts(ctx context.Context, jobID int64) ([]LabeledPost, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.url, p.author_username, coalesce(p.author_name, ''), p.author_verified,
		        coalesce(p.author_url, ''), p.content, p.published_at, p.likes, p.replies, p.reposts, p.shares,
		        p.images, p.videos, p.links, coalesce(p.quoted_post_url, ''), jp.is_new,
		        pl.category, pl.market, pl.sentiment, pl.aspects, pl.language, pl.reason, pl.excluded
		 FROM job_posts jp
		 JOIN jobs j ON j.id = jp.job_id
		 JOIN posts p ON p.id = jp.post_id
		 LEFT JOIN post_labels pl ON pl.post_id = p.id AND pl.project_id = j.project_id
		 WHERE jp.job_id = $1
		 ORDER BY jp.position`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabeledPost{}
	for rows.Next() {
		var lp LabeledPost
		var cat, market, sentiment, lang, reason *string
		var aspects []string
		var excluded *bool
		p := &lp.SavedPost
		if err := rows.Scan(&p.ID, &p.URL, &p.AuthorUsername, &p.AuthorName, &p.AuthorVerified,
			&p.AuthorURL, &p.Content, &p.PublishedAt, &p.Likes, &p.Replies, &p.Reposts, &p.Shares,
			&p.Images, &p.Videos, &p.Links, &p.QuotedPostURL, &p.IsNew,
			&cat, &market, &sentiment, &aspects, &lang, &reason, &excluded); err != nil {
			return nil, err
		}
		if cat != nil {
			lp.Label = &PostLabel{Category: *cat, Market: *market, Sentiment: *sentiment, Aspects: aspects,
				Language: *lang, Reason: *reason, Excluded: *excluded}
		}
		out = append(out, lp)
	}
	return out, rows.Err()
}

// JobComments returns the comments collected under a run's posts, with labels for the run's project.
func (s *Store) JobComments(ctx context.Context, jobID int64) ([]LabeledPost, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id, c.url, c.author_username, coalesce(c.author_name, ''), c.author_verified,
		        coalesce(c.author_url, ''), c.content, c.published_at, c.likes, c.replies, c.reposts, c.shares,
		        c.images, c.videos, c.links, coalesce(c.quoted_post_url, ''),
		        cl.category, cl.market, cl.sentiment, cl.aspects, cl.language, cl.reason, cl.excluded,
		        p.url, p.author_username, p.content
		 FROM job_posts jp
		 JOIN jobs j ON j.id = jp.job_id
		 JOIN posts p ON p.id = jp.post_id
		 JOIN posts c ON c.parent_id = p.id
		 LEFT JOIN post_labels cl ON cl.post_id = c.id AND cl.project_id = j.project_id
		 WHERE jp.job_id = $1
		 ORDER BY jp.position, c.published_at NULLS LAST`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabeledPost{}
	for rows.Next() {
		var lp LabeledPost
		var cat, market, sentiment, lang, reason *string
		var aspects []string
		var excluded *bool
		parent := &ParentRef{}
		c := &lp.SavedPost
		if err := rows.Scan(&c.ID, &c.URL, &c.AuthorUsername, &c.AuthorName, &c.AuthorVerified,
			&c.AuthorURL, &c.Content, &c.PublishedAt, &c.Likes, &c.Replies, &c.Reposts, &c.Shares,
			&c.Images, &c.Videos, &c.Links, &c.QuotedPostURL,
			&cat, &market, &sentiment, &aspects, &lang, &reason, &excluded,
			&parent.URL, &parent.AuthorUsername, &parent.Content); err != nil {
			return nil, err
		}
		if cat != nil {
			lp.Label = &PostLabel{Category: *cat, Market: *market, Sentiment: *sentiment, Aspects: aspects,
				Language: *lang, Reason: *reason, Excluded: *excluded}
		}
		lp.Parent = parent
		out = append(out, lp)
	}
	return out, rows.Err()
}

// SetJobFiles replaces the export files of a finished run (e.g. after its comments arrived).
func (s *Store) SetJobFiles(ctx context.Context, jobID int64, fileKey, removedKey string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET file_key = NULLIF($2, ''), removed_file_key = NULLIF($3, '') WHERE id = $1`,
		jobID, fileKey, removedKey)
	return err
}

func (s *Store) FinishAnalysis(ctx context.Context, jobID int64, fileKey, removedKey string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET analysis_status = 'done', file_key = NULLIF($2, ''), removed_file_key = NULLIF($3, '')
		 WHERE id = $1 AND analysis_status = 'running'`, jobID, fileKey, removedKey)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrJobState
	}
	return err
}

func (s *Store) FailAnalysis(ctx context.Context, jobID int64, message string, fileKey string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET analysis_status = 'failed', analysis_error = $2, file_key = COALESCE(NULLIF($3, ''), file_key)
		 WHERE id = $1`, jobID, message, fileKey)
	return err
}

// Summary aggregates a project run's labels for the dashboard.
type Summary struct {
	Total     int                       `json:"total"`
	Analyzed  int                       `json:"analyzed"`
	Kept      int                       `json:"kept"`
	Sentiment map[string]int            `json:"sentiment"` // kept posts only
	Excluded  map[string]int            `json:"excluded"`  // reason -> count
	Aspects   map[string]map[string]int `json:"aspects"`   // aspect -> sentiment -> count (kept posts)
	Languages map[string]int            `json:"languages"`
	NewByMood map[string]int            `json:"newBySentiment"` // kept new posts by sentiment
	// Collected comments under this run's posts, by sentiment (kept only)
	Comments map[string]int `json:"comments"`
}

func (s *Store) JobSummary(ctx context.Context, jobID int64) (Summary, error) {
	sum := Summary{Sentiment: map[string]int{}, Excluded: map[string]int{}, Aspects: map[string]map[string]int{},
		Languages: map[string]int{}, NewByMood: map[string]int{}, Comments: map[string]int{}}
	rows, err := s.pool.Query(ctx,
		`SELECT cl.sentiment, count(*)::int
		 FROM job_posts jp JOIN jobs j ON j.id = jp.job_id
		 JOIN posts c ON c.parent_id = jp.post_id
		 JOIN post_labels cl ON cl.post_id = c.id AND cl.project_id = j.project_id
		 WHERE jp.job_id = $1 AND NOT cl.excluded
		 GROUP BY cl.sentiment`, jobID)
	if err != nil {
		return sum, err
	}
	for rows.Next() {
		var sentiment string
		var n int
		if err := rows.Scan(&sentiment, &n); err != nil {
			rows.Close()
			return sum, err
		}
		sum.Comments[sentiment] = n
	}
	rows.Close()
	posts, err := s.JobLabeledPosts(ctx, jobID)
	if err != nil {
		return sum, err
	}
	for _, p := range posts {
		sum.Total++
		l := p.Label
		if l == nil {
			continue
		}
		sum.Analyzed++
		sum.Languages[l.Language]++
		if l.Excluded {
			reason := l.Category
			if l.Market == "other_country" {
				reason = "other_country"
			}
			sum.Excluded[reason]++
			continue
		}
		sum.Kept++
		sum.Sentiment[l.Sentiment]++
		if p.IsNew {
			sum.NewByMood[l.Sentiment]++
		}
		for _, a := range l.Aspects {
			if sum.Aspects[a] == nil {
				sum.Aspects[a] = map[string]int{}
			}
			sum.Aspects[a][l.Sentiment]++
		}
	}
	return sum, nil
}
