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

	flags.String("system", "", "system prompt")
	_ = viper.BindPFlag("chat_system", flags.Lookup("system"))
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
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

func run(ctx context.Context) error {
	ollamaURL := viper.GetString("ollama_url")
	client := ollama.NewClient(ollamaURL)

	fmt.Print("Connecting to Ollama... ")
	if err := client.Ping(ctx); err != nil {
		fmt.Println("failed")
		return fmt.Errorf("cannot reach Ollama at %s — is it running? (brew install ollama && ollama serve): %w", ollamaURL, err)
	}
	fmt.Println("ok")

	modelTag := viper.GetString("model")
	if modelTag == "" {
		info := hardware.Detect()
		sel := hardware.SelectModel(info)
		modelTag = sel.Tag
		fmt.Printf("Auto-selected model: %s (%s)\n", sel.DisplayName, sel.Reason)
	}

	has, err := client.HasModel(ctx, modelTag)
	if err != nil {
		return fmt.Errorf("checking model: %w", err)
	}
	if !has {
		fmt.Printf("Pulling %s (this may take a while)...\n", modelTag)
		if err := client.PullModel(ctx, modelTag); err != nil {
			return fmt.Errorf("pulling model: %w", err)
		}
		fmt.Println("Pull complete.")
	}

	fmt.Print("Loading model into GPU memory... ")
	if err := client.WarmModel(ctx, modelTag); err != nil {
		fmt.Println("failed (first message may be slow)")
	} else {
		fmt.Println("ready")
	}

	// Start the HTTP server on a random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	addr := listener.Addr().String()

	handler := proxy.NewHandler(client, modelTag)
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

	fmt.Printf("Model: %s\n", modelTag)
	fmt.Println("Type your message and press Enter. /quit to exit, /clear to reset history.")
	fmt.Println()

	var history []message

	if sys := viper.GetString("chat_system"); sys != "" {
		history = append(history, message{Role: "system", Content: sys})
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Print("you> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "/quit" || input == "/exit" {
			break
		}
		if input == "/clear" {
			history = history[:0]
			if sys := viper.GetString("chat_system"); sys != "" {
				history = append(history, message{Role: "system", Content: sys})
			}
			fmt.Println("(history cleared)")
			continue
		}

		history = append(history, message{Role: "user", Content: input})

		reqBody, _ := json.Marshal(chatRequest{
			Model:    modelTag,
			Messages: history,
			Stream:   true,
		})

		req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
		if err != nil {
			fmt.Printf("error: %v\n", err)
			history = history[:len(history)-1]
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			history = history[:len(history)-1]
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			fmt.Printf("error (HTTP %d): %s\n", resp.StatusCode, string(body))
			history = history[:len(history)-1]
			continue
		}

		fmt.Print("assistant> ")
		var full strings.Builder

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			line = strings.TrimSpace(line)

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					break
				}
				var chunk streamChunk
				if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
					text := chunk.Choices[0].Delta.Content
					fmt.Print(text)
					full.WriteString(text)
				}
			}

			if err != nil {
				break
			}
		}
		_ = resp.Body.Close()
		fmt.Println()

		history = append(history, message{Role: "assistant", Content: full.String()})
	}

	return nil
}
