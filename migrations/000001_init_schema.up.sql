-- ========== EXTENSIONS ==========
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ========== USERS ==========
CREATE TYPE user_role AS ENUM ('admin', 'professor', 'student');

CREATE TABLE users (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name            VARCHAR(150) NOT NULL,
    email                VARCHAR(255) NOT NULL UNIQUE,
    password_hash        TEXT NOT NULL,
    role                 user_role NOT NULL,
    student_code         VARCHAR(50) UNIQUE,      -- only for students (index number)
    is_active            BOOLEAN NOT NULL DEFAULT TRUE,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_role ON users(role);

-- ========== REFRESH TOKENS (DB-backed for revocation) ==========
CREATE TABLE refresh_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,           -- sha-256 hex of the raw refresh token
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);

-- ========== SUBJECTS ==========
CREATE TABLE subjects (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(150) NOT NULL,
    code          VARCHAR(30) NOT NULL UNIQUE,   -- e.g. "CS101"
    professor_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_subjects_professor ON subjects(professor_id);

-- ========== ENROLLMENTS ==========
CREATE TABLE enrollments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id   UUID NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    student_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (subject_id, student_id)
);

CREATE INDEX idx_enrollments_student ON enrollments(student_id);

-- ========== CLASS SCHEDULES (recurring weekly slot, optional/reference) ==========
CREATE TABLE class_schedules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id   UUID NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    day_of_week  SMALLINT NOT NULL CHECK (day_of_week BETWEEN 0 AND 6), -- 0=Sunday
    start_time   TIME NOT NULL,
    end_time     TIME NOT NULL,
    room         VARCHAR(100),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_time > start_time)
);

CREATE INDEX idx_class_schedules_subject ON class_schedules(subject_id);

-- ========== CLASS SESSIONS (a single concrete class occurrence) ==========
CREATE TYPE session_status AS ENUM ('pending', 'live', 'ended');

CREATE TABLE class_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id      UUID NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    schedule_id     UUID REFERENCES class_schedules(id) ON DELETE SET NULL,
    status          session_status NOT NULL DEFAULT 'pending',
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_class_sessions_subject ON class_sessions(subject_id);

-- Enforce only one live session per subject at a time (partial unique index)
CREATE UNIQUE INDEX one_live_session_per_subject
    ON class_sessions (subject_id)
    WHERE status = 'live';

-- ========== QR TOKENS (rotating every 1s while session is live) ==========
CREATE TABLE qr_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID NOT NULL REFERENCES class_sessions(id) ON DELETE CASCADE,
    token        VARCHAR(128) NOT NULL UNIQUE,
    issued_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL   -- issued_at + ~1-2 seconds
);

CREATE INDEX idx_qr_tokens_session ON qr_tokens(session_id);
CREATE INDEX idx_qr_tokens_expiry ON qr_tokens(expires_at);

-- ========== ATTENDANCE RECORDS ==========
CREATE TABLE attendance_records (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID NOT NULL REFERENCES class_sessions(id) ON DELETE CASCADE,
    student_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    qr_token_id  UUID REFERENCES qr_tokens(id) ON DELETE SET NULL,
    scanned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, student_id)   -- one record per student per session
);

CREATE INDEX idx_attendance_session ON attendance_records(session_id);
CREATE INDEX idx_attendance_student ON attendance_records(student_id);
