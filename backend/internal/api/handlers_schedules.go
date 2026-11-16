package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/schedule"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

type scheduleRequest struct {
	// Run a whole project instead of a keyword (keyword is then ignored)
	ProjectID  *int64 `json:"projectId"`
	Keyword    string `json:"keyword"`
	Filter     string `json:"filter"`
	MaxItems   int    `json:"maxItems"`
	Kind       string `json:"kind"`
	EveryHours int    `json:"everyHours"`
	DailyTime  string `json:"dailyTime"`
	Timezone   string `json:"timezone"`
	Enabled    *bool  `json:"enabled"`
}

type scheduleResponse struct {
	ID            int64      `json:"id"`
	Keyword       string     `json:"keyword"`
	Filter        string     `json:"filter"`
	MaxItems      int        `json:"maxItems"`
	Kind          string     `json:"kind"`
	EveryHours    int        `json:"everyHours"`
	DailyTime     string     `json:"dailyTime"`
	Timezone      string     `json:"timezone"`
	Enabled       bool       `json:"enabled"`
	NextRunAt     time.Time  `json:"nextRunAt"`
	LastRunAt     *time.Time `json:"lastRunAt"`
	LastError     *string    `json:"lastError"`
	LastJobID     *int64     `json:"lastJobId"`
	LastJobStatus *string    `json:"lastJobStatus"`
	CreatedAt     time.Time  `json:"createdAt"`
	ProjectID     *int64     `json:"projectId"`
	ProjectName   *string    `json:"projectName"`
	Aliases       []string   `json:"aliases"`
}

func toScheduleResponse(sc store.Schedule) scheduleResponse {
	return scheduleResponse{
		ID: sc.ID, Keyword: sc.Keyword, Filter: sc.Filter, MaxItems: sc.MaxItems,
		Kind: sc.Spec.Kind, EveryHours: sc.Spec.EveryHours, DailyTime: sc.Spec.DailyTime, Timezone: sc.Spec.Timezone,
		Enabled: sc.Enabled, NextRunAt: sc.NextRunAt, LastRunAt: sc.LastRunAt, LastError: sc.LastError,
		LastJobID: sc.LastJobID, LastJobStatus: sc.LastJobStatus, CreatedAt: sc.CreatedAt,
		ProjectID: sc.ProjectID, ProjectName: sc.ProjectName, Aliases: sc.ProjectAliases,
	}
}

// toInput validates the request (same keyword/filter/maxItems rules as a one-off search).
// Returns a user-facing error message, or "".
func (s *Server) scheduleInput(req scheduleRequest) (store.ScheduleInput, string) {
	job := createJobRequest{Keyword: req.Keyword, Filter: req.Filter, MaxItems: req.MaxItems}
	if msg := s.validateJob(&job); msg != "" {
		return store.ScheduleInput{}, msg
	}
	spec := schedule.Spec{Kind: req.Kind, EveryHours: req.EveryHours, DailyTime: req.DailyTime, Timezone: req.Timezone}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	// Keep only the fields that matter for the kind, so equal schedules compare equal
	if spec.Kind == schedule.KindEveryHours {
		spec.DailyTime = ""
	} else {
		spec.EveryHours = 0
	}
	if err := spec.Validate(s.cfg.MinScheduleHours); err != nil {
		return store.ScheduleInput{}, "Расписание: " + err.Error()
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return store.ScheduleInput{ProjectID: req.ProjectID, Keyword: job.Keyword, Filter: job.Filter, MaxItems: job.MaxItems, Spec: spec, Enabled: enabled}, ""
}

// resolveScheduleProject checks that a project schedule points to the user's own project
// and uses the project name as the schedule's display keyword. Returns false after writing an error.
func (s *Server) resolveScheduleProject(w http.ResponseWriter, r *http.Request, req *scheduleRequest) bool {
	if req.ProjectID == nil {
		return true
	}
	p, err := s.store.GetProject(r.Context(), userFrom(r.Context()).ID, *req.ProjectID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusBadRequest, "invalid_schedule", "Проект не найден")
		return false
	}
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	req.Keyword = p.Name
	return true
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSchedules(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]scheduleResponse, len(list))
	for i, sc := range list {
		out[i] = toScheduleResponse(sc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req scheduleRequest
	if !decodeJSON(w, r, 4<<10, &req) {
		return
	}
	if !s.resolveScheduleProject(w, r, &req) {
		return
	}
	in, msg := s.scheduleInput(req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_schedule", msg)
		return
	}
	n, err := s.store.CountSchedules(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if n >= s.cfg.QuotaSchedules {
		writeError(w, http.StatusTooManyRequests, "quota_schedules",
			fmt.Sprintf("Можно создать не больше %d регулярных поисков", s.cfg.QuotaSchedules))
		return
	}
	sc, err := s.store.CreateSchedule(r.Context(), u.ID, in, time.Now())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.log.Info("schedule created", "schedule", sc.ID, "user", u.ID, "keyword", sc.Keyword, "kind", sc.Spec.Kind)
	writeJSON(w, http.StatusCreated, map[string]any{"schedule": toScheduleResponse(sc)})
}

func scheduleIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Расписание не найдено")
		return 0, false
	}
	return id, true
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := scheduleIDFromURL(w, r)
	if !ok {
		return
	}
	var req scheduleRequest
	if !decodeJSON(w, r, 4<<10, &req) {
		return
	}
	if !s.resolveScheduleProject(w, r, &req) {
		return
	}
	in, msg := s.scheduleInput(req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_schedule", msg)
		return
	}
	sc, err := s.store.UpdateSchedule(r.Context(), userFrom(r.Context()).ID, id, in, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Расписание не найдено")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedule": toScheduleResponse(sc)})
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := scheduleIDFromURL(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteSchedule(r.Context(), userFrom(r.Context()).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Расписание не найдено")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
