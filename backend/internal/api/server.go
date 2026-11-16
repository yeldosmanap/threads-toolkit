// Package api wires HTTP routes: the public JSON API for the web app (/api/*)
// and the internal API for the scraper worker (/internal/*).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/auth"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/config"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/storage"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

type Server struct {
	cfg       config.Config
	store     *store.Store
	files     *storage.S3
	log       *slog.Logger
	authLimit *auth.IPLimiter
}

func New(cfg config.Config, st *store.Store, files *storage.S3, log *slog.Logger) *Server {
	return &Server{
		cfg:   cfg,
		store: st,
		files: files,
		log:   log,
		// Login/registration: 10 attempts per minute per IP
		authLimit: auth.NewIPLimiter(10, 10),
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// Backend is only reachable through the web proxy (nginx) which sets X-Real-IP
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.requestLog)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	r.Route("/api", func(r chi.Router) {
		r.Use(requireJSONForWrites)
		r.Use(middleware.Timeout(30 * time.Second))

		r.Group(func(r chi.Router) {
			r.Use(s.rateLimitAuth)
			r.Post("/auth/register", s.handleRegister)
			r.Post("/auth/login", s.handleLogin)
		})
		r.Post("/auth/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Get("/auth/me", s.handleMe)
			r.Get("/limits", s.handleLimits)
			r.Get("/jobs", s.handleListJobs)
			r.Post("/jobs", s.handleCreateJob)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Post("/jobs/{id}/cancel", s.handleCancelJob)
			r.Get("/jobs/{id}/download", s.handleDownload)
		})
	})

	r.Route("/internal", func(r chi.Router) {
		r.Use(s.requireWorker)
		r.Post("/jobs/claim", s.handleClaim)
		r.Post("/jobs/{id}/progress", s.handleProgress)
		r.Post("/jobs/{id}/complete", s.handleComplete)
		r.Post("/jobs/{id}/fail", s.handleFail)
	})

	return r
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

type ctxKey int

const userKey ctxKey = 1

func userFrom(ctx context.Context) store.User {
	u, _ := ctx.Value(userKey).(store.User)
	return u
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Требуется вход")
			return
		}
		u, err := s.store.GetSessionUser(r.Context(), auth.HashToken(c.Value))
		if errors.Is(err, store.ErrNotFound) {
			http.SetCookie(w, auth.ClearCookie(s.cfg.CookieSecure))
			writeError(w, http.StatusUnauthorized, "unauthorized", "Сессия истекла, войдите снова")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

func (s *Server) requireWorker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if len(h) <= len(prefix) || !auth.TokenEqual(h[len(prefix):], s.cfg.WorkerToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid worker token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimitAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		// Without X-Real-IP, RemoteAddr is "ip:port" - drop the port so every connection shares one bucket
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		if !s.authLimit.Allow(ip) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Слишком много попыток, подождите минуту")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireJSONForWrites: HTML forms cannot send application/json cross-site without a
// CORS preflight, so together with SameSite=Lax cookies this closes CSRF.
func requireJSONForWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		// Worker polls /claim every few seconds - keep the log readable
		if r.URL.Path == "/internal/jobs/claim" && ww.Status() == http.StatusNoContent {
			return
		}
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"ms", time.Since(start).Milliseconds())
	})
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: message}})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("internal error", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Внутренняя ошибка сервера")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "Некорректный JSON: "+err.Error())
		return false
	}
	return true
}
