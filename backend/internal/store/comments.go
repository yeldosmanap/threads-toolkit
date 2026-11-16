package store

import (
	"context"
	"fmt"
)

// PostsForComments returns URLs of a run's kept negative posts that have replies on Threads
// and whose comments were not collected yet - the most discussed first.
func (s *Store) PostsForComments(ctx context.Context, jobID, projectID int64, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.url FROM job_posts jp
		 JOIN posts p ON p.id = jp.post_id
		 JOIN post_labels pl ON pl.post_id = p.id AND pl.project_id = $2
		 WHERE jp.job_id = $1 AND pl.sentiment = 'negative' AND NOT pl.excluded AND p.replies > 0
		   AND p.parent_id IS NULL
		   AND NOT EXISTS (SELECT 1 FROM posts c WHERE c.parent_id = p.id)
		 ORDER BY p.replies DESC, jp.position
		 LIMIT $3`, jobID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	urls := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, rows.Err()
}

// CreateCommentsJob queues a system job collecting comments under the given posts of a run.
func (s *Store) CreateCommentsJob(ctx context.Context, parent Job, urls []string, perPost int) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx,
		`INSERT INTO jobs AS j (user_id, keyword, keyword_norm, filter, max_items, project_id, kind, parent_job_id, post_urls)
		 VALUES ($1, $2, $3, $4, $5, $6, 'comments', $7, $8)
		 RETURNING `+jobColumns,
		parent.UserID, "Комментарии: "+parent.Keyword, fmt.Sprintf("comments:%d", parent.ID),
		parent.Filter, perPost, parent.ProjectID, parent.ID, urls))
}

// ListComments returns the comments collected under a post, with labels for the project.
func (s *Store) ListComments(ctx context.Context, projectID *int64, postID string, limit int) ([]JobPost, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.url, p.author_username, coalesce(p.author_name, ''), p.author_verified,
		        coalesce(p.author_url, ''), p.content, p.published_at, p.likes, p.replies, p.reposts, p.shares,
		        p.images, p.videos, p.links, coalesce(p.quoted_post_url, ''), false,
		        pl.category, pl.market, pl.sentiment, pl.aspects, pl.language, pl.reason, pl.excluded
		 FROM posts p
		 LEFT JOIN post_labels pl ON pl.post_id = p.id AND pl.project_id = $1
		 WHERE p.parent_id = $2
		 ORDER BY p.published_at NULLS LAST
		 LIMIT $3`, projectID, postID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobPost{}
	for rows.Next() {
		var p JobPost
		var cat, market, sentiment, lang, reason *string
		var aspects []string
		var excluded *bool
		if err := rows.Scan(&p.ID, &p.URL, &p.AuthorUsername, &p.AuthorName, &p.AuthorVerified,
			&p.AuthorURL, &p.Content, &p.PublishedAt, &p.Likes, &p.Replies, &p.Reposts, &p.Shares,
			&p.Images, &p.Videos, &p.Links, &p.QuotedPostURL, &p.IsNew,
			&cat, &market, &sentiment, &aspects, &lang, &reason, &excluded); err != nil {
			return nil, err
		}
		if cat != nil {
			p.Label = &PostLabel{Category: *cat, Market: *market, Sentiment: *sentiment, Aspects: aspects,
				Language: *lang, Reason: *reason, Excluded: *excluded}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
