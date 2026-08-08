package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"my-bot/internal/runtime"
)

var ErrDisconnected = errors.New("browser extension is not connected")

const protocolVersion = 1

type Config struct {
	ListenAddr string
	Path       string
}

type BrokerCaller interface {
	Call(context.Context, string, string, any) (<-chan brokerFrame, error)
	CloseScope(context.Context, string) error
}

type Broker interface {
	NewClient() Client
	Run(context.Context) error
}

type ExtensionBroker struct {
	cfg          Config
	rt           runtime.Runtime
	maxChars     int
	requests     chan brokerCall
	connected    chan *extensionConn
	disconnected chan *extensionConn
	responses    chan brokerResponse
	done         chan struct{}
	nextID       atomic.Uint64
}

type brokerCall struct {
	ctx    context.Context
	scope  string
	action string
	params any
	reply  chan brokerFrame
}

type brokerFrame struct {
	data string
	err  error
}

type extensionConn struct {
	conn *websocket.Conn
}

type brokerRequest struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	ScopeID string `json:"scope_id"`
	Action  string `json:"action"`
	Params  any    `json:"params,omitempty"`
}

type brokerResponse struct {
	Type    string `json:"type,omitempty"`
	ID      string `json:"id"`
	Data    string `json:"data"`
	Error   string `json:"error"`
	HasMore bool   `json:"has_more,omitempty"`
	conn    *extensionConn
}

func NewExtensionBroker(cfg Config, rt runtime.Runtime, maxChars int) *ExtensionBroker {
	if cfg.Path == "" {
		cfg.Path = "/browser"
	}
	return &ExtensionBroker{
		cfg:          cfg,
		rt:           rt,
		maxChars:     maxChars,
		requests:     make(chan brokerCall),
		connected:    make(chan *extensionConn),
		disconnected: make(chan *extensionConn),
		responses:    make(chan brokerResponse, 64),
		done:         make(chan struct{}),
	}
}

type NoopBroker struct{}

func NewNoopBroker() *NoopBroker {
	return &NoopBroker{}
}

func (b *ExtensionBroker) NewClient() Client {
	return newClient(b, b.rt, b.maxChars)
}

func (b *NoopBroker) NewClient() Client {
	return NewNoopClient()
}

func (b *NoopBroker) Run(context.Context) error {
	return nil
}

func (b *ExtensionBroker) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", b.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen browser extension: %w", err)
	}
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc(b.cfg.Path, b.handleWebSocket)
	server := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown browser extension broker", "err", err)
		}
	}()
	go b.loop(ctx)

	slog.Info("browser extension broker listening", "addr", listener.Addr().String(), "path", b.cfg.Path)
	serveErr := server.Serve(listener)
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}

func (b *ExtensionBroker) Call(ctx context.Context, scopeID, action string, params any) (<-chan brokerFrame, error) {
	reply := make(chan brokerFrame, 8)
	call := brokerCall{ctx: ctx, scope: scopeID, action: action, params: params, reply: reply}
	select {
	case b.requests <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.done:
		return nil, ErrDisconnected
	}
	return reply, nil
}

func (b *ExtensionBroker) CloseScope(ctx context.Context, scopeID string) error {
	frames, err := b.Call(ctx, scopeID, "scope_close", map[string]any{})
	if err != nil {
		return err
	}
	for range frames {
	}
	return nil
}

func (b *ExtensionBroker) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("browser extension connect failed", "err", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(2 << 20)
	if !b.authenticate(conn) {
		return
	}
	extension := &extensionConn{conn: conn}
	select {
	case b.connected <- extension:
	case <-b.done:
		return
	}
	defer func() {
		select {
		case b.disconnected <- extension:
		case <-b.done:
		}
	}()
	for {
		var frame brokerResponse
		if err := conn.ReadJSON(&frame); err != nil {
			return
		}
		frame.conn = extension
		select {
		case b.responses <- frame:
		case <-b.done:
			return
		}
	}
}

func (b *ExtensionBroker) authenticate(conn *websocket.Conn) bool {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	var frame struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
	}
	if err := conn.ReadJSON(&frame); err != nil {
		return false
	}
	if frame.Type != "authenticate" || frame.Version != protocolVersion {
		return false
	}
	return conn.WriteJSON(map[string]any{"type": "authenticated", "version": protocolVersion}) == nil
}

func (b *ExtensionBroker) loop(ctx context.Context) {
	defer close(b.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var extension *extensionConn
	pending := make(map[string]*brokerCall)
	failPending := func(err error) {
		for id, request := range pending {
			delete(pending, id)
			request.reply <- brokerFrame{err: err}
			close(request.reply)
		}
	}
	for {
		select {
		case <-ctx.Done():
			failPending(ctx.Err())
			return
		case <-ticker.C:
			for id, request := range pending {
				if err := request.ctx.Err(); err != nil {
					delete(pending, id)
					request.reply <- brokerFrame{err: err}
					close(request.reply)
				}
			}
		case extension = <-b.connected:
			if extension != nil {
				failPending(ErrDisconnected)
			}
		case disconnected := <-b.disconnected:
			if disconnected == extension {
				extension = nil
				failPending(ErrDisconnected)
			}
		case response := <-b.responses:
			if response.conn != extension {
				continue
			}
			if response.Type == "ping" {
				_ = extension.conn.WriteJSON(map[string]string{"type": "pong"})
				continue
			}
			if response.ID == "" {
				continue
			}
			request, ok := pending[response.ID]
			if !ok {
				continue
			}
			frame := brokerFrame{data: response.Data}
			if response.Error != "" {
				frame.err = errors.New(response.Error)
			}
			if !response.HasMore {
				delete(pending, response.ID)
				request.reply <- frame
				close(request.reply)
			} else {
				request.reply <- frame
			}
		case call := <-b.requests:
			if extension == nil {
				call.reply <- brokerFrame{err: ErrDisconnected}
				close(call.reply)
				continue
			}
			id := fmt.Sprintf("browser-%d-%s", b.nextID.Add(1), uuid.NewString())
			frame := requestFromCall(id, call)
			if err := extension.conn.WriteJSON(frame); err != nil {
				call.reply <- brokerFrame{err: fmt.Errorf("send browser request: %w", err)}
				close(call.reply)
				continue
			}
			pending[id] = &call
		}
	}
}

func requestFromCall(id string, call brokerCall) brokerRequest {
	return brokerRequest{
		Type:    "request",
		ID:      id,
		ScopeID: call.scope,
		Action:  call.action,
		Params:  call.params,
	}
}
