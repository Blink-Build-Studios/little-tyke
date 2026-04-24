// Package proxy translates OpenAI-compatible chat completion requests
// to Ollama's native API and back.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/Blink-Build-Studios/little-tyke/internal/monitoring"
	"github.com/Blink-Build-Studios/little-tyke/internal/ollama"
)

// ChatClient is the interface for sending chat requests to Ollama.
type ChatClient interface {
	Chat(ctx context.Context, req *ollama.ChatRequest) (*ollama.ChatResponse, error)
	ChatStream(ctx context.Context, req *ollama.ChatRequest, onChunk func(*ollama.ChatResponse) error) error
}

// Handler serves OpenAI-compatible /v1/chat/completions requests.
type Handler struct {
	client          ChatClient
	model           string
	keepAlive       string
	defaultMaxToks  int
	numCtx          int
}

// HandlerOption configures a Handler.
type HandlerOption func(*Handler)

// WithKeepAlive sets the Ollama keep_alive duration (e.g. "30m", "-1" for forever).
func WithKeepAlive(d string) HandlerOption { return func(h *Handler) { h.keepAlive = d } }

// WithDefaultMaxTokens sets the default max tokens if the caller doesn't specify.
func WithDefaultMaxTokens(n int) HandlerOption { return func(h *Handler) { h.defaultMaxToks = n } }

// WithNumCtx sets the context window size.
func WithNumCtx(n int) HandlerOption { return func(h *Handler) { h.numCtx = n } }

// NewHandler creates a proxy handler for the given model.
func NewHandler(client ChatClient, model string, opts ...HandlerOption) *Handler {
	h := &Handler{client: client, model: model}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// --- OpenAI API types ---

// ChatCompletionRequest matches the OpenAI chat completion request format.
type ChatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	TopP           *float64        `json:"top_p,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	Stop           any             `json:"stop,omitempty"`
	Stream         bool            `json:"stream"`
	Tools          []Tool          `json:"tools,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat controls the output format of the model.
type ResponseFormat struct {
	Type       string      `json:"type"`                  // "text", "json_object", or "json_schema"
	JSONSchema *JSONSchema `json:"json_schema,omitempty"` // required when type is "json_schema"
}

// JSONSchema defines a structured output schema.
type JSONSchema struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema"`
	Strict      *bool  `json:"strict,omitempty"`
}

// Message is an OpenAI-format chat message.
type Message struct {
	Role       string          `json:"role"`
	Content    any             `json:"content"`             // string or []ContentPart
	ToolCalls  []OAIToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// OAIToolCall is an OpenAI-format tool call.
type OAIToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function OAIToolCallFunction `json:"function"`
}

// OAIToolCallFunction is the function details in an OpenAI tool call.
type OAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string, not parsed object
}

// Tool is an OpenAI-format tool definition.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function definition within a tool.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ChatCompletionResponse is an OpenAI-format response.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage tracks token consumption.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Streaming types ---

// ChatCompletionChunk is a streamed SSE chunk.
type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
}

// ChunkChoice is a single streamed choice.
type ChunkChoice struct {
	Index        int          `json:"index"`
	Delta        ChunkDelta   `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

// ChunkDelta is the incremental content in a stream chunk.
type ChunkDelta struct {
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []OAIToolCall `json:"tool_calls,omitempty"`
}

// ServeHTTP handles /v1/chat/completions.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"method not allowed","type":"invalid_request_error"}}`, http.StatusMethodNotAllowed)
		return
	}

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	start := time.Now()

	if req.Stream {
		h.handleStream(w, r, &req, start)
	} else {
		h.handleNonStream(w, r, &req, start)
	}
}

