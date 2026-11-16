package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/export"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

// Worker contract (src/worker.ts):
//   POST /internal/jobs/claim           -> 200 {job} | 204 nothing to do
//   POST /internal/jobs/{id}/progress   {postsSeen}
//   POST /internal/jobs/{id}/complete   {posts: ThreadsPost[]}
//   POST /internal/jobs/{id}/fail       {error}

type claimedJob struct {
	ID              int64  `json:"id"`
	Keyword         string `json:"keyword"`
	Filter          string `json:"filter"`
	MaxItems        int    `json:"maxItems"`
	ScrollDelay     int    `json:"scrollDelay"`
	MaxEmptyScrolls int    `json:"maxEmptyScrolls"`
	MaxScrollTime   int    `json:"maxScrollTime"`
}

// scrollBudgetSecs: logged-in Threads yields ~6 posts per ~14 s scroll step (~2.5 s/post).
func scrollBudgetSecs(maxItems int) int {
	return min(1800, max(60, maxItems*5/2+60))
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	workerID := r.Header.Get("X-Worker-Id")
	j, err := s.store.ClaimJob(r.Context(), workerID)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.log.Info("job claimed", "job", j.ID, "worker", workerID, "attempt", j.Attempts)
	writeJSON(w, http.StatusOK, map[string]claimedJob{"job": {
		ID: j.ID, Keyword: j.Keyword, Filter: j.Filter, MaxItems: j.MaxItems,
		ScrollDelay: 3000, MaxEmptyScrolls: 6, MaxScrollTime: scrollBudgetSecs(j.MaxItems),
	}})
}

func internalJobID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", "invalid job id")
		return 0, false
	}
	return id, true
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := internalJobID(w, r)
	if !ok {
		return
	}
	var body struct {
		PostsSeen int `json:"postsSeen"`
	}
	if !decodeJSON(w, r, 1<<10, &body) {
		return
	}
	err := s.store.Heartbeat(r.Context(), id, body.PostsSeen)
	if errors.Is(err, store.ErrJobState) {
		writeError(w, http.StatusConflict, "not_running", "job is not running")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scrapedPost mirrors ThreadsPost from src/types.ts.
type scrapedPost struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Content string `json:"content"`
	// ISO-8601 string; may be empty or unparsable for some posts
	Timestamp string `json:"timestamp"`
	Author    struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		ProfileURL  string `json:"profileUrl"`
		IsVerified  bool   `json:"isVerified"`
	} `json:"author"`
	Stats struct {
		Likes   int `json:"likes"`
		Replies int `json:"replies"`
		Reposts int `json:"reposts"`
		Shares  int `json:"shares"`
	} `json:"stats"`
	Images     []string `json:"images"`
	Videos     []string `json:"videos"`
	Links      []string `json:"links"`
	QuotedPost *struct {
		URL string `json:"url"`
	} `json:"quotedPost"`
}

func (p scrapedPost) toPost() store.Post {
	out := store.Post{
		ID: p.ID, URL: p.URL, Content: p.Content,
		AuthorUsername: p.Author.Username, AuthorName: p.Author.DisplayName,
		AuthorURL: p.Author.ProfileURL, AuthorVerified: p.Author.IsVerified,
		Likes: p.Stats.Likes, Replies: p.Stats.Replies, Reposts: p.Stats.Reposts, Shares: p.Stats.Shares,
		Images: p.Images, Videos: p.Videos, Links: p.Links,
	}
	if t, err := time.Parse(time.RFC3339, p.Timestamp); err == nil {
		out.PublishedAt = &t
	}
	if p.QuotedPost != nil {
		out.QuotedPostURL = p.QuotedPost.URL
	}
	return out
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	id, ok := internalJobID(w, r)
	if !ok {
		return
	}
	var body struct {
		Posts []scrapedPost `json:"posts"`
	}
	if !decodeJSON(w, r, 64<<20, &body) {
		return
	}

	// Drop invalid posts and duplicates (the same post can appear twice in a feed)
	posts := make([]store.Post, 0, len(body.Posts))
	seen := make(map[string]bool, len(body.Posts))
	for _, sp := range body.Posts {
		if sp.ID == "" || sp.URL == "" || sp.Author.Username == "" || seen[sp.ID] {
			continue
		}
		if !strings.HasPrefix(sp.URL, "https://www.threads.com/") && !strings.HasPrefix(sp.URL, "https://www.threads.net/") {
			continue
		}
		seen[sp.ID] = true
		posts = append(posts, sp.toPost())
	}

	saved, err := s.store.SaveResults(r.Context(), id, posts)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrJobState) {
		writeError(w, http.StatusConflict, "not_running", "job is not running")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	newCount := 0
	for _, p := range saved {
		if p.IsNew {
			newCount++
		}
	}

	job, err := s.store.GetJobByID(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	file, err := export.BuildXLSX(saved, job.IsFirstRun)
	if err == nil {
		// ASCII-only object key: some S3 implementations sign non-ASCII paths differently.
		// The human-readable (possibly Cyrillic) name is applied at download via Content-Disposition.
		key := fmt.Sprintf("users/%d/jobs/%d/posts.xlsx", job.UserID, job.ID)
		if err = s.files.Put(r.Context(), key,
			xlsxContentType, file); err == nil {
			err = s.store.MarkDone(r.Context(), id, key, len(saved), newCount)
		}
	}
	if err != nil {
		s.log.Error("finishing job failed", "job", id, "err", err)
		_ = s.store.MarkFailed(r.Context(), id, "Не удалось сохранить файл с результатами")
		s.internalError(w, r, err)
		return
	}

	s.log.Info("job done", "job", id, "posts", len(saved), "new", newCount)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFail(w http.ResponseWriter, r *http.Request) {
	id, ok := internalJobID(w, r)
	if !ok {
		return
	}
	var body struct {
		Error string `json:"error"`
	}
	if !decodeJSON(w, r, 16<<10, &body) {
		return
	}
	msg := strings.TrimSpace(body.Error)
	if msg == "" {
		msg = "Неизвестная ошибка"
	}
	err := s.store.MarkFailed(r.Context(), id, msg)
	if errors.Is(err, store.ErrJobState) {
		writeError(w, http.StatusConflict, "not_running", "job is not running")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.log.Warn("job failed", "job", id, "error", msg)
	w.WriteHeader(http.StatusNoContent)
}
