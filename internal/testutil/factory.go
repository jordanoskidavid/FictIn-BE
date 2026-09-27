package testutil

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"fictin/internal/auth"
	"fictin/internal/models"
	"fictin/internal/repository"
)

// DefaultPassword is the password of every factory-created user unless overridden.
const DefaultPassword = "Passw0rd-for-tests"

// Factory inserts test data straight into the database with sensible,
// unique defaults. Every builder accepts options to override fields.
type Factory struct {
	store  *repository.Store
	hasher *auth.Hasher
}

func NewFactory(store *repository.Store, hasher *auth.Hasher) *Factory {
	return &Factory{store: store, hasher: hasher}
}

var seq atomic.Int64

// next returns a process-unique number so defaults never collide.
func next() int64 { return seq.Add(1) }

// ---- users ----

// UserSpec describes a user to create; override it with UserOpt functions.
type UserSpec struct {
	FullName           string
	Email              string
	Password           string
	StudentCode        *string // defaulted for students only
	MustChangePassword bool
	Inactive           bool
}

type UserOpt func(*UserSpec)

func WithEmail(email string) UserOpt      { return func(s *UserSpec) { s.Email = email } }
func WithPassword(pw string) UserOpt      { return func(s *UserSpec) { s.Password = pw } }
func WithFullName(name string) UserOpt    { return func(s *UserSpec) { s.FullName = name } }
func WithStudentCode(code string) UserOpt { return func(s *UserSpec) { s.StudentCode = &code } }
func MustChangePassword() UserOpt         { return func(s *UserSpec) { s.MustChangePassword = true } }
func Inactive() UserOpt                   { return func(s *UserSpec) { s.Inactive = true } }

// User creates a user with the given role.
func (f *Factory) User(t testing.TB, role models.Role, opts ...UserOpt) *models.User {
	t.Helper()
	n := next()
	spec := UserSpec{
		FullName: fmt.Sprintf("Test %s %d", role, n),
		Email:    fmt.Sprintf("%s%d-%s@example.test", role, n, uuid.NewString()[:8]),
		Password: DefaultPassword,
	}
	if role == models.RoleStudent {
		code := fmt.Sprintf("S%06d-%s", n, uuid.NewString()[:4])
		spec.StudentCode = &code
	}
	for _, o := range opts {
		o(&spec)
	}

	hash, err := f.hasher.Hash(spec.Password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	ctx := context.Background()
	u, err := f.store.Users.Create(ctx, repository.CreateUserParams{
		FullName:           spec.FullName,
		Email:              spec.Email,
		PasswordHash:       hash,
		Role:               role,
		StudentCode:        spec.StudentCode,
		MustChangePassword: spec.MustChangePassword,
	})
	if err != nil {
		t.Fatalf("create %s user: %v", role, err)
	}
	if spec.Inactive {
		inactive := false
		if u, err = f.store.Users.Update(ctx, u.ID, repository.UpdateUserParams{IsActive: &inactive}); err != nil {
			t.Fatalf("deactivate user: %v", err)
		}
	}
	return u
}

// ---- subjects & enrollments ----

type SubjectSpec struct {
	Name string
	Code string
}

type SubjectOpt func(*SubjectSpec)

func WithSubjectCode(code string) SubjectOpt { return func(s *SubjectSpec) { s.Code = code } }
func WithSubjectName(name string) SubjectOpt { return func(s *SubjectSpec) { s.Name = name } }

// Subject creates a subject owned by professorID.
func (f *Factory) Subject(t testing.TB, professorID uuid.UUID, opts ...SubjectOpt) *models.Subject {
	t.Helper()
	n := next()
	spec := SubjectSpec{Name: fmt.Sprintf("Subject %d", n), Code: fmt.Sprintf("SUB%d", n)}
	for _, o := range opts {
		o(&spec)
	}
	s, err := f.store.Subjects.Create(context.Background(), spec.Name, spec.Code, professorID)
	if err != nil {
		t.Fatalf("create subject: %v", err)
	}
	return s
}

// Enroll enrolls studentID in subjectID.
func (f *Factory) Enroll(t testing.TB, subjectID, studentID uuid.UUID) *models.Enrollment {
	t.Helper()
	e, err := f.store.Enroll.Create(context.Background(), subjectID, studentID)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	return e
}

// ---- sessions, QR tokens, attendance ----

// LiveSession inserts a live session row. It does NOT start QR rotation; use
// the start endpoint when a test needs rotating tokens.
func (f *Factory) LiveSession(t testing.TB, subjectID uuid.UUID) *models.ClassSession {
	t.Helper()
	s, err := f.store.Sessions.StartLive(context.Background(), subjectID, nil)
	if err != nil {
		t.Fatalf("start live session: %v", err)
	}
	return s
}

// EndedSession inserts a session and immediately ends it.
func (f *Factory) EndedSession(t testing.TB, subjectID uuid.UUID) *models.ClassSession {
	t.Helper()
	s := f.LiveSession(t, subjectID)
	ended, err := f.store.Sessions.Stop(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("stop session: %v", err)
	}
	return ended
}

// QRToken inserts a token for sessionID issued at issuedAt and valid for ttl.
func (f *Factory) QRToken(t testing.TB, sessionID uuid.UUID, issuedAt time.Time, ttl time.Duration) *models.QRToken {
	t.Helper()
	tok := fmt.Sprintf("test-token-%d-%s", next(), uuid.NewString())
	q, err := f.store.QRTokens.Insert(context.Background(), sessionID, tok, issuedAt.UTC(), issuedAt.UTC().Add(ttl))
	if err != nil {
		t.Fatalf("insert qr token: %v", err)
	}
	return q
}

// Attendance records studentID as present in sessionID.
func (f *Factory) Attendance(t testing.TB, sessionID, studentID uuid.UUID) *models.AttendanceRecord {
	t.Helper()
	a, err := f.store.Attendance.Insert(context.Background(), sessionID, studentID, nil)
	if err != nil {
		t.Fatalf("insert attendance: %v", err)
	}
	return a
}

// Classroom is a professor with a subject, one enrolled student and a live
// session: the common starting point for attendance tests.
type Classroom struct {
	Professor *models.User
	Student   *models.User
	Subject   *models.Subject
	Session   *models.ClassSession
}

func (f *Factory) Classroom(t testing.TB) Classroom {
	t.Helper()
	prof := f.User(t, models.RoleProfessor)
	stud := f.User(t, models.RoleStudent)
	subj := f.Subject(t, prof.ID)
	f.Enroll(t, subj.ID, stud.ID)
	return Classroom{Professor: prof, Student: stud, Subject: subj, Session: f.LiveSession(t, subj.ID)}
}