func (h *Handler) handleNonStream(w http.ResponseWriter, r *http.Request, req *ChatCompletionRequest, start time.Time) {
	ollamaReq := h.toOllamaRequest(req)

	resp, err := h.client.Chat(r.Context(), ollamaReq)
	if err != nil {
		log.WithError(err).Error("ollama chat failed")
		writeError(w, http.StatusBadGateway, "server_error", "model inference failed: "+err.Error())
		monitoring.RequestsTotal.WithLabelValues("POST", "/v1/chat/completions", "502").Inc()
		return
	}

	oaiResp := h.toOpenAIResponse(resp)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(oaiResp)

	elapsed := time.Since(start)
	monitoring.RequestsTotal.WithLabelValues("POST", "/v1/chat/completions", "200").Inc()
	monitoring.RequestDuration.WithLabelValues("POST", "/v1/chat/completions").Observe(elapsed.Seconds())
	recordOllamaMetrics(resp)

	log.WithFields(log.Fields{
		"model":               h.model,
		"prompt_tokens":       resp.PromptEvalCount,
		"completion_tokens":   resp.EvalCount,
		"duration_ms":         elapsed.Milliseconds(),
		"load_ms":             resp.LoadDuration / 1e6,
		"prompt_eval_ms":      resp.PromptEvalDuration / 1e6,
		"generation_ms":       resp.EvalDuration / 1e6,
		"prompt_tok_per_sec":  tokensPerSec(resp.PromptEvalCount, resp.PromptEvalDuration),
		"gen_tok_per_sec":     tokensPerSec(resp.EvalCount, resp.EvalDuration),
	}).Info("chat completion")
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request, req *ChatCompletionRequest, start time.Time) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ollamaReq := h.toOllamaRequest(req)
	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	firstChunk := true
	var firstTokenTime time.Time
	var finalChunk *ollama.ChatResponse

	err := h.client.ChatStream(r.Context(), ollamaReq, func(chunk *ollama.ChatResponse) error {
		oaiChunk := ChatCompletionChunk{
			ID:      chatID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   h.model,
			Choices: []ChunkChoice{
				{
					Index: 0,
					Delta: ChunkDelta{
						Content: chunk.Message.Content,
					},
				},
			},
		}

		if firstChunk {
			oaiChunk.Choices[0].Delta.Role = "assistant"
			firstChunk = false
			firstTokenTime = time.Now()
		}

		if chunk.Done {
			stop := "stop"
			oaiChunk.Choices[0].FinishReason = &stop
			oaiChunk.Choices[0].Delta.Content = ""
			finalChunk = chunk
		}

		data, _ := json.Marshal(oaiChunk)
		_, err := fmt.Fprintf(w, "data: %s\n\n", data)
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})

	if err != nil {
		log.WithError(err).Error("stream failed")
		return
	}

	// Send [DONE] marker
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()

	elapsed := time.Since(start)
	monitoring.RequestsTotal.WithLabelValues("POST", "/v1/chat/completions", "200").Inc()
	monitoring.RequestDuration.WithLabelValues("POST", "/v1/chat/completions").Observe(elapsed.Seconds())

	if !firstTokenTime.IsZero() {
		ttft := firstTokenTime.Sub(start).Seconds()
		monitoring.TimeToFirstToken.Observe(ttft)
	}

	logFields := log.Fields{
		"model":       h.model,
		"duration_ms": elapsed.Milliseconds(),
		"stream":      true,
	}

	if finalChunk != nil {
		recordOllamaMetrics(finalChunk)
		logFields["prompt_tokens"] = finalChunk.PromptEvalCount
		logFields["completion_tokens"] = finalChunk.EvalCount
		logFields["load_ms"] = finalChunk.LoadDuration / 1e6
		logFields["prompt_eval_ms"] = finalChunk.PromptEvalDuration / 1e6
		logFields["generation_ms"] = finalChunk.EvalDuration / 1e6
		logFields["prompt_tok_per_sec"] = tokensPerSec(finalChunk.PromptEvalCount, finalChunk.PromptEvalDuration)
		logFields["gen_tok_per_sec"] = tokensPerSec(finalChunk.EvalCount, finalChunk.EvalDuration)
		if !firstTokenTime.IsZero() {
			logFields["ttft_ms"] = firstTokenTime.Sub(start).Milliseconds()
		}
	}

	log.WithFields(logFields).Info("chat completion (stream)")
}

