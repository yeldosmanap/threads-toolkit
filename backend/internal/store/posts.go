package store

import (
	"context"
	"strings"
)

// Sort options for ListJobPosts. Keys are what the API accepts; values are trusted SQL.
var postSorts = map[string]string{
	"newest":  "p.published_at DESC NULLS LAST",
	"oldest":  "p.published_at ASC NULLS LAST",
	"likes":   "p.likes DESC",
	"replies": "p.replies DESC",
	"reposts": "p.reposts DESC",
}

// ValidPostSort reports whether sort is a supported ListJobPosts order.
func ValidPostSort(sort string) bool {
	_, ok := postSorts[sort]
	return ok
}

type PostQuery struct {
	OnlyNew bool
	// Case-insensitive substring match on text, author username and author name
	Search string
	Sort   string // key of postSorts; default "newest"
	Limit  int
	Offset int
}

// JobPost is a post as found by a particular job.
type JobPost struct {
	Post
	IsNew bool
}

// escapeLike makes user input literal inside a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

const jobPostsWhere = `
	FROM job_posts jp JOIN posts p ON p.id = jp.post_id
	WHERE jp.job_id = $1
	  AND (NOT $2 OR jp.is_new)
	  AND ($3 = '' OR p.content ILIKE $3 OR p.author_username ILIKE $3 OR coalesce(p.author_name, '') ILIKE $3)`

// ListJobPosts returns one page of a job's posts and the total number matching the filters.
// Ownership of the job must be checked by the caller.
func (s *Store) ListJobPosts(ctx context.Context, jobID int64, q PostQuery) ([]JobPost, int, error) {
	order, ok := postSorts[q.Sort]
	if !ok {
		order = postSorts["newest"]
	}
	pattern := ""
	if q.Search != "" {
		pattern = "%" + escapeLike(q.Search) + "%"
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+jobPostsWhere, jobID, q.OnlyNew, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.url, p.author_username, coalesce(p.author_name, ''), p.author_verified,
		        coalesce(p.author_url, ''), p.content, p.published_at, p.likes, p.replies, p.reposts, p.shares,
		        p.images, p.videos, p.links, coalesce(p.quoted_post_url, ''), jp.is_new
		`+jobPostsWhere+`
		 ORDER BY `+order+`, jp.position
		 LIMIT $4 OFFSET $5`,
		jobID, q.OnlyNew, pattern, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	posts := []JobPost{}
	for rows.Next() {
		var p JobPost
		if err := rows.Scan(&p.ID, &p.URL, &p.AuthorUsername, &p.AuthorName, &p.AuthorVerified,
			&p.AuthorURL, &p.Content, &p.PublishedAt, &p.Likes, &p.Replies, &p.Reposts, &p.Shares,
			&p.Images, &p.Videos, &p.Links, &p.QuotedPostURL, &p.IsNew); err != nil {
			return nil, 0, err
		}
		posts = append(posts, p)
	}
	return posts, total, rows.Err()
}
