# little-tyke

Self-hosted Gemma 4 model server with an OpenAI-compatible API. Auto-detects hardware and selects the best model variant for your machine.

## Quick Start

```bash
# Install Ollama (required — runs natively for Metal GPU acceleration)
brew install ollama
ollama serve

# Build and run
make run
```

The server starts at `http://localhost:8081` with:
- `POST /v1/chat/completions` — OpenAI-compatible chat endpoint (streaming + non-streaming)
- `GET /v1/models` — List available models
- `GET /healthz` — Health check

## How It Works

1. Detects your hardware (OS, arch, memory)
2. Selects the best Gemma 4 variant that fits in memory (with 8GB headroom)
3. Pulls the model via Ollama if not already downloaded
4. Proxies OpenAI-format requests to Ollama's native API

### Model Selection

| Machine | Model |
|---------|-------|
| Apple Silicon 64GB+ | Gemma 4 27B (MLX bf16) |
| Apple Silicon 24GB+ | Gemma 4 E4B (MLX bf16) |
| Apple Silicon 16GB | Gemma 4 E2B (Q4_K_M) |
| Linux/Other 28GB+ | Gemma 4 31B (Q4_K_M) |
| Linux/Other 16GB+ | Gemma 4 E4B (Q4_K_M) |

Override with `--model` flag or `LITTLE_TYKE_MODEL` env var.

## Configuration

All flags can be set via environment variables with `LITTLE_TYKE_` prefix.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--addr` | `LITTLE_TYKE_ADDR` | `:8081` | API listen address |
| `--ollama-url` | `LITTLE_TYKE_OLLAMA_URL` | `http://localhost:11434` | Ollama API URL |
| `--model` | `LITTLE_TYKE_MODEL` | (auto) | Override model tag |
| `--prometheus-enabled` | `LITTLE_TYKE_PROMETHEUS_ENABLED` | `true` | Enable metrics |
| `--prometheus-addr` | `LITTLE_TYKE_PROMETHEUS_ADDR` | `:9001` | Metrics listen address |

## Summarize

Summarize documents using tool calling and structured output. Supports PDF files (requires `pdftotext` from poppler) and plain text files.

```bash
# Summarize a PDF
make summarize ARGS="~/Documents/report.pdf"

# Use the smallest model for faster results
make summarize ARGS="--fast ~/Documents/report.pdf"

# Output raw JSON instead of formatted text
make summarize ARGS="--json ~/Documents/report.pdf"

# Combine flags
make summarize ARGS="--fast --json ~/Documents/report.pdf"
```

### Dependencies

PDF support requires `pdftotext`:

```bash
brew install poppler
```

## Docker

The Go server can run in Docker, but Ollama must run on the host for Metal GPU acceleration.

```bash
ollama serve                    # On host
docker compose up --build       # Connects to host Ollama via host.docker.internal
```

## Why Ollama on Host?

Docker on macOS runs in a Linux VM with no access to Metal GPU. Running Ollama natively gives ~30-40 tok/s vs ~5 tok/s in Docker. Ollama abstracts the GPU backend, so the same setup works on CUDA machines too.
