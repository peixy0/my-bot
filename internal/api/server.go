package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
	messaging "my-bot/internal/messaging/websocket"
	"my-bot/internal/runtime"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	pingInterval   = 20 * time.Second
	pongTimeout    = 10 * time.Second
	enqueueTimeout = 5 * time.Second
)

type Server struct {
	inbox         inbox.Inbox[events.AgentEvent]
	indexHTMLPath string
	rt            runtime.Runtime
	mux           *http.ServeMux
	token         string
}

type ServerOptions struct {
	Token         string
	IndexHTMLPath string
}

func NewServer(agentInbox inbox.Inbox[events.AgentEvent], rt runtime.Runtime, opts ServerOptions) *Server {
	s := &Server{
		inbox:         agentInbox,
		indexHTMLPath: opts.IndexHTMLPath,
		rt:            rt,
		mux:           http.NewServeMux(),
		token:         opts.Token,
	}
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/bot", s.handleWS)
	return s
}

func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv.ListenAndServe()
}

func (s *Server) nextChatID() string {
	return fmt.Sprintf("session-%s", uuid.NewString())
}
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	data, err := os.ReadFile(s.indexHTMLPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
	})
	_ = conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))

	chatID := s.nextChatID()
	outbound := messaging.NewOutbound(conn, chatID, s.rt)
	outbound.Connected(r.Context())

	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := outbound.Ping(pongTimeout); err != nil {
					return
				}
			case <-r.Context().Done():
				return
			}
		}
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var frame struct {
			Type      string `json:"type"`
			Message   string `json:"message"`
			MessageID string `json:"message_id"`
			Data      string `json:"data"`
			MIMEType  string `json:"mime_type"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			slog.Warn("websocket frame parse failed", "err", err)
			continue
		}

		switch frame.Type {
		case "text":
			if !s.enqueue(r.Context(), events.TextInputEvent{
				ChatID:    chatID,
				MessageID: frame.MessageID,
				Message:   frame.Message,
				Sender:    outbound,
			}) {
				return
			}
		case "image":
			imgData, err := base64.StdEncoding.DecodeString(frame.Data)
			if err != nil {
				imgData, err = base64.RawStdEncoding.DecodeString(frame.Data)
			}
			if err == nil && len(imgData) > 0 {
				if !s.enqueue(r.Context(), events.ImageInputEvent{
					ChatID:    chatID,
					MessageID: frame.MessageID,
					ImageData: []events.ImageData{{Data: imgData, MIMEType: frame.MIMEType}},
					Message:   frame.Message,
					Sender:    outbound,
				}) {
					return
				}
			}
		}
	}

	s.enqueue(context.Background(), events.DropSessionEvent{ChatID: chatID})
}

func (s *Server) enqueue(ctx context.Context, ev events.AgentEvent) bool {
	ctx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()
	err := s.inbox.Publish(ctx, ev)
	if err == nil {
		return true
	}
	if ctx.Err() == context.DeadlineExceeded {
		slog.Warn("enqueue timeout", "event", fmt.Sprintf("%T", ev), "timeout", enqueueTimeout)
	} else {
		slog.Warn("enqueue canceled", "event", fmt.Sprintf("%T", ev), "err", err)
	}
	return false
}

func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	if r.URL.Query().Get("token") == s.token {
		return true
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	return strings.HasPrefix(auth, prefix) && strings.TrimPrefix(auth, prefix) == s.token
}
