package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath = "config.yaml"
	defaultHost       = "127.0.0.1"
	defaultPort       = 8017
)

type options struct {
	prompt     string
	configPath string
}

type webUIConfig struct {
	host  string
	port  int
	token string
}

type serverFrame struct {
	Type     string `json:"type"`
	ChatID   string `json:"chat_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

type promptFrame struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	MessageID string `json:"message_id"`
}

type outputState struct {
	stdout          io.Writer
	stderr          io.Writer
	streamOpen      bool
	lastByteNewline bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(ctx, opts, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var opts options
	fs.StringVar(&opts.prompt, "p", "", "prompt to send")
	fs.StringVar(&opts.configPath, "c", defaultConfigPath, "config file path")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if strings.TrimSpace(opts.prompt) == "" {
		return options{}, errors.New("usage: agent -p \"prompt\" [-c config.yaml]")
	}
	return opts, nil
}

func loadWebUIConfig(path string) (webUIConfig, error) {
	cfg := webUIConfig{host: defaultHost, port: defaultPort}
	data, err := os.ReadFile(path)
	if err != nil {
		return webUIConfig{}, fmt.Errorf("read config: %w", err)
	}

	var raw struct {
		WebUI struct {
			Host  string `yaml:"host"`
			Port  int    `yaml:"port"`
			Token string `yaml:"token"`
		} `yaml:"webui"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return webUIConfig{}, fmt.Errorf("parse config: %w", err)
	}
	if raw.WebUI.Host != "" {
		cfg.host = raw.WebUI.Host
	}
	if raw.WebUI.Port != 0 {
		cfg.port = raw.WebUI.Port
	}
	cfg.token = raw.WebUI.Token
	return cfg, nil
}

func run(ctx context.Context, opts options, stdout, stderr io.Writer) error {
	cfg, err := loadWebUIConfig(opts.configPath)
	if err != nil {
		return err
	}
	wsURL, err := buildWebSocketURL(cfg)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect websocket: %w", err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if err := conn.WriteJSON(promptFrame{
		Type:      "text",
		Message:   opts.prompt,
		MessageID: fmt.Sprintf("cli-%d", time.Now().UnixNano()),
	}); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}

	out := &outputState{stdout: stdout, stderr: stderr, lastByteNewline: true}
	for {
		var frame serverFrame
		if err := conn.ReadJSON(&frame); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read websocket: %w", err)
		}
		done, err := out.handle(frame)
		if err != nil {
			return err
		}
		if done {
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return nil
		}
	}
}

func buildWebSocketURL(cfg webUIConfig) (string, error) {
	raw := strings.TrimSpace(fmt.Sprintf("%s:%d", cfg.host, cfg.port))
	if raw == "" {
		raw = fmt.Sprintf("%s:%d", defaultHost, defaultPort)
	}
	if !strings.Contains(raw, "://") {
		raw = "ws://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse address: %w", err)
	}
	switch u.Scheme {
	case "ws", "wss":
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported address scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("address host is required")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/api/bot"
	}
	if cfg.token != "" {
		q := u.Query()
		q.Set("token", cfg.token)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (o *outputState) handle(frame serverFrame) (bool, error) {
	switch frame.Type {
	case "connected", "thinking_start":
		return false, nil
	case "message_stream_delta":
		o.writeStream(frame.Text)
		return false, nil
	case "message_stream_end":
		o.finishStream()
		return false, nil
	case "message":
		o.finishStream()
		fmt.Fprintln(o.stdout, frame.Text)
		o.lastByteNewline = true
		return false, nil
	case "image":
		fmt.Fprintf(o.stderr, "[image: %s, %d base64 bytes]\n", frame.MIMEType, len(frame.Data))
		return false, nil
	case "error":
		if frame.Text == "" {
			frame.Text = "server error"
		}
		return false, errors.New(frame.Text)
	case "thinking_end":
		o.finishStream()
		return true, nil
	default:
		return false, nil
	}
}

func (o *outputState) writeStream(text string) {
	if text == "" {
		return
	}
	fmt.Fprint(o.stdout, text)
	o.streamOpen = true
	o.lastByteNewline = strings.HasSuffix(text, "\n")
}

func (o *outputState) finishStream() {
	if !o.streamOpen {
		return
	}
	if !o.lastByteNewline {
		fmt.Fprintln(o.stdout)
	}
	o.streamOpen = false
	o.lastByteNewline = true
}
