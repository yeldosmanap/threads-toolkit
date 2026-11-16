package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

type jobResponse struct {
	ID            int64      `json:"id"`
	Keyword       string     `json:"keyword"`
	Filter        string     `json:"filter"`
	MaxItems      int        `json:"maxItems"`
	Status        string     `json:"status"`
	PostsSeen     int        `json:"postsSeen"`
	PostsCount    *int       `json:"postsCount"`
	NewPostsCount *int       `json:"newPostsCount"`
	Error         *string    `json:"error"`
	HasFile       bool       `json:"hasFile"`
	QueuePosition *int       `json:"queuePosition"`
	CreatedAt     time.Time  `json:"createdAt"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
}

func toJobResponse(j store.Job) jobResponse {
	return jobResponse{
		ID: j.ID, Keyword: j.Keyword, Filter: j.Filter, MaxItems: j.MaxItems, Status: j.Status,
		PostsSeen: j.PostsSeen, PostsCount: j.PostsCount, NewPostsCount: j.NewPostsCount, Error: j.Error,
		HasFile: j.FileKey != nil, QueuePosition: j.QueuePosition,
		CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
	}
}

type createJobRequest struct {
	Keyword  string `json:"keyword"`
	Filter   string `json:"filter"`
	MaxItems int    `json:"maxItems"`
}

// validateJob normalizes the request and returns a user-facing error message, or "".
func (s *Server) validateJob(req *createJobRequest) string {
	req.Keyword = strings.Join(strings.Fields(req.Keyword), " ")
	if req.Keyword == "" {
		return "Введите ключевое слово"
	}
	if len([]rune(req.Keyword)) > 100 {
		return "Ключевое слово слишком длинное (максимум 100 символов)"
	}
	for _, r := range req.Keyword {
		if unicode.IsControl(r) {
			return "Ключевое слово содержит недопустимые символы"
		}
	}
	if req.Filter == "" {
		req.Filter = "recent"
	}
	if req.Filter != "recent" && req.Filter != "top" {
		return "Сортировка должна быть recent или top"
	}
	if req.MaxItems == 0 {
		req.MaxItems = s.cfg.DefaultMaxItems
	}
	if req.MaxItems < 1 || req.MaxItems > s.cfg.MaxItemsLimit {
		return fmt.Sprintf("Количество постов должно быть от 1 до %d", s.cfg.MaxItemsLimit)
	}
	return ""
}

func (s *Server) handleLimits(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	active, err := s.store.CountActiveJobs(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	daily, err := s.store.CountJobsSince(r.Context(), u.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"maxItemsLimit":   s.cfg.MaxItemsLimit,
		"defaultMaxItems": s.cfg.DefaultMaxItems,
		"activeJobs":      active,
		"activeJobsLimit": s.cfg.QuotaActiveJobs,
		"dailyJobs":       daily,
		"dailyJobsLimit":  s.cfg.QuotaDailyJobs,
	})
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req createJobRequest
	if !decodeJSON(w, r, 4<<10, &req) {
		return
	}
	if msg := s.validateJob(&req); msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_job", msg)
		return
	}

	// Quotas: one Threads account serves every user, so each user gets a fair share.
	// (Check-then-insert can race by one job under concurrent requests - acceptable for a soft limit.)
	active, err := s.store.CountActiveJobs(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if active >= s.cfg.QuotaActiveJobs {
		writeError(w, http.StatusTooManyRequests, "quota_active",
			fmt.Sprintf("У вас уже %d поисков в очереди или в работе — дождитесь их завершения", active))
		return
	}
	daily, err := s.store.CountJobsSince(r.Context(), u.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if daily >= s.cfg.QuotaDailyJobs {
		writeError(w, http.StatusTooManyRequests, "quota_daily",
			fmt.Sprintf("Достигнут лимит: %d поисков за 24 часа", s.cfg.QuotaDailyJobs))
		return
	}

	job, err := s.store.CreateJob(r.Context(), u.ID, req.Keyword, req.Filter, req.MaxItems)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.log.Info("job created", "job", job.ID, "user", u.ID, "keyword", job.Keyword)
	// Re-read to include the queue position
	if full, err := s.store.GetJob(r.Context(), u.ID, job.ID); err == nil {
		job = full
	}
	writeJSON(w, http.StatusCreated, map[string]any{"job": toJobResponse(job)})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(r.Context(), userFrom(r.Context()).ID, 100)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]jobResponse, len(jobs))
	for i, j := range jobs {
		out[i] = toJobResponse(j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (s *Server) jobFromURL(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Поиск не найден")
		return store.Job{}, false
	}
	j, err := s.store.GetJob(r.Context(), userFrom(r.Context()).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Поиск не найден")
		return j, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return j, false
	}
	return j, true
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if j, ok := s.jobFromURL(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"job": toJobResponse(j)})
	}
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromURL(w, r)
	if !ok {
		return
	}
	j, err := s.store.CancelJob(r.Context(), userFrom(r.Context()).ID, j.ID)
	if errors.Is(err, store.ErrJobState) {
		writeError(w, http.StatusConflict, "not_cancelable", "Отменить можно только поиск, который ещё в очереди")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": toJobResponse(j)})
}

// handleDownload streams the xlsx from the private bucket after the ownership check in jobFromURL.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromURL(w, r)
	if !ok {
		return
	}
	if j.FileKey == nil {
		writeError(w, http.StatusNotFound, "no_file", "Файл ещё не готов")
		return
	}
	body, size, err := s.files.Get(r.Context(), *j.FileKey)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", xlsxContentType)
	w.Header().Set("Content-Disposition", contentDisposition(exportFilename(j)))
	w.Header().Set("Cache-Control", "private, no-store")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if _, err := io.Copy(w, body); err != nil {
		s.log.Warn("download interrupted", "job", j.ID, "err", err)
	}
}

const xlsxContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// contentDisposition supports non-ASCII (e.g. Cyrillic) file names via RFC 5987.
func contentDisposition(filename string) string {
	ascii := make([]rune, 0, len(filename))
	for _, r := range filename {
		if r < 128 && r != '"' && r != '\\' {
			ascii = append(ascii, r)
		} else {
			ascii = append(ascii, '_')
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, string(ascii), url.PathEscape(filename))
}

var unsafeFilename = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

// exportFilename: e.g. "threads-билайн-2026-11-17-42.xlsx"
func exportFilename(j store.Job) string {
	kw := strings.Trim(unsafeFilename.ReplaceAllString(j.Keyword, "_"), "_")
	if r := []rune(kw); len(r) > 40 {
		kw = string(r[:40])
	}
	return fmt.Sprintf("threads-%s-%s-%d.xlsx", kw, j.CreatedAt.UTC().Format("2006-01-02"), j.ID)
}
