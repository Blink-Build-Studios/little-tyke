# little-tyke

Self-hosted Gemma 4 model server with OpenAI-compatible API, backed by Ollama.

## Build & Test

```bash
make build          # Build binary to build/little-tyke
make test           # Run all tests
make lint           # Run golangci-lint
make vet            # Run go vet
make tidy           # Tidy go.mod
make run            # Build and run
make docker-build   # Build Docker image
```

## Project Structure

```
cmd/little-tyke/           CLI entrypoint (cobra commands)
internal/hardware/         Hardware detection and model selection
internal/ollama/           Ollama HTTP client
internal/proxy/            OpenAI-compatible API proxy handler
internal/logging/          Logrus JSON logging setup
internal/monitoring/       Prometheus metrics and pprof
internal/sentry/           Sentry error reporting
docker/                    Dockerfile
scripts/                   Setup script
```

## Key Design Decisions

- Ollama runs on the host (not in Docker) for Metal GPU acceleration
- Model is auto-selected at startup based on available memory minus 8GB headroom
- Apple Silicon prefers MLX bf16 variants; other platforms prefer quantized variants
- OpenAI `/v1/chat/completions` format is translated to/from Ollama's native `/api/chat`
- Supports both streaming (SSE) and non-streaming responses
