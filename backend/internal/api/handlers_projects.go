package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

const (
	maxAliases     = 10
	maxOfficial    = 10
	maxCompetitors = 20
	maxNotesRunes  = 2000
)

var threadsUsername = regexp.MustCompile(`^[A-Za-z0-9._]{1,30}$`)

type projectRequest struct {
	Name             string   `json:"name"`
	Market           string   `json:"market"`
	Aliases          []string `json:"aliases"`
	OfficialAccounts []string `json:"officialAccounts"`
	Competitors      []string `json:"competitors"`
	Notes            string   `json:"notes"`
}

type projectResponse struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Market           string    `json:"market"`
	Aliases          []string  `json:"aliases"`
	OfficialAccounts []string  `json:"officialAccounts"`
	Competitors      []string  `json:"competitors"`
	Notes            string    `json:"notes"`
	LastJobID        *int64    `json:"lastJobId"`
	LastJobStatus    *string   `json:"lastJobStatus"`
	RunsCount        int       `json:"runsCount"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func toProjectResponse(p store.Project) projectResponse {
	return projectResponse{
		ID: p.ID, Name: p.Name, Market: p.Market, Aliases: p.Aliases, OfficialAccounts: p.OfficialAccounts,
		Competitors: p.Competitors, Notes: p.Notes, LastJobID: p.LastJobID, LastJobStatus: p.LastJobStatus, RunsCount: p.RunsCount,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// cleanList trims, drops empties and case-insensitive duplicates.
func cleanList(in []string, transform func(string) string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.Join(strings.Fields(v), " ")
		if transform != nil {
			v = transform(v)
		}
		key := strings.ToLower(v)
		if v == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' {
			return true
		}
	}
	return false
}

// projectInput validates and normalizes the request; returns a user-facing error message or "".
func projectInput(req projectRequest) (store.ProjectInput, string) {
	in := store.ProjectInput{
		Name:             strings.Join(strings.Fields(req.Name), " "),
		Market:           strings.Join(strings.Fields(req.Market), " "),
		Aliases:          cleanList(req.Aliases, nil),
		OfficialAccounts: cleanList(req.OfficialAccounts, func(s string) string { return strings.TrimPrefix(s, "@") }),
		Competitors:      cleanList(req.Competitors, nil),
		Notes:            strings.TrimSpace(req.Notes),
	}
	if in.Market == "" {
		in.Market = "Казахстан"
	}
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 100:
		return in, "Название проекта: от 1 до 100 символов"
	case len([]rune(in.Market)) > 60:
		return in, "Страна: не больше 60 символов"
	case len(in.Aliases) == 0:
		return in, "Добавьте хотя бы одно упоминание (например, название компании)"
	case len(in.Aliases) > maxAliases:
		return in, fmt.Sprintf("Не больше %d упоминаний: каждое — отдельный поиск в Threads", maxAliases)
	case len(in.OfficialAccounts) > maxOfficial:
		return in, fmt.Sprintf("Не больше %d официальных аккаунтов", maxOfficial)
	case len(in.Competitors) > maxCompetitors:
		return in, fmt.Sprintf("Не больше %d конкурентов", maxCompetitors)
	case len([]rune(in.Notes)) > maxNotesRunes:
		return in, fmt.Sprintf("Заметки: не больше %d символов", maxNotesRunes)
	case hasControl(in.Notes):
		return in, "Заметки содержат недопустимые символы"
	}
	for _, a := range in.Aliases {
		if len([]rune(a)) > 100 || hasControl(a) {
			return in, "Каждое упоминание — не длиннее 100 символов, без спецсимволов"
		}
	}
	for _, a := range in.OfficialAccounts {
		if !threadsUsername.MatchString(a) {
			return in, fmt.Sprintf("Некорректный аккаунт @%s: только латиница, цифры, точка и подчёркивание", a)
		}
	}
	for _, c := range in.Competitors {
		if len([]rune(c)) > 60 || hasControl(c) {
			return in, "Название конкурента — не длиннее 60 символов"
		}
	}
	return in, ""
}

func (s *Server) projectFromURL(w http.ResponseWriter, r *http.Request) (store.Project, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Проект не найден")
		return store.Project{}, false
	}
	p, err := s.store.GetProject(r.Context(), userFrom(r.Context()).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Проект не найден")
		return p, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return p, false
	}
	return p, true
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListProjects(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]projectResponse, len(list))
	for i, p := range list {
		out[i] = toProjectResponse(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out, "analysisEnabled": s.analysisEnabled})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.projectFromURL(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"project": toProjectResponse(p)})
	}
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req projectRequest
	if !decodeJSON(w, r, 16<<10, &req) {
		return
	}
	in, msg := projectInput(req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_project", msg)
		return
	}
	n, err := s.store.CountProjects(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if n >= s.cfg.QuotaProjects {
		writeError(w, http.StatusTooManyRequests, "quota_projects", fmt.Sprintf("Можно создать не больше %d проектов", s.cfg.QuotaProjects))
		return
	}
	p, err := s.store.CreateProject(r.Context(), u.ID, in)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.log.Info("project created", "project", p.ID, "user", u.ID, "aliases", len(p.Aliases))
	writeJSON(w, http.StatusCreated, map[string]any{"project": toProjectResponse(p)})
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.projectFromURL(w, r)
	if !ok {
		return
	}
	var req projectRequest
	if !decodeJSON(w, r, 16<<10, &req) {
		return
	}
	in, msg := projectInput(req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_project", msg)
		return
	}
	p, err := s.store.UpdateProject(r.Context(), cur.UserID, cur.ID, in)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": toProjectResponse(p)})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectFromURL(w, r)
	if !ok {
		return
	}
	// ?withRuns=1 also deletes the project's searches (running ones are stopped)
	if r.URL.Query().Get("withRuns") == "1" {
		ids, err := s.store.ProjectJobIDs(r.Context(), p.UserID, p.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		for _, id := range ids {
			if err := s.deleteJob(r, p.UserID, id); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.internalError(w, r, err)
				return
			}
		}
	}
	if err := s.store.DeleteProject(r.Context(), p.UserID, p.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type runProjectRequest struct {
	Filter   string `json:"filter"`
	MaxItems int    `json:"maxItems"`
}

// handleRunProject queues a run: every alias is searched, then the posts are analyzed.
func (s *Server) handleRunProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectFromURL(w, r)
	if !ok {
		return
	}
	var req runProjectRequest
	if !decodeJSON(w, r, 4<<10, &req) {
		return
	}
	// Reuse the one-off search validation for filter / maxItems (maxItems is per alias)
	check := createJobRequest{Keyword: p.Name, Filter: req.Filter, MaxItems: req.MaxItems}
	if msg := s.validateJob(&check); msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_job", msg)
		return
	}
	if msg, status := s.checkJobQuotas(r, p.UserID); msg != "" {
		writeError(w, status, "quota", msg)
		return
	}
	job, err := s.store.CreateProjectJob(r.Context(), p, check.Filter, check.MaxItems, nil)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.log.Info("project run queued", "project", p.ID, "job", job.ID, "aliases", len(p.Aliases))
	if full, err := s.store.GetJob(r.Context(), p.UserID, job.ID); err == nil {
		job = full
	}
	writeJSON(w, http.StatusCreated, map[string]any{"job": toJobResponse(job)})
}
