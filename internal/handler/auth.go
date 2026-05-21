package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	authsvc "github.com/mkapitanoff/pki-service/internal/auth"
	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

const userIDKey ctxKey = "user_id"

// JWTAuth validates Bearer JWT and injects Claims into the request context.
func JWTAuth(svc *authsvc.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(authz, prefix) {
				respondError(w, apperr.ErrUnauthorized)
				return
			}
			tokenStr := strings.TrimSpace(authz[len(prefix):])

			claims, err := svc.ValidateToken(r.Context(), tokenStr)
			if err != nil {
				respondError(w, apperr.ErrUnauthorized)
				return
			}

			ctx := r.Context()
			ctx = WithTenant(ctx, claims.TenantID)
			ctx = context.WithValue(ctx, userIDKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AuthHandler handles registration, login, me, logout endpoints.
type AuthHandler struct {
	svc *authsvc.AuthService
}

func NewAuthHandler(svc *authsvc.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// HandleRegister — POST /auth/register
func (h *AuthHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}
	if req.Email == "" || req.Password == "" || req.Name == "" {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	user, err := h.svc.Register(r.Context(), req.Email, req.Password, req.Name, uuid.Nil)
	if err != nil {
		respondError(w, err)
		return
	}

	token, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"user":  userView(user),
		"token": token,
	})
}

// HandleLogin — POST /auth/login
func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	token, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		respondError(w, err)
		return
	}

	claims, err := h.svc.ValidateToken(r.Context(), token)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	user, err := h.svc.Me(r.Context(), claims.UserID)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"user":  userView(user),
		"token": token,
	})
}

// HandleMe — GET /auth/me (requires JWTAuth middleware)
func (h *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(uuid.UUID)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	user, err := h.svc.Me(r.Context(), userID)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, userView(user))
}

// HandleLogout — POST /auth/logout (revokes stored token hash)
func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	authz := r.Header.Get("Authorization")
	if strings.HasPrefix(authz, "Bearer ") {
		tokenStr := strings.TrimSpace(authz[len("Bearer "):])
		h.svc.RevokeToken(r.Context(), tokenStr)
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func userView(u *repository.User) map[string]any {
	return map[string]any{
		"id":        u.ID,
		"email":     u.Email,
		"name":      u.Name,
		"role":      u.Role,
		"tenant_id": u.TenantID.UUID,
	}
}
