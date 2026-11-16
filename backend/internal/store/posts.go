package store

import (
	"context"
	"fmt"
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
	// Label filters (project runs): "" = any
	Sentiment string
	Aspect    string
	// Exact post (used to check that a post belongs to a job)
	PostID string
	// "kept" (default for analyzed runs) | "removed" | "all"
	View string
	// Case-insensitive substring match on text, author username and author name
	Search string
	Sort   string // key of postSorts; default "newest"
	Limit  int
	Offset int
}

// JobPost is a post as found by a particular job, with its label for the job's project (if any).
type JobPost struct {
	Post
	IsNew bool
	Label *PostLabel
	// Collected comments under the post, by sentiment (kept comments only); "total" includes unlabeled
	Comments map[string]int
}

// escapeLike makes user input literal inside a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

const jobPostsWhere = `
	FROM job_posts jp
	JOIN jobs j ON j.id = jp.job_id
	JOIN posts p ON p.id = jp.post_id
	LEFT JOIN post_labels pl ON pl.post_id = p.id AND pl.project_id = j.project_id
	WHERE jp.job_id = $1
	  AND (NOT $2 OR jp.is_new)
	  AND ($3 = '' OR p.content ILIKE $3 OR p.author_username ILIKE $3 OR coalesce(p.author_name, '') ILIKE $3)
	  AND ($6 = '' OR pl.sentiment = $6)
	  AND ($7 = '' OR $7 = ANY(pl.aspects))
	  AND ($9 = '' OR p.id = $9)
	  AND ($8 = 'all' OR ($8 = 'kept' AND NOT coalesce(pl.excluded, false)) OR ($8 = 'removed' AND coalesce(pl.excluded, false)))`

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

	view := q.View
	if view != "kept" && view != "removed" {
		view = "all"
	}
	// $4/$5 are LIMIT/OFFSET in the page query; the count query passes them too so the numbering matches
	args := []any{jobID, q.OnlyNew, pattern, q.Limit, q.Offset, q.Sentiment, q.Aspect, view, q.PostID}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+jobPostsWhere+` AND $4::int >= 0 AND $5::int >= 0`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.url, p.author_username, coalesce(p.author_name, ''), p.author_verified,
		        coalesce(p.author_url, ''), p.content, p.published_at, p.likes, p.replies, p.reposts, p.shares,
		        p.images, p.videos, p.links, coalesce(p.quoted_post_url, ''), jp.is_new,
		        pl.category, pl.market, pl.sentiment, pl.aspects, pl.language, pl.reason, pl.excluded,
		        (SELECT count(*) FROM posts c WHERE c.parent_id = p.id)::int,
		        (SELECT count(*) FILTER (WHERE cl.sentiment = 'negative')::int || ',' ||
		                count(*) FILTER (WHERE cl.sentiment = 'neutral')::int || ',' ||
		                count(*) FILTER (WHERE cl.sentiment = 'positive')::int
		         FROM posts c JOIN post_labels cl ON cl.post_id = c.id AND cl.project_id = j.project_id
		         WHERE c.parent_id = p.id AND NOT cl.excluded)
		`+jobPostsWhere+`
		 ORDER BY `+order+`, jp.position
		 LIMIT $4 OFFSET $5`,
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	posts := []JobPost{}
	for rows.Next() {
		var p JobPost
		var cat, market, sentiment, lang, reason *string
		var aspects []string
		var excluded *bool
		var commentsTotal int
		var commentsBySentiment *string
		if err := rows.Scan(&p.ID, &p.URL, &p.AuthorUsername, &p.AuthorName, &p.AuthorVerified,
			&p.AuthorURL, &p.Content, &p.PublishedAt, &p.Likes, &p.Replies, &p.Reposts, &p.Shares,
			&p.Images, &p.Videos, &p.Links, &p.QuotedPostURL, &p.IsNew,
			&cat, &market, &sentiment, &aspects, &lang, &reason, &excluded,
			&commentsTotal, &commentsBySentiment); err != nil {
			return nil, 0, err
		}
		if commentsTotal > 0 {
			p.Comments = map[string]int{"total": commentsTotal}
			if commentsBySentiment != nil {
				var neg, neu, pos int
				if _, err := fmt.Sscanf(*commentsBySentiment, "%d,%d,%d", &neg, &neu, &pos); err == nil {
					p.Comments["negative"], p.Comments["neutral"], p.Comments["positive"] = neg, neu, pos
				}
			}
		}
		if cat != nil {
			p.Label = &PostLabel{Category: *cat, Market: *market, Sentiment: *sentiment, Aspects: aspects,
				Language: *lang, Reason: *reason, Excluded: *excluded}
		}
		posts = append(posts, p)
	}
	return posts, total, rows.Err()
}
