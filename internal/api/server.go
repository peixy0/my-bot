package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
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
	inbox      inbox.Inbox[events.AgentEvent]
	assetsPath string
	rt         runtime.Runtime
	mux        *http.ServeMux
	token      string
}

type ServerOptions struct {
	Token      string
	AssetsPath string
}

func NewServer(agentInbox inbox.Inbox[events.AgentEvent], rt runtime.Runtime, opts ServerOptions) *Server {
	s := &Server{
		inbox:      agentInbox,
		assetsPath: opts.AssetsPath,
		rt:         rt,
		mux:        http.NewServeMux(),
		token:      opts.Token,
	}
	s.mux.HandleFunc("/", s.handleFrontend)
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
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown api server", "err", err)
		}
	}()
	return srv.ListenAndServe()
}

func (s *Server) nextChatID() string {
	return fmt.Sprintf("session-%s", uuid.NewString())
}
func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	relativePath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if relativePath == "" {
		relativePath = "index.html"
	}
	assetPath := filepath.Join(s.assetsPath, filepath.FromSlash(relativePath))
	info, err := os.Stat(assetPath)
	if err == nil && !info.IsDir() {
		http.ServeFile(w, r, assetPath)
		return
	}
	if path.Ext(relativePath) != "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.assetsPath, "index.html"))
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
