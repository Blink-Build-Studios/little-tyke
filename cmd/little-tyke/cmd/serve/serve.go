package serve

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/Blink-Build-Studios/little-tyke/internal/hardware"
	"github.com/Blink-Build-Studios/little-tyke/internal/logging"
	"github.com/Blink-Build-Studios/little-tyke/internal/monitoring"
	"github.com/Blink-Build-Studios/little-tyke/internal/ollama"
	"github.com/Blink-Build-Studios/little-tyke/internal/proxy"
	appsentry "github.com/Blink-Build-Studios/little-tyke/internal/sentry"
)

var Cmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the OpenAI-compatible API server",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		logging.Setup(map[string]string{"service": "little-tyke"})
		return logging.SetLevel(viper.GetString("log_level"))
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd.Context())
	},
}

func init() {
	flags := Cmd.Flags()

	flags.String("addr", ":8081", "API server listen address")
	_ = viper.BindPFlag("addr", flags.Lookup("addr"))

	flags.String("ollama-url", "http://localhost:11434", "Ollama API base URL")
	_ = viper.BindPFlag("ollama_url", flags.Lookup("ollama-url"))

	flags.String("model", "", "override model tag (e.g. 'gemma4:e4b-it-q4_K_M'). Auto-selects if empty.")
	_ = viper.BindPFlag("model", flags.Lookup("model"))

	flags.String("prometheus-addr", ":9001", "prometheus metrics listen address")
	_ = viper.BindPFlag("prometheus_addr", flags.Lookup("prometheus-addr"))

	flags.Bool("prometheus-enabled", true, "enable prometheus metrics server")
	_ = viper.BindPFlag("prometheus_enabled", flags.Lookup("prometheus-enabled"))

	flags.String("pprof-addr", ":9000", "pprof listen address")
	_ = viper.BindPFlag("pprof_addr", flags.Lookup("pprof-addr"))

	flags.Bool("pprof-enabled", false, "enable pprof server")
	_ = viper.BindPFlag("pprof_enabled", flags.Lookup("pprof-enabled"))

	flags.String("sentry-dsn", "", "Sentry DSN for error reporting")
	_ = viper.BindPFlag("sentry_dsn", flags.Lookup("sentry-dsn"))

	flags.String("sentry-environment", "", "Sentry environment (default: development)")
	_ = viper.BindPFlag("sentry_environment", flags.Lookup("sentry-environment"))
}

func run(ctx context.Context) error {
	// Sentry
	sentryEnabled, err := appsentry.Init(
		viper.GetString("sentry_dsn"),
		viper.GetString("sentry_environment"),
	)
	if err != nil {
		log.WithError(err).Warn("failed to initialize Sentry")
	}
	if sentryEnabled {
		defer appsentry.Flush(2 * time.Second)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 5)

	ollamaURL := viper.GetString("ollama_url")
	client := ollama.NewClient(ollamaURL)

	// --- Check Ollama is running ---
	log.WithField("url", ollamaURL).Info("connecting to Ollama")
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("cannot reach Ollama at %s — is it running? (brew install ollama && ollama serve): %w", ollamaURL, err)
	}
	log.Info("ollama connection OK")

	// --- Select model ---
	modelTag := viper.GetString("model")
	if modelTag == "" {
		info := hardware.Detect()
		selection := hardware.SelectModel(info)
		modelTag = selection.Tag
		log.WithFields(log.Fields{
			"model":  selection.DisplayName,
			"tag":    selection.Tag,
			"reason": selection.Reason,
		}).Info("auto-selected model")
	} else {
		log.WithField("model", modelTag).Info("using override model")
	}

	// --- Ensure model is pulled ---
	has, err := client.HasModel(ctx, modelTag)
	if err != nil {
		return fmt.Errorf("checking model availability: %w", err)
	}
	if !has {
		log.WithField("model", modelTag).Info("model not found locally, pulling (this may take a while)...")
		if err := client.PullModel(ctx, modelTag); err != nil {
			return fmt.Errorf("pulling model %s: %w", modelTag, err)
		}
		log.WithField("model", modelTag).Info("model pull complete")
	} else {
		log.WithField("model", modelTag).Info("model already available locally")
	}

	// --- Warm model into GPU memory ---
	log.WithField("model", modelTag).Info("warming model (loading into GPU memory)...")
	if err := client.WarmModel(ctx, modelTag); err != nil {
		log.WithError(err).Warn("model warmup failed (first request may be slow)")
	} else {
		log.WithField("model", modelTag).Info("model warm and ready")
	}

	// --- Prometheus ---
	if viper.GetBool("prometheus_enabled") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := monitoring.RunPrometheusServer(ctx, viper.GetString("prometheus_addr")); err != nil {
				errCh <- fmt.Errorf("prometheus server: %w", err)
			}
		}()
	}

	// --- pprof ---
	if viper.GetBool("pprof_enabled") {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := monitoring.RunPprofServer(ctx, viper.GetString("pprof_addr")); err != nil {
				errCh <- fmt.Errorf("pprof server: %w", err)
			}
		}()
	}

	// --- HTTP server ---
	addr := viper.GetString("addr")
	handler := proxy.NewHandler(client, modelTag)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","model":"%s"}`, modelTag)
	})
	mux.HandleFunc("/v1/chat/completions", handler.ServeHTTP)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"object":"list","data":[{"id":"%s","object":"model","owned_by":"google"}]}`, modelTag)
	})

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		log.Info("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.WithFields(log.Fields{
			"addr":  addr,
			"model": modelTag,
		}).Info("little-tyke ready — OpenAI-compatible API at /v1/chat/completions")
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	wg.Wait()
	log.Info("shutdown complete")
	return nil
}
