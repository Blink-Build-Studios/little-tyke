package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/Blink-Build-Studios/little-tyke/internal/hardware"
	"github.com/Blink-Build-Studios/little-tyke/internal/logging"
	"github.com/Blink-Build-Studios/little-tyke/internal/ollama"
	"github.com/Blink-Build-Studios/little-tyke/internal/proxy"
)

// ANSI color codes
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

const maxHistoryTurns = 20

var Cmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive chat REPL (starts the server in-process and tests end-to-end)",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		logging.Setup(map[string]string{"service": "little-tyke"})
		return logging.SetLevel("warn")
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd.Context())
	},
}

func init() {
	flags := Cmd.Flags()

	flags.String("ollama-url", "http://localhost:11434", "Ollama API base URL")
	_ = viper.BindPFlag("ollama_url", flags.Lookup("ollama-url"))

	flags.String("model", "", "override model tag")
	_ = viper.BindPFlag("model", flags.Lookup("model"))

	flags.Bool("fast", false, "use the smallest/fastest model")
	_ = viper.BindPFlag("chat_fast", flags.Lookup("fast"))

	flags.String("system", "", "system prompt")
	_ = viper.BindPFlag("chat_system", flags.Lookup("system"))

	flags.Int("history", maxHistoryTurns, "max conversation turns to keep (0 = unlimited)")
	_ = viper.BindPFlag("chat_history", flags.Lookup("history"))

	flags.Bool("cot", false, "enforce chain-of-thought structured JSON output")
	_ = viper.BindPFlag("chat_cot", flags.Lookup("cot"))
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []message       `json:"messages"`
	Stream         bool            `json:"stream"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
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

type delta struct {
	Content string `json:"content,omitempty"`
}

type choice struct {
	Delta        delta  `json:"delta"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type streamChunk struct {
	Choices []choice `json:"choices"`
}

func status(icon, msg string) {
	fmt.Printf("  %s %s\n", icon, msg)
}

func run(ctx context.Context) error {
	ollamaURL := viper.GetString("ollama_url")
	client := ollama.NewClient(ollamaURL)
	maxTurns := viper.GetInt("chat_history")
	cotMode := viper.GetBool("chat_cot")

	fmt.Println()
	fmt.Printf("  %s%slittle-tyke%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("  %s─────────────────────────────────%s\n", colorDim, colorReset)

	status(colorYellow+"*"+colorReset, "Connecting to Ollama...")
	if err := client.Ping(ctx); err != nil {
		status(colorRed+"x"+colorReset, "Failed to connect")
		return fmt.Errorf("cannot reach Ollama at %s — is it running? (brew install ollama && ollama serve): %w", ollamaURL, err)
	}

	modelTag := viper.GetString("model")
	if modelTag == "" {
		info := hardware.Detect()
		var sel hardware.ModelSelection
		if viper.GetBool("chat_fast") {
			sel = hardware.FastModel(info)
		} else {
			sel = hardware.SelectModel(info)
		}
		modelTag = sel.Tag
		status(colorGreen+"+"+colorReset, fmt.Sprintf("Model: %s%s%s", colorBold, sel.DisplayName, colorReset))
		status(" ", fmt.Sprintf("%s%s%s", colorDim, sel.Reason, colorReset))
	} else {
		status(colorGreen+"+"+colorReset, fmt.Sprintf("Model: %s%s%s", colorBold, modelTag, colorReset))
	}

	has, err := client.HasModel(ctx, modelTag)
	if err != nil {
		return fmt.Errorf("checking model: %w", err)
	}
	if !has {
		status(colorYellow+"*"+colorReset, fmt.Sprintf("Pulling %s (this may take a while)...", modelTag))
		if err := client.PullModel(ctx, modelTag); err != nil {
			return fmt.Errorf("pulling model: %w", err)
		}
		status(colorGreen+"+"+colorReset, "Pull complete")
	}

	status(colorYellow+"*"+colorReset, "Loading model into GPU memory...")
	if err := client.WarmModel(ctx, modelTag); err != nil {
		status(colorRed+"!"+colorReset, "Warmup failed (first message may be slow)")
	} else {
		status(colorGreen+"+"+colorReset, "Model warm and ready")
	}

	// Start the HTTP server on a random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	addr := listener.Addr().String()

	handler := proxy.NewHandler(client, modelTag,
		proxy.WithKeepAlive("-1s"),
		proxy.WithDefaultMaxTokens(2048),
		proxy.WithNumCtx(4096),
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handler.ServeHTTP)
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

	baseURL := "http://" + addr

	fmt.Println()
	fmt.Printf("  %sCommands: /clear /quit%s\n", colorDim, colorReset)
	if maxTurns > 0 {
		fmt.Printf("  %sHistory: last %d turns%s\n", colorDim, maxTurns, colorReset)
	}
	if cotMode {
		fmt.Printf("  %sMode: chain-of-thought (structured JSON)%s\n", colorDim, colorReset)
	}
	fmt.Printf("  %s─────────────────────────────────%s\n", colorDim, colorReset)
	fmt.Println()

	var history []message
	var systemMsg *message

	sys := viper.GetString("chat_system")
	if sys == "" {
		if cotMode {
			sys = "Think step by step. Always respond with valid JSON matching the required schema. Put your reasoning in the \"thinking\" field and your final answer in the \"response\" field."
		} else {
			sys = "Be concise and direct. Avoid filler words and unnecessary preamble."
		}
	}
	sm := message{Role: "system", Content: sys}
	systemMsg = &sm

	var cotFormat *responseFormat
	if cotMode {
		t := true
		cotFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchema{
				Name:   "cot_response",
				Strict: &t,
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"thinking": map[string]any{
							"type":        "string",
							"description": "Step-by-step reasoning process",
						},
						"response": map[string]any{
							"type":        "string",
							"description": "Final answer to the user",
						},
					},
					"required":             []string{"thinking", "response"},
					"additionalProperties": false,
				},
			},
		}
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Printf("  %s%syou >%s ", colorBold, colorGreen, colorReset)
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "/quit" || input == "/exit" {
			fmt.Printf("\n  %sGoodbye!%s\n\n", colorDim, colorReset)
			break
		}
		if input == "/clear" {
			history = history[:0]
			fmt.Printf("  %s(history cleared)%s\n\n", colorDim, colorReset)
			continue
		}

		history = append(history, message{Role: "user", Content: input})

		// Sliding window: keep system prompt + last N turns (1 turn = user + assistant)
		sendMessages := trimHistory(systemMsg, history, maxTurns)

		cr := chatRequest{
			Model:          modelTag,
			Messages:       sendMessages,
			Stream:         !cotMode,
			ResponseFormat: cotFormat,
		}
		reqBody, _ := json.Marshal(cr)

		req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
		if err != nil {
			fmt.Printf("  %s%serror: %v%s\n\n", colorBold, colorRed, err, colorReset)
			history = history[:len(history)-1]
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("  %s%serror: %v%s\n\n", colorBold, colorRed, err, colorReset)
			history = history[:len(history)-1]
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			fmt.Printf("  %s%serror (HTTP %d): %s%s\n\n", colorBold, colorRed, resp.StatusCode, string(body), colorReset)
			history = history[:len(history)-1]
			continue
		}

		start := time.Now()
		var fullContent string

		if cotMode {
			fullContent = handleCoTResponse(resp)
		} else {
			fullContent = handleStreamResponse(resp)
		}

		elapsed := time.Since(start)
		fmt.Printf("  %s%.1fs%s\n\n", colorDim, elapsed.Seconds(), colorReset)

		history = append(history, message{Role: "assistant", Content: fullContent})
	}

	return nil
}

