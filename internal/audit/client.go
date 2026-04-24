package audit

import (
	"context"
	"time"

	"github.com/Blink-Build-Studios/little-tyke/internal/ollama"
)

// Client wraps an ollama.Client and logs every request/response.
type Client struct {
	inner  *ollama.Client
	logger *Logger
}

// NewClient wraps an Ollama client with audit logging.
func NewClient(inner *ollama.Client, logger *Logger) *Client {
	return &Client{inner: inner, logger: logger}
}

// Inner returns the underlying Ollama client for operations that don't need auditing.
func (c *Client) Inner() *ollama.Client {
	return c.inner
}

// Chat sends a non-streaming chat request and logs the full exchange.
func (c *Client) Chat(ctx context.Context, req *ollama.ChatRequest) (*ollama.ChatResponse, error) {
	id := NewID()
	start := time.Now()

	resp, err := c.inner.Chat(ctx, req)

	entry := Entry{
		ID:         id,
		Caller:     CallerFrom(ctx),
		Model:      req.Model,
		Request:    req,
		DurationMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		entry.Error = err.Error()
	} else {
		entry.Response = resp
	}
	c.logger.LogRequest(entry)

	return resp, err
}

// ChatStream sends a streaming chat request and logs the full exchange.
// The final chunk (with Done=true) contains timing data and is captured for the log.
func (c *Client) ChatStream(ctx context.Context, req *ollama.ChatRequest, onChunk func(*ollama.ChatResponse) error) error {
	id := NewID()
	start := time.Now()

	var finalChunk *ollama.ChatResponse
	var fullContent string

	err := c.inner.ChatStream(ctx, req, func(chunk *ollama.ChatResponse) error {
		fullContent += chunk.Message.Content
		if chunk.Done {
			finalChunk = chunk
		}
		return onChunk(chunk)
	})

	entry := Entry{
		ID:         id,
		Caller:     CallerFrom(ctx),
		Model:      req.Model,
		Request:    req,
		DurationMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		entry.Error = err.Error()
	} else if finalChunk != nil {
		entry.Response = map[string]any{
			"content":               fullContent,
			"done":                  true,
			"total_duration":        finalChunk.TotalDuration,
			"load_duration":         finalChunk.LoadDuration,
			"prompt_eval_count":     finalChunk.PromptEvalCount,
			"prompt_eval_duration":  finalChunk.PromptEvalDuration,
			"eval_count":            finalChunk.EvalCount,
			"eval_duration":         finalChunk.EvalDuration,
		}
	}
	c.logger.LogRequest(entry)

	return err
}
