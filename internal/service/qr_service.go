package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"fictin/internal/repository"
)

// TokenMessage is what a professor's board receives each rotation tick.
type TokenMessage struct {
	SessionID string    `json:"session_id"`
	Token     string    `json:"token"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// QRManager runs one rotation goroutine per live session and fans the rotating
// token out to subscribed WebSocket clients.
type QRManager struct {
	store            *repository.Store
	tokenTTL         time.Duration
	rotationInterval time.Duration
	log              *slog.Logger

	mu      sync.Mutex
	runners map[uuid.UUID]*sessionRunner
}

func NewQRManager(store *repository.Store, tokenTTL, rotationInterval time.Duration, log *slog.Logger) *QRManager {
	return &QRManager{
		store:            store,
		tokenTTL:         tokenTTL,
		rotationInterval: rotationInterval,
		log:              log,
		runners:          make(map[uuid.UUID]*sessionRunner),
	}
}

// Start launches (or no-ops if already running) the rotation goroutine for a session.
func (m *QRManager) Start(sessionID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runners[sessionID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &sessionRunner{
		sessionID:   sessionID,
		cancel:      cancel,
		subscribers: make(map[int]chan TokenMessage),
	}
	m.runners[sessionID] = r
	go m.loop(ctx, r)
	m.log.Info("qr rotation started", "session_id", sessionID)
}

// Stop cancels the rotation goroutine for a session and closes subscribers.
func (m *QRManager) Stop(sessionID uuid.UUID) {
	m.mu.Lock()
	r, ok := m.runners[sessionID]
	if ok {
		delete(m.runners, sessionID)
	}
	m.mu.Unlock()
	if ok {
		r.cancel()
		r.closeAll()
		m.log.Info("qr rotation stopped", "session_id", sessionID)
	}
}

// StopAll cancels every runner (used on graceful shutdown). DB status is left
// untouched so rotation resumes on the next boot.
func (m *QRManager) StopAll() {
	m.mu.Lock()
	runners := m.runners
	m.runners = make(map[uuid.UUID]*sessionRunner)
	m.mu.Unlock()
	for _, r := range runners {
		r.cancel()
		r.closeAll()
	}
}

// Subscribe registers a board client. The returned cancel func must be called
// when the client disconnects.
func (m *QRManager) Subscribe(sessionID uuid.UUID) (<-chan TokenMessage, func(), bool) {
	m.mu.Lock()
	r, ok := m.runners[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, nil, false
	}
	ch, id := r.addSubscriber()
	return ch, func() { r.removeSubscriber(id) }, true
}

// Current returns the last token minted in-process for a session, if any.
func (m *QRManager) Current(sessionID uuid.UUID) (TokenMessage, bool) {
	m.mu.Lock()
	r, ok := m.runners[sessionID]
	m.mu.Unlock()
	if !ok {
		return TokenMessage{}, false
	}
	return r.currentToken()
}

func (m *QRManager) IsRunning(sessionID uuid.UUID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.runners[sessionID]
	return ok
}

func (m *QRManager) loop(ctx context.Context, r *sessionRunner) {
	// Emit one immediately, then on every tick.
	m.rotate(ctx, r)

	ticker := time.NewTicker(m.rotationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.rotate(ctx, r)
		}
	}
}

func (m *QRManager) rotate(ctx context.Context, r *sessionRunner) {
	tok, err := generateToken()
	if err != nil {
		m.log.Error("qr token generation failed", "session_id", r.sessionID, "err", err)
		return
	}
	issued := time.Now().UTC()
	expires := issued.Add(m.tokenTTL)

	insCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	row, err := m.store.QRTokens.Insert(insCtx, r.sessionID, tok, issued, expires)
	cancel()
	if err != nil {
		m.log.Error("qr token persist failed", "session_id", r.sessionID, "err", err)
		return
	}

	msg := TokenMessage{
		SessionID: r.sessionID.String(),
		Token:     row.Token,
		IssuedAt:  row.IssuedAt,
		ExpiresAt: row.ExpiresAt,
	}
	r.setCurrent(msg)
	r.broadcast(msg)
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sessionRunner holds per-session rotation state.
type sessionRunner struct {
	sessionID uuid.UUID
	cancel    context.CancelFunc

	mu          sync.Mutex
	nextSubID   int
	subscribers map[int]chan TokenMessage
	current     TokenMessage
	hasCurrent  bool
}

func (r *sessionRunner) addSubscriber() (chan TokenMessage, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextSubID
	r.nextSubID++
	ch := make(chan TokenMessage, 4)
	r.subscribers[id] = ch
	if r.hasCurrent {
		ch <- r.current
	}
	return ch, id
}

func (r *sessionRunner) removeSubscriber(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.subscribers[id]; ok {
		delete(r.subscribers, id)
		close(ch)
	}
}

func (r *sessionRunner) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, ch := range r.subscribers {
		delete(r.subscribers, id)
		close(ch)
	}
}

func (r *sessionRunner) broadcast(msg TokenMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.subscribers {
		select {
		case ch <- msg:
		default: // slow client: drop this tick
		}
	}
}

func (r *sessionRunner) setCurrent(msg TokenMessage) {
	r.mu.Lock()
	r.current = msg
	r.hasCurrent = true
	r.mu.Unlock()
}

func (r *sessionRunner) currentToken() (TokenMessage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current, r.hasCurrent
}

// ResumeLiveSessions restarts rotation goroutines for sessions the DB still
// marks live (e.g. after a server restart).
func (m *QRManager) ResumeLiveSessions(ctx context.Context) error {
	live, err := m.store.Sessions.ListLive(ctx)
	if err != nil {
		return err
	}
	for _, s := range live {
		m.Start(s.ID)
	}
	if len(live) > 0 {
		m.log.Info("resumed live sessions", "count", len(live))
	}
	return nil
}
