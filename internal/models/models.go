// Package models holds the domain structs mapped to database rows.
package models

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleAdmin     Role = "admin"
	RoleProfessor Role = "professor"
	RoleStudent   Role = "student"
)

func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleProfessor, RoleStudent:
		return true
	default:
		return false
	}
}

type SessionStatus string

const (
	SessionPending SessionStatus = "pending"
	SessionLive    SessionStatus = "live"
	SessionEnded   SessionStatus = "ended"
)

// User is a row of the users table. PasswordHash is never serialized.
type User struct {
	ID                 uuid.UUID `json:"id"`
	FullName           string    `json:"full_name"`
	Email              string    `json:"email"`
	PasswordHash       string    `json:"-"`
	Role               Role      `json:"role"`
	StudentCode        *string   `json:"student_code,omitempty"`
	IsActive           bool      `json:"is_active"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID // login chain; reuse of a rotated token revokes the family
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

type Subject struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Code        string    `json:"code"`
	ProfessorID uuid.UUID `json:"professor_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Enrollment struct {
	ID        uuid.UUID `json:"id"`
	SubjectID uuid.UUID `json:"subject_id"`
	StudentID uuid.UUID `json:"student_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ClassSchedule struct {
	ID        uuid.UUID `json:"id"`
	SubjectID uuid.UUID `json:"subject_id"`
	DayOfWeek int16     `json:"day_of_week"` // 0=Sunday .. 6=Saturday
	StartTime string    `json:"start_time"`  // "HH:MM" or "HH:MM:SS"
	EndTime   string    `json:"end_time"`
	Room      *string   `json:"room,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ClassSession struct {
	ID         uuid.UUID     `json:"id"`
	SubjectID  uuid.UUID     `json:"subject_id"`
	ScheduleID *uuid.UUID    `json:"schedule_id,omitempty"`
	Status     SessionStatus `json:"status"`
	StartedAt  *time.Time    `json:"started_at,omitempty"`
	EndedAt    *time.Time    `json:"ended_at,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
}

type QRToken struct {
	ID        uuid.UUID `json:"-"`
	SessionID uuid.UUID `json:"-"`
	Token     string    `json:"token"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AttendanceRecord struct {
	ID        uuid.UUID  `json:"id"`
	SessionID uuid.UUID  `json:"session_id"`
	StudentID uuid.UUID  `json:"student_id"`
	QRTokenID *uuid.UUID `json:"qr_token_id,omitempty"`
	ScannedAt time.Time  `json:"scanned_at"`
}

// AccountState is the live state of a user account, checked on every
// authenticated request so deactivation and forced password changes take
// effect immediately rather than when the access token expires.
type AccountState struct {
	Exists             bool
	Active             bool
	MustChangePassword bool
}
