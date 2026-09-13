package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"fictin/internal/apperr"
	"fictin/internal/auth"
	"fictin/internal/httpresponse"
	"fictin/internal/service"
)

type WSHandler struct {
	sessions       *service.SessionService
	qr             *service.QRManager
	allowedOrigins []string
}

func NewWSHandler(sessions *service.SessionService, qr *service.QRManager, allowedOrigins []string) *WSHandler {
	return &WSHandler{sessions: sessions, qr: qr, allowedOrigins: allowedOrigins}
}

// StreamQR upgrades to a WebSocket and pushes the rotating token to the
// owning professor's board (spec §7.2). Auth is via ?access_token= because
// browsers cannot set headers on the WS handshake.
func (h *WSHandler) StreamQR(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	sessionID, err := uuidParam(r, "session_id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	sess, err := h.sessions.LoadOwnedByProfessor(r.Context(), sessionID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	if sess.Status != "live" {
		httpresponse.FromError(w, r, apperr.SessionNotLive)
		return
	}

	opts := &websocket.AcceptOptions{}
	if len(h.allowedOrigins) == 1 && h.allowedOrigins[0] == "*" {
		opts.InsecureSkipVerify = true
	} else {
		opts.OriginPatterns = h.allowedOrigins
	}

	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return // Accept already wrote a response
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context()) // detect client disconnect

	ch, unsubscribe, ok := h.qr.Subscribe(sessionID)
	if !ok {
		_ = conn.Close(websocket.StatusGoingAway, "session not live")
		return
	}
	defer unsubscribe()

	// Keepalive ping so dead peers are noticed.
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		case msg, open := <-ch:
			if !open {
				_ = conn.Close(websocket.StatusNormalClosure, "session ended")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := wsjson.Write(wctx, conn, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
