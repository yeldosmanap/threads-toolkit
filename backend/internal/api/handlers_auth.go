package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/auth"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"isAdmin"`
}

func toUserResponse(u store.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, IsAdmin: u.IsAdmin}
}

// validateCredentials returns a user-facing error message, or "" if valid.
func validateCredentials(c credentials) string {
	addr, err := mail.ParseAddress(c.Email)
	if err != nil || addr.Address != c.Email || len(c.Email) > 254 {
		return "Введите корректный email"
	}
	if n := len([]rune(c.Password)); n < 8 {
		return "Пароль должен быть не короче 8 символов"
	}
	// bcrypt only uses the first 72 bytes
	if len(c.Password) > 72 {
		return "Пароль слишком длинный (максимум 72 байта)"
	}
	return ""
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decodeJSON(w, r, 4<<10, &c) {
		return
	}
	c.Email = strings.TrimSpace(c.Email)
	if msg := validateCredentials(c); msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_credentials", msg)
		return
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, err := s.store.CreateUser(r.Context(), c.Email, hash)
	if errors.Is(err, store.ErrEmailTaken) {
		writeError(w, http.StatusConflict, "email_taken", "Этот email уже зарегистрирован")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !s.startSession(w, r, u) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": toUserResponse(u)})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decodeJSON(w, r, 4<<10, &c) {
		return
	}
	u, hash, err := s.store.GetUserByEmail(r.Context(), strings.TrimSpace(c.Email))
	if errors.Is(err, store.ErrNotFound) {
		auth.BurnPasswordCheck(c.Password)
		writeError(w, http.StatusUnauthorized, "bad_login", "Неверный email или пароль")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !auth.CheckPassword(hash, c.Password) {
		writeError(w, http.StatusUnauthorized, "bad_login", "Неверный email или пароль")
		return
	}
	if !s.startSession(w, r, u) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserResponse(u)})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) bool {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	if err := s.store.CreateSession(r.Context(), hash, u.ID, time.Now().Add(s.cfg.SessionTTL)); err != nil {
		s.internalError(w, r, err)
		return false
	}
	http.SetCookie(w, auth.SessionCookie(token, s.cfg.SessionTTL, s.cfg.CookieSecure))
	return true
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
		if err := s.store.DeleteSession(r.Context(), auth.HashToken(c.Value)); err != nil {
			s.log.Warn("delete session", "err", err)
		}
	}
	http.SetCookie(w, auth.ClearCookie(s.cfg.CookieSecure))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserResponse(userFrom(r.Context()))})
}
