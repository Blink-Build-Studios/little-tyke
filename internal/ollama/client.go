package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

// Client communicates with a local Ollama instance.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an Ollama client pointing at the given base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 0, // No timeout — model pulls and generation can take a while
		},
	}
}

// --- Health ---

// Ping checks if Ollama is reachable.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", c.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}
	return nil
}

// --- Model management ---

// LocalModel represents a model available locally in Ollama.
type LocalModel struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

// ListModels returns all locally available models.
func (c *Client) ListModels(ctx context.Context) ([]LocalModel, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Models []LocalModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Models, nil
}

// HasModel checks if a model is already pulled locally.
func (c *Client) HasModel(ctx context.Context, tag string) (bool, error) {
	models, err := c.ListModels(ctx)
	if err != nil {
		return false, err
	}
	for _, m := range models {
		if m.Name == tag || m.Name == tag+":latest" {
			return true, nil
		}
	}
	return false, nil
}

// PullModel downloads a model. Blocks until complete, logging progress.
func (c *Client) PullModel(ctx context.Context, tag string) error {
	body, _ := json.Marshal(map[string]any{"name": tag, "stream": true})
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("pull request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pull failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// Stream pull progress
	decoder := json.NewDecoder(resp.Body)
	lastLog := time.Now()
	for {
		var progress struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
		}
		if err := decoder.Decode(&progress); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		// Log progress every 5 seconds to avoid spam
		if time.Since(lastLog) > 5*time.Second || progress.Status == "success" {
			if progress.Total > 0 {
				pct := float64(progress.Completed) / float64(progress.Total) * 100
				log.WithFields(log.Fields{
					"status":   progress.Status,
					"progress": fmt.Sprintf("%.1f%%", pct),
				}).Info("pulling model")
			} else {
				log.WithField("status", progress.Status).Info("pulling model")
			}
			lastLog = time.Now()
		}

		if progress.Status == "success" {
			return nil
		}
	}
	return nil
}

// --- Chat completion (Ollama native API) ---

// ChatRequest is an Ollama /api/chat request.
type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	Tools     []any         `json:"tools,omitempty"`
	Options   *Options      `json:"options,omitempty"`
	KeepAlive string        `json:"keep_alive,omitempty"`
}

// ChatMessage is a single message in a chat.
type ChatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a tool/function call from the model.
type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the function details in a tool call.
type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Options are model inference options.
type Options struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	NumPredict  *int     `json:"num_predict,omitempty"`
	NumCtx      *int     `json:"num_ctx,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

// ChatResponse is a non-streaming Ollama /api/chat response.
type ChatResponse struct {
	Model     string      `json:"model"`
	Message   ChatMessage `json:"message"`
	Done      bool        `json:"done"`
	CreatedAt string      `json:"created_at"`

	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

// WarmModel forces Ollama to load the model into memory by sending a minimal request.
func (c *Client) WarmModel(ctx context.Context, model string) error {
	_, err := c.Chat(ctx, &ChatRequest{
		Model:    model,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Options:  &Options{NumPredict: intPtr(1)},
	})
	return err
}

func intPtr(v int) *int { return &v }

// Chat sends a non-streaming chat request to Ollama.
func (c *Client) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("chat failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}
	return &chatResp, nil
}

// ChatStream sends a streaming chat request and calls the callback for each chunk.
func (c *Client) ChatStream(ctx context.Context, req *ChatRequest, onChunk func(*ChatResponse) error) error {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("chat stream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chat stream failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk ChatResponse
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := onChunk(&chunk); err != nil {
			return err
		}
		if chunk.Done {
			return nil
		}
	}
}
