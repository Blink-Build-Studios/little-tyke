package summarize

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/Blink-Build-Studios/little-tyke/internal/audit"
	"github.com/Blink-Build-Studios/little-tyke/internal/hardware"
	"github.com/Blink-Build-Studios/little-tyke/internal/logging"
	"github.com/Blink-Build-Studios/little-tyke/internal/ollama"
	"github.com/Blink-Build-Studios/little-tyke/internal/proxy"
)

const (
	colorReset  = "\033[0m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorBlue   = "\033[34m"
)

var Cmd = &cobra.Command{
	Use:   "summarize <file>",
	Short: "Summarize a document using tool calling and structured output",
	Args:  cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		logging.Setup(map[string]string{"service": "little-tyke"})
		return logging.SetLevel("warn")
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd.Context(), args[0])
	},
}

func init() {
	flags := Cmd.Flags()

	flags.String("ollama-url", "http://localhost:11434", "Ollama API base URL")
	_ = viper.BindPFlag("ollama_url", flags.Lookup("ollama-url"))

	flags.String("model", "", "override model tag")
	_ = viper.BindPFlag("model", flags.Lookup("model"))

	flags.Bool("fast", false, "use the smallest/fastest model")
	_ = viper.BindPFlag("summarize_fast", flags.Lookup("fast"))
}

// --- API types ---

type message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolCallFunc `json:"function"`
}

type toolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []message       `json:"messages"`
	Stream         bool            `json:"stream"`
	Tools          []tool          `json:"tools,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type tool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type responseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *jsonSchema `json:"json_schema,omitempty"`
}

type jsonSchema struct {
	Name   string `json:"name"`
	Schema any    `json:"schema"`
	Strict *bool  `json:"strict,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
}

// --- Output schema ---

type documentSummary struct {
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	Synopsis   string    `json:"synopsis"`
	Principals []string  `json:"principals"`
	Sections   []section `json:"sections"`
	Takeaways  []string  `json:"takeaways"`
}

type section struct {
	Heading string   `json:"heading"`
	Summary string   `json:"summary"`
	Points  []string `json:"points"`
}

func status(icon, msg string) {
	fmt.Fprintf(os.Stderr, "  %s %s\n", icon, msg)
}

func run(ctx context.Context, filePath string) error {
	// Resolve file path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("file not found: %s", absPath)
	}

	ollamaURL := viper.GetString("ollama_url")
	client := ollama.NewClient(ollamaURL)

	fmt.Fprintln(os.Stderr)
	status(colorYellow+"*"+colorReset, "Connecting to Ollama...")
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("cannot reach Ollama: %w", err)
	}

	modelTag := viper.GetString("model")
	if modelTag == "" {
		info := hardware.Detect()
		var sel hardware.ModelSelection
		if viper.GetBool("summarize_fast") {
			sel = hardware.FastModel(info)
		} else {
			sel = hardware.SelectModel(info)
		}
		modelTag = sel.Tag
		status(colorGreen+"+"+colorReset, fmt.Sprintf("Model: %s%s%s", colorBold, sel.DisplayName, colorReset))
	}

	has, err := client.HasModel(ctx, modelTag)
	if err != nil {
		return fmt.Errorf("checking model: %w", err)
	}
	if !has {
		status(colorYellow+"*"+colorReset, fmt.Sprintf("Pulling %s...", modelTag))
		if err := client.PullModel(ctx, modelTag); err != nil {
			return fmt.Errorf("pulling model: %w", err)
		}
	}

	status(colorYellow+"*"+colorReset, "Warming model...")
	_ = client.WarmModel(ctx, modelTag)

	// Start in-process server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	auditLogger, _ := audit.New(audit.DefaultConfig())
	auditClient := audit.NewClient(client, auditLogger)

	handler := proxy.NewHandler(auditClient, modelTag,
		proxy.WithKeepAlive("-1s"),
		proxy.WithDefaultMaxTokens(4096),
		proxy.WithNumCtx(4096),
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		ctx := audit.WithCaller(r.Context(), "summarize")
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != http.ErrServerClosed {
			log.WithError(err).Error("http server error")
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	baseURL := "http://" + listener.Addr().String()

	status(colorYellow+"*"+colorReset, fmt.Sprintf("Summarizing %s%s%s...", colorBold, filepath.Base(filePath), colorReset))
	start := time.Now()

	summary, err := runToolLoop(ctx, baseURL, modelTag, absPath)
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	printSummary(summary)
	fmt.Fprintf(os.Stderr, "\n  %s%.1fs%s\n\n", colorDim, elapsed.Seconds(), colorReset)

	return nil
}

var readFileTool = tool{
	Type: "function",
	Function: toolFunction{
		Name:        "read_file",
		Description: "Read the contents of a file at the given path",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to read",
				},
			},
			"required": []string{"path"},
		},
	},
}

