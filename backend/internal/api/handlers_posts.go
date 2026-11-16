package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/analysis"
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
	// Classifier label for the job's project; nil for plain searches or not yet analyzed
	Analysis *postAnalysis `json:"analysis"`
	// Collected comments: {"total": n, "negative": .., "neutral": .., "positive": ..}; nil if none
	Comments map[string]int `json:"comments"`
}

type postAnalysis struct {
	Sentiment string   `json:"sentiment"`
	Category  string   `json:"category"`
	Market    string   `json:"market"`
	Aspects   []string `json:"aspects"`
	Language  string   `json:"language"`
	Reason    string   `json:"reason"`
	Excluded  bool     `json:"excluded"`
}

func toPostResponse(p store.JobPost) postResponse {
	links := p.Links
	if links == nil {
		links = []string{}
	}
	var a *postAnalysis
	if l := p.Label; l != nil {
		aspects := l.Aspects
		if aspects == nil {
			aspects = []string{}
		}
		a = &postAnalysis{Sentiment: l.Sentiment, Category: l.Category, Market: l.Market, Aspects: aspects,
			Language: l.Language, Reason: l.Reason, Excluded: l.Excluded}
	}
	return postResponse{
		Comments: p.Comments,
		Analysis: a,
		ID:       p.ID, URL: p.URL,
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
	if s := v.Get("sentiment"); s != "" {
		if s != "negative" && s != "neutral" && s != "positive" {
			return q, "Неизвестная тональность"
		}
		q.Sentiment = s
	}
	if s := v.Get("aspect"); s != "" {
		if !slices.Contains(analysis.Aspects, s) {
			return q, "Неизвестный аспект"
		}
		q.Aspect = s
	}
	q.View = "all"
	if s := v.Get("view"); s != "" {
		if s != "kept" && s != "removed" && s != "all" {
			return q, "view должен быть kept, removed или all"
		}
		q.View = s
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

// handleListComments returns the comments collected under one of the job's posts.
func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromURL(w, r)
	if !ok {
		return
	}
	postID := chi.URLParam(r, "postId")
	// Only posts that belong to this job (ownership checked via the job)
	posts, _, err := s.store.ListJobPosts(r.Context(), j.ID, store.PostQuery{Search: "", View: "all", Limit: 1, PostID: postID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(posts) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Пост не найден")
		return
	}
	comments, err := s.store.ListComments(r.Context(), j.ProjectID, postID, 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]postResponse, len(comments))
	for i, c := range comments {
		out[i] = toPostResponse(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": out})
}
