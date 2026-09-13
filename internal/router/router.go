// Package router wires middleware and routes to handlers.
package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"fictin/internal/auth"
	"fictin/internal/handlers"
	"fictin/internal/middleware"
	"fictin/internal/models"
)

// Deps is everything the router needs to mount routes.
type Deps struct {
	Log         *slog.Logger
	AuthMW      *auth.Middleware
	ScanLimiter *middleware.PerUserRateLimiter
	CORSOrigins []string
	Health      *handlers.HealthHandler
	Auth        *handlers.AuthHandler
	Admin       *handlers.AdminHandler
	Subject     *handlers.SubjectHandler
	Session     *handlers.SessionHandler
	Attendance  *handlers.AttendanceHandler
	WS          *handlers.WSHandler
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS(d.CORSOrigins))
	r.Use(middleware.RequestLogger(d.Log))
	r.Use(chimw.Timeout(30 * time.Second))

	r.Get("/healthz", d.Health.Live)
	r.Get("/readyz", d.Health.Ready)

	authed := func(next http.Handler) http.Handler { return d.AuthMW.AuthRequired(next) }
	requireAdmin := d.AuthMW.RequireRole(models.RoleAdmin)
	requireProfessor := d.AuthMW.RequireRole(models.RoleProfessor)
	requireStudent := d.AuthMW.RequireRole(models.RoleStudent)
	requireProfessorOrAdmin := d.AuthMW.RequireRole(models.RoleProfessor, models.RoleAdmin)

	r.Route("/api/v1", func(r chi.Router) {
		// ---- Auth ----
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", d.Auth.Login)
			r.Post("/refresh", d.Auth.Refresh)
			r.Post("/logout", d.Auth.Logout)

			r.Group(func(r chi.Router) {
				r.Use(authed)
				r.Get("/me", d.Auth.Me)
				r.Post("/change-password", d.Auth.ChangePassword)
			})

			r.Group(func(r chi.Router) {
				r.Use(authed, requireAdmin)
				r.Post("/register", d.Auth.Register)
			})
		})

		// ---- Admin ----
		r.Route("/admin", func(r chi.Router) {
			r.Use(authed, requireAdmin)
			r.Get("/users", d.Admin.ListUsers)
			r.Patch("/users/{id}", d.Admin.UpdateUser)
			r.Delete("/users/{id}", d.Admin.DeleteUser)
			r.Get("/subjects", d.Admin.ListSubjects)
			r.Get("/attendance", d.Admin.ListAttendance)
		})

		// ---- Professor: subjects, schedules, enrollments ----
		r.Route("/subjects", func(r chi.Router) {
			r.Use(authed, requireProfessor)
			r.Post("/", d.Subject.Create)
			r.Get("/", d.Subject.List)
			r.Get("/{id}", d.Subject.Get)
			r.Patch("/{id}", d.Subject.Update)
			r.Delete("/{id}", d.Subject.Delete)
			r.Post("/{id}/schedules", d.Subject.AddSchedule)
			r.Get("/{id}/schedules", d.Subject.ListSchedules)
			r.Post("/{id}/enrollments", d.Subject.Enroll)
			r.Get("/{id}/enrollments", d.Subject.ListRoster)
		})

		// ---- Professor: sessions & QR ----
		r.Route("/sessions", func(r chi.Router) {
			r.Use(authed)

			// {id} is a subject id for start, a session id for the rest.
			r.With(requireProfessor).Post("/{id}/start", d.Session.Start)
			r.With(requireProfessor).Post("/{id}/stop", d.Session.Stop)
			r.With(requireProfessor).Get("/{id}/qr/current", d.Session.CurrentQR)
			r.With(requireProfessor).Get("/{id}/attendance", d.Session.SessionAttendance)
			r.With(requireProfessorOrAdmin).Get("/{id}", d.Session.Get)
		})

		// ---- Student ----
		r.Group(func(r chi.Router) {
			r.Use(authed, requireStudent)
			r.Get("/student/subjects", d.Attendance.StudentSubjects)
			r.Get("/student/attendance", d.Attendance.StudentHistory)
			r.With(d.ScanLimiter.Middleware).Post("/attendance/scan", d.Attendance.Scan)
		})
	})

	// ---- WebSocket: professor board QR stream ----
	r.Group(func(r chi.Router) {
		r.Use(authed, requireProfessor)
		r.Get("/ws/sessions/{session_id}/qr", d.WS.StreamQR)
	})

	return r
}
