package handlers

import (
	"net/http"

	"fictin/internal/auth"
	"fictin/internal/httpresponse"
	"fictin/internal/models"
	"fictin/internal/service"
)

type AuthHandler struct {
	svc *service.AuthService
}

func NewAuthHandler(svc *service.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

type registerRequest struct {
	FullName    string `json:"full_name" validate:"required,min=2,max=150"`
	Email       string `json:"email" validate:"required,email,max=255"`
	Password    string `json:"password" validate:"required,min=8,max=128"`
	Role        string `json:"role" validate:"required,oneof=admin professor student"`
	StudentCode string `json:"student_code" validate:"omitempty,max=50"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	in := service.RegisterInput{
		FullName: req.FullName,
		Email:    req.Email,
		Password: req.Password,
		Role:     models.Role(req.Role),
	}
	if req.StudentCode != "" {
		in.StudentCode = &req.StudentCode
	}
	u, err := h.svc.Register(r.Context(), in)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.Created(w, u)
}

type loginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	pair, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, tokenResponse(pair))
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	pair, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, tokenResponse(pair))
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, map[string]string{"status": "logged_out"})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	u, err := h.svc.Me(r.Context(), id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, u)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password" validate:"required,min=8,max=128"`
}

func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.ChangePassword(r.Context(), id.UserID, req.CurrentPassword, req.NewPassword); err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, map[string]string{"status": "password_changed"})
}

func tokenResponse(p *service.TokenPair) map[string]any {
	return map[string]any{
		"access_token":  p.AccessToken,
		"refresh_token": p.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    p.ExpiresInSec,
		"user":          p.User,
	}
}