func handleStreamResponse(resp *http.Response) string {
	fmt.Printf("  %s%sthinking...%s", colorDim, colorYellow, colorReset)
	var full strings.Builder
	firstToken := true

	sseScanner := bufio.NewScanner(resp.Body)
	for sseScanner.Scan() {
		line := strings.TrimSpace(sseScanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
			text := chunk.Choices[0].Delta.Content
			if text != "" && firstToken {
				fmt.Printf("\r  %s%s  >%s ", colorBold, colorBlue, colorReset)
				firstToken = false
			}
			fmt.Print(text)
			full.WriteString(text)
		}
	}
	_ = resp.Body.Close()
	if firstToken {
		fmt.Printf("\r  %s%s  >%s ", colorBold, colorBlue, colorReset)
	}
	fmt.Println()
	return full.String()
}

type nonStreamResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type cotResponse struct {
	Thinking string `json:"thinking"`
	Response string `json:"response"`
}

func handleCoTResponse(resp *http.Response) string {
	fmt.Printf("  %s%sthinking...%s", colorDim, colorYellow, colorReset)

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	var apiResp nonStreamResponse
	if err := json.Unmarshal(body, &apiResp); err != nil || len(apiResp.Choices) == 0 {
		fmt.Printf("\r  %s%serror parsing response%s\n", colorBold, colorRed, colorReset)
		return ""
	}

	content := apiResp.Choices[0].Message.Content

	var cot cotResponse
	if err := json.Unmarshal([]byte(content), &cot); err != nil {
		// Model returned something but not valid CoT JSON — show raw
		fmt.Printf("\r  %s%s  >%s %s\n", colorBold, colorBlue, colorReset, content)
		return content
	}

	// Pretty print
	fmt.Printf("\r  %s%sthinking:%s %s%s%s\n", colorDim, colorYellow, colorReset, colorDim, cot.Thinking, colorReset)
	fmt.Printf("  %s%s  >%s %s\n", colorBold, colorBlue, colorReset, cot.Response)
	return content
}

// trimHistory returns messages to send: system prompt (if any) + last maxTurns*2 messages.
// If maxTurns is 0, all messages are sent.
func trimHistory(systemMsg *message, history []message, maxTurns int) []message {
	var msgs []message
	if systemMsg != nil {
		msgs = append(msgs, *systemMsg)
	}

	if maxTurns > 0 {
		maxMessages := maxTurns * 2
		if len(history) > maxMessages {
			msgs = append(msgs, history[len(history)-maxMessages:]...)
		} else {
			msgs = append(msgs, history...)
		}
	} else {
		msgs = append(msgs, history...)
	}

	return msgs
}