// toOllamaRequest converts an OpenAI request to Ollama format.
func (h *Handler) toOllamaRequest(req *ChatCompletionRequest) *ollama.ChatRequest {
	var messages []ollama.ChatMessage
	for _, m := range req.Messages {
		content := ""
		switch v := m.Content.(type) {
		case string:
			content = v
		case []any:
			// Handle array content (text parts)
			for _, part := range v {
				if p, ok := part.(map[string]any); ok {
					if p["type"] == "text" {
						if text, ok := p["text"].(string); ok {
							content += text
						}
					}
				}
			}
		}
		messages = append(messages, ollama.ChatMessage{
			Role:    m.Role,
			Content: content,
		})
	}

	ollamaReq := &ollama.ChatRequest{
		Model:     h.model,
		Messages:  messages,
		KeepAlive: h.keepAlive,
	}

	// Map response_format to Ollama's format field
	if req.ResponseFormat != nil {
		switch req.ResponseFormat.Type {
		case "json_object":
			ollamaReq.Format = "json"
		case "json_schema":
			if req.ResponseFormat.JSONSchema != nil {
				ollamaReq.Format = req.ResponseFormat.JSONSchema.Schema
			}
		}
		// "text" is the default — no format field needed
	}

	if req.Tools != nil {
		toolsJSON, _ := json.Marshal(req.Tools)
		var tools []any
		_ = json.Unmarshal(toolsJSON, &tools)
		ollamaReq.Tools = tools
	}

	// Build options — apply handler defaults, then caller overrides
	opts := &ollama.Options{}
	hasOpts := false

	if h.numCtx > 0 {
		opts.NumCtx = &h.numCtx
		hasOpts = true
	}
	if req.MaxTokens != nil {
		opts.NumPredict = req.MaxTokens
		hasOpts = true
	} else if h.defaultMaxToks > 0 {
		opts.NumPredict = &h.defaultMaxToks
		hasOpts = true
	}
	if req.Temperature != nil {
		opts.Temperature = req.Temperature
		hasOpts = true
	}
	if req.TopP != nil {
		opts.TopP = req.TopP
		hasOpts = true
	}
	if stops, ok := req.Stop.([]any); ok {
		for _, s := range stops {
			if str, ok := s.(string); ok {
				opts.Stop = append(opts.Stop, str)
			}
		}
		hasOpts = true
	} else if stop, ok := req.Stop.(string); ok {
		opts.Stop = []string{stop}
		hasOpts = true
	}

	if hasOpts {
		ollamaReq.Options = opts
	}

	return ollamaReq
}

// toOpenAIResponse converts an Ollama response to OpenAI format.
func (h *Handler) toOpenAIResponse(resp *ollama.ChatResponse) *ChatCompletionResponse {
	msg := Message{
		Role:    resp.Message.Role,
		Content: resp.Message.Content,
	}

	// Convert tool calls
	for i, tc := range resp.Message.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Function.Arguments)
		msg.ToolCalls = append(msg.ToolCalls, OAIToolCall{
			ID:   fmt.Sprintf("call_%d", i),
			Type: "function",
			Function: OAIToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: string(argsJSON),
			},
		})
	}

	finishReason := "stop"
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}

	return &ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   h.model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
		Usage: &Usage{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		},
	}
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
		},
	})
}

func nanosToSec(ns int64) float64 {
	return float64(ns) / 1e9
}

func tokensPerSec(count int, durationNanos int64) float64 {
	if durationNanos == 0 {
		return 0
	}
	return float64(count) / nanosToSec(durationNanos)
}

func recordOllamaMetrics(resp *ollama.ChatResponse) {
	if resp.TotalDuration > 0 {
		monitoring.TotalInferenceDuration.Observe(nanosToSec(resp.TotalDuration))
	}
	if resp.LoadDuration > 0 {
		monitoring.ModelLoadDuration.Observe(nanosToSec(resp.LoadDuration))
	}
	if resp.PromptEvalDuration > 0 {
		monitoring.PromptEvalDuration.Observe(nanosToSec(resp.PromptEvalDuration))
		if resp.PromptEvalCount > 0 {
			monitoring.PromptTokensPerSecond.Observe(tokensPerSec(resp.PromptEvalCount, resp.PromptEvalDuration))
		}
	}
	if resp.EvalDuration > 0 {
		monitoring.GenerationDuration.Observe(nanosToSec(resp.EvalDuration))
		if resp.EvalCount > 0 {
			monitoring.GenerationTokensPerSecond.Observe(tokensPerSec(resp.EvalCount, resp.EvalDuration))
		}
	}
	if resp.PromptEvalCount > 0 {
		monitoring.PromptTokensTotal.Add(float64(resp.PromptEvalCount))
	}
	if resp.EvalCount > 0 {
		monitoring.GeneratedTokensTotal.Add(float64(resp.EvalCount))
	}
}
