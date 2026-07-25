package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"adp/internal/domain/model"
)

type contextKey string

const userContextKey contextKey = "user"

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string     `json:"token"`
	User  model.User `json:"user"`
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type changePasswordRequest struct {
	NewPassword string `json:"new_password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	token, user, err := s.authService.Login(req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{Token: token, User: user})
}

func (s *Server) withUserAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, errors.New("missing bearer token"))
			return
		}

		user, err := s.authService.ParseToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err)
			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}
}

// withAdminAuth restricts configuration-changing control-plane endpoints to
// administrators. Authentication alone is insufficient because managed
// configuration can alter executable templates and policy.
func (s *Server) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.withUserAuth(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).Role != "admin" {
			writeError(w, http.StatusForbidden, errors.New("admin role required"))
			return
		}
		next(w, r)
	})
}

func currentUser(r *http.Request) model.User {
	user, _ := r.Context().Value(userContextKey).(model.User)
	return user
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Role != "admin" {
		writeError(w, http.StatusForbidden, errors.New("admin role required"))
		return
	}

	writeJSON(w, http.StatusOK, s.authService.ListUsers())
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Role != "admin" {
		writeError(w, http.StatusForbidden, errors.New("admin role required"))
		return
	}

	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	user, err := s.authService.CreateUser(req.Username, req.Password, req.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	actor := currentUser(r)
	s.recordAudit("user", actor.Username, "user.created", "user", user.Username, map[string]any{
		"role": user.Role,
	})

	writeJSON(w, http.StatusCreated, user)
}

// handleDeleteUser deletes a user. Admin only; cannot delete self.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Role != "admin" {
		writeError(w, http.StatusForbidden, errors.New("admin role required"))
		return
	}

	username := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	if username == "" {
		writeError(w, http.StatusBadRequest, errors.New("username is required"))
		return
	}

	// Parse path: /api/v1/users/{username} or /api/v1/users/{username}/password
	username = strings.Split(username, "/")[0]

	if username == currentUser(r).Username {
		writeError(w, http.StatusBadRequest, errors.New("cannot delete yourself"))
		return
	}

	if err := s.authService.DeleteUser(username); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	actor := currentUser(r)
	s.recordAudit("user", actor.Username, "user.deleted", "user", username, nil)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleChangePassword changes a user's password.
// Path: PUT /api/v1/users/{username}/password
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "password" {
		writeError(w, http.StatusNotFound, errors.New("route not found"))
		return
	}

	username := parts[0]
	actor := currentUser(r)

	// Only admin or the user themselves can change password.
	if actor.Role != "admin" && actor.Username != username {
		writeError(w, http.StatusForbidden, errors.New("cannot change another user's password"))
		return
	}

	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, errors.New("new_password is required"))
		return
	}

	if err := s.authService.ChangePassword(username, req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	s.recordAudit("user", actor.Username, "user.password_changed", "user", username, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "password updated"})
}
