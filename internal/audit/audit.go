package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config controls audit log behavior.
type Config struct {
	Enabled    bool
	Dir        string
	MaxSizeMB  int
	MaxAgeDays int
	MaxBackups int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Enabled:    true,
		Dir:        filepath.Join(home, ".little-tyke", "audit"),
		MaxSizeMB:  50,
		MaxAgeDays: 30,
		MaxBackups: 5,
	}
}

// Logger is a dedicated audit logger backed by a rotating file.
type Logger struct {
	log *logrus.Logger
}

// New creates an audit logger. Returns a no-op logger if disabled.
func New(cfg Config) (*Logger, error) {
	l := logrus.New()
	l.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	l.SetLevel(logrus.InfoLevel)

	if !cfg.Enabled {
		l.SetOutput(os.NewFile(0, os.DevNull))
		return &Logger{log: l}, nil
	}

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, err
	}

	l.SetOutput(&lumberjack.Logger{
		Filename:   filepath.Join(cfg.Dir, "ollama.jsonl"),
		MaxSize:    cfg.MaxSizeMB,
		MaxAge:     cfg.MaxAgeDays,
		MaxBackups: cfg.MaxBackups,
		Compress:   true,
	})

	return &Logger{log: l}, nil
}

// LogRequest records a full Ollama request/response pair.
func (a *Logger) LogRequest(entry Entry) {
	fields := logrus.Fields{
		"id":          entry.ID,
		"caller":      entry.Caller,
		"model":       entry.Model,
		"request":     entry.Request,
		"duration_ms": entry.DurationMS,
	}
	if entry.Response != nil {
		fields["response"] = entry.Response
	}
	if entry.Error != "" {
		fields["error"] = entry.Error
		a.log.WithFields(fields).Error("ollama request failed")
	} else {
		a.log.WithFields(fields).Info("ollama request")
	}
}

// Entry is a single audit log record.
type Entry struct {
	ID         string
	Caller     string
	Model      string
	Request    any
	Response   any
	DurationMS int64
	Error      string
}

// NewID generates a short random request ID.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

type contextKey string

const callerKey contextKey = "audit_caller"

// WithCaller attaches a caller identifier to the context.
func WithCaller(ctx context.Context, caller string) context.Context {
	return context.WithValue(ctx, callerKey, caller)
}

// CallerFrom extracts the caller identifier from the context.
func CallerFrom(ctx context.Context) string {
	if v, ok := ctx.Value(callerKey).(string); ok {
		return v
	}
	return "unknown"
}
