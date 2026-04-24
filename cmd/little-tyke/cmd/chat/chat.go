package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var Cmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive chat REPL (hits the local HTTP API end-to-end)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return run()
	},
}

func init() {
	flags := Cmd.Flags()

	flags.String("url", "http://localhost:8081", "little-tyke API base URL")
	_ = viper.BindPFlag("chat_url", flags.Lookup("url"))

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

func run() error {
	baseURL := viper.GetString("chat_url")

	// Quick health check
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		return fmt.Errorf("cannot reach little-tyke at %s — is the server running? (make run): %w", baseURL, err)
	}
	_ = resp.Body.Close()

	fmt.Printf("Connected to %s\n", baseURL)
	fmt.Println("Type your message and press Enter. /quit to exit, /clear to reset history.")
	fmt.Println()

	var history []message

	if sys := viper.GetString("chat_system"); sys != "" {
		history = append(history, message{Role: "system", Content: sys})
	}

	scanner := bufio.NewScanner(os.Stdin)
	// Allow long pastes
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
			Model:    "default",
			Messages: history,
			Stream:   true,
		})

		req, err := http.NewRequest("POST", baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
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