var summarySchema = &responseFormat{
	Type: "json_schema",
	JSONSchema: &jsonSchema{
		Name: "document_summary",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Title or filename of the document",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "Document type (e.g. code, config, report, notes, contract, readme)",
				},
				"synopsis": map[string]any{
					"type":        "string",
					"description": "One-paragraph summary of the document",
				},
				"principals": map[string]any{
					"type":        "array",
					"description": "Key people, organizations, systems, or entities mentioned",
					"items":       map[string]any{"type": "string"},
				},
				"sections": map[string]any{
					"type":        "array",
					"description": "Logical sections of the document",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"heading": map[string]any{"type": "string", "description": "Section heading or topic"},
							"summary": map[string]any{"type": "string", "description": "Brief summary of this section"},
							"points":  map[string]any{"type": "array", "description": "Key points", "items": map[string]any{"type": "string"}},
						},
						"required":             []string{"heading", "summary", "points"},
						"additionalProperties": false,
					},
				},
				"takeaways": map[string]any{
					"type":        "array",
					"description": "Top-level takeaways or action items",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required":             []string{"title", "type", "synopsis", "principals", "sections", "takeaways"},
			"additionalProperties": false,
		},
	},
}

func runToolLoop(ctx context.Context, baseURL, model, filePath string) (*documentSummary, error) {
	messages := []message{
		{
			Role:    "system",
			Content: "You are a document analysis tool. Use the read_file tool to read the document, then produce a structured summary. Be thorough but concise.",
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Summarize the document at: %s", filePath),
		},
	}

	for i := 0; i < 5; i++ { // max 5 tool-call rounds
		reqBody, _ := json.Marshal(chatRequest{
			Model:    model,
			Messages: messages,
			Stream:   false,
			Tools:    []tool{readFileTool},
		})

		req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		var apiResp chatResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			return nil, fmt.Errorf("parsing response: %w", err)
		}
		if len(apiResp.Choices) == 0 {
			return nil, fmt.Errorf("empty response from model")
		}

		choice := apiResp.Choices[0]

		// If no tool calls, this is the final response — now request structured output
		if choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) == 0 {
			// We have the file content in context, now request structured summary
			break
		}

		// Add assistant message with tool calls
		messages = append(messages, choice.Message)

		// Execute each tool call
		for _, tc := range choice.Message.ToolCalls {
			status(colorCyan+"~"+colorReset, fmt.Sprintf("reading %s", colorDim+extractPath(tc.Function.Arguments)+colorReset))

			result := executeToolCall(tc)
			messages = append(messages, message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	// Final request with structured output schema, no tools
	reqBody, _ := json.Marshal(chatRequest{
		Model:          model,
		Messages:       append(messages, message{Role: "user", Content: "Now produce the structured summary based on the file contents above."}),
		Stream:         false,
		ResponseFormat: summarySchema,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("final request failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var apiResp chatResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing final response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("empty final response")
	}

	content, ok := apiResp.Choices[0].Message.Content.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected content type in response")
	}

	var summary documentSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		return nil, fmt.Errorf("parsing summary JSON: %w\nraw: %s", err, content)
	}

	return &summary, nil
}

func executeToolCall(tc toolCall) string {
	if tc.Function.Name != "read_file" {
		return fmt.Sprintf("unknown tool: %s", tc.Function.Name)
	}

	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err)
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return fmt.Sprintf("error reading file: %v", err)
	}

	// Truncate large files
	const maxBytes = 50 * 1024
	if len(data) > maxBytes {
		return string(data[:maxBytes]) + "\n\n[truncated — file exceeds 50KB]"
	}

	return string(data)
}

func extractPath(argsJSON string) string {
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
		return filepath.Base(args.Path)
	}
	return "file"
}

func printSummary(s *documentSummary) {
	fmt.Println()
	fmt.Printf("  %s%s%s%s\n", colorBold, colorCyan, s.Title, colorReset)
	fmt.Printf("  %s%s%s\n", colorDim, s.Type, colorReset)
	fmt.Printf("  %s─────────────────────────────────%s\n", colorDim, colorReset)

	fmt.Printf("\n  %s%sSynopsis%s\n", colorBold, colorBlue, colorReset)
	fmt.Printf("  %s\n", wordWrap(s.Synopsis, 60, "  "))

	if len(s.Principals) > 0 {
		fmt.Printf("\n  %s%sPrincipals%s\n", colorBold, colorBlue, colorReset)
		for _, p := range s.Principals {
			fmt.Printf("  %s*%s %s\n", colorYellow, colorReset, p)
		}
	}

	if len(s.Sections) > 0 {
		fmt.Printf("\n  %s%sSections%s\n", colorBold, colorBlue, colorReset)
		for _, sec := range s.Sections {
			fmt.Printf("\n  %s%s%s%s\n", colorBold, colorGreen, sec.Heading, colorReset)
			fmt.Printf("  %s\n", wordWrap(sec.Summary, 60, "  "))
			for _, pt := range sec.Points {
				fmt.Printf("    %s-%s %s\n", colorDim, colorReset, pt)
			}
		}
	}

	if len(s.Takeaways) > 0 {
		fmt.Printf("\n  %s%sTakeaways%s\n", colorBold, colorBlue, colorReset)
		for _, t := range s.Takeaways {
			fmt.Printf("  %s>%s %s\n", colorYellow, colorReset, t)
		}
	}
}

func wordWrap(text string, width int, indent string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n"+indent)
}
