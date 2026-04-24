package monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// --- HTTP layer ---

	// RequestsTotal counts API requests by method, path, and status code.
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "little_tyke",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total number of HTTP requests.",
	}, []string{"method", "path", "status"})

	// RequestDuration observes total request duration in seconds.
	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "Total duration of HTTP requests in seconds.",
		Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"method", "path"})

	// --- LLM inference phases ---

	// ModelLoadDuration tracks time to load the model into memory (seconds).
	ModelLoadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "model_load_duration_seconds",
		Help:      "Time spent loading the model into GPU/CPU memory.",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60},
	})

	// PromptEvalDuration tracks prompt evaluation (prefill) time in seconds.
	PromptEvalDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "prompt_eval_duration_seconds",
		Help:      "Time spent evaluating the input prompt (prefill phase).",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	})

	// GenerationDuration tracks token generation (decode) time in seconds.
	GenerationDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "generation_duration_seconds",
		Help:      "Time spent generating output tokens (decode phase).",
		Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	})

	// TotalInferenceDuration tracks total Ollama-reported inference time in seconds.
	TotalInferenceDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "total_inference_duration_seconds",
		Help:      "Total inference duration as reported by Ollama (load + prompt eval + generation).",
		Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	})

	// TimeToFirstToken tracks time from request start to first generated token (seconds).
	TimeToFirstToken = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "time_to_first_token_seconds",
		Help:      "Time from request received to first token generated (streaming only).",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	})

	// --- Token metrics ---

	// PromptTokensTotal counts input tokens processed.
	PromptTokensTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "prompt_tokens_total",
		Help:      "Total input tokens evaluated.",
	})

	// GeneratedTokensTotal counts output tokens generated.
	GeneratedTokensTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "generated_tokens_total",
		Help:      "Total output tokens generated.",
	})

	// PromptTokensPerSecond tracks prompt evaluation throughput.
	PromptTokensPerSecond = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "prompt_tokens_per_second",
		Help:      "Prompt evaluation speed in tokens per second.",
		Buckets:   []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
	})

	// GenerationTokensPerSecond tracks token generation throughput.
	GenerationTokensPerSecond = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "little_tyke",
		Subsystem: "llm",
		Name:      "generation_tokens_per_second",
		Help:      "Token generation speed in tokens per second.",
		Buckets:   []float64{1, 5, 10, 15, 20, 30, 40, 50, 75, 100},
	})
)
