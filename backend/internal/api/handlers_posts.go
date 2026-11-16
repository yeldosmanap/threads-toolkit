package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

type postAuthor struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
	URL      string `json:"url"`
}

type postResponse struct {
	ID            string     `json:"id"`
	URL           string     `json:"url"`
	Author        postAuthor `json:"author"`
	Content       string     `json:"content"`
	PublishedAt   *time.Time `json:"publishedAt"`
	Likes         int        `json:"likes"`
	Replies       int        `json:"replies"`
	Reposts       int        `json:"reposts"`
	Shares        int        `json:"shares"`
	ImagesCount   int        `json:"imagesCount"`
	VideosCount   int        `json:"videosCount"`
	Links         []string   `json:"links"`
	QuotedPostURL string     `json:"quotedPostUrl"`
	IsNew         bool       `json:"isNew"`
}

func toPostResponse(p store.JobPost) postResponse {
	links := p.Links
	if links == nil {
		links = []string{}
	}
	return postResponse{
		ID: p.ID, URL: p.URL,
		Author:  postAuthor{Username: p.AuthorUsername, Name: p.AuthorName, Verified: p.AuthorVerified, URL: p.AuthorURL},
		Content: p.Content, PublishedAt: p.PublishedAt,
		Likes: p.Likes, Replies: p.Replies, Reposts: p.Reposts, Shares: p.Shares,
		ImagesCount: len(p.Images), VideosCount: len(p.Videos), Links: links,
		QuotedPostURL: p.QuotedPostURL, IsNew: p.IsNew,
	}
}

type postsQuery struct {
	store.PostQuery
	Page     int
	PageSize int
}

// parsePostsQuery reads ?onlyNew=1&q=...&sort=likes&page=2&pageSize=50.
// Returns a user-facing error message, or "".
func parsePostsQuery(v url.Values) (postsQuery, string) {
	q := postsQuery{Page: 1, PageSize: defaultPageSize}
	q.Sort = "newest"

	if s := v.Get("onlyNew"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return q, "Параметр onlyNew должен быть true или false"
		}
		q.OnlyNew = b
	}
	q.Search = strings.Join(strings.Fields(v.Get("q")), " ")
	if len([]rune(q.Search)) > 100 {
		return q, "Строка поиска слишком длинная (максимум 100 символов)"
	}
	if s := v.Get("sort"); s != "" {
		if !store.ValidPostSort(s) {
			return q, "Неизвестная сортировка"
		}
		q.Sort = s
	}
	if s := v.Get("page"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100000 {
			return q, "Некорректный номер страницы"
		}
		q.Page = n
	}
	if s := v.Get("pageSize"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxPageSize {
			return q, "pageSize должен быть от 1 до 100"
		}
		q.PageSize = n
	}
	q.Limit = q.PageSize
	q.Offset = (q.Page - 1) * q.PageSize
	return q, ""
}

func (s *Server) handleListJobPosts(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromURL(w, r)
	if !ok {
		return
	}
	q, msg := parsePostsQuery(r.URL.Query())
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_query", msg)
		return
	}
	posts, total, err := s.store.ListJobPosts(r.Context(), j.ID, q.PostQuery)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]postResponse, len(posts))
	for i, p := range posts {
		out[i] = toPostResponse(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"posts":    out,
		"total":    total,
		"page":     q.Page,
		"pageSize": q.PageSize,
	})
}
