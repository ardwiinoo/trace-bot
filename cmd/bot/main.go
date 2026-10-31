package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"

	"github.com/joho/godotenv"

	"tracebot/internal/agent"
	"tracebot/internal/config"
	"tracebot/internal/httpapi"
	"tracebot/internal/llm"
	"tracebot/internal/telegram"
	"tracebot/internal/tools"
)

func main() {
	_ = godotenv.Load() // optional: docker compose injects env itself

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	var client llm.Client
	switch cfg.Provider {
	case "claude":
		client = llm.NewClaude(cfg.AnthropicKey, cfg.AnthropicModel)
		slog.Info("llm", "provider", "claude", "model", cfg.AnthropicModel)
	default:
		client = llm.NewGemini(cfg.GeminiKey, cfg.GeminiModel)
		slog.Info("llm", "provider", "gemini", "model", cfg.GeminiModel)
	}

	registry := tools.NewRegistry(tools.Backends{LokiURL: cfg.LokiURL, PromURL: cfg.PromURL, Loc: cfg.Loc})
	ag := agent.New(client, registry, cfg.Loc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var wg sync.WaitGroup
	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				slog.Error(name, "err", err)
				stop()
			}
		}()
	}

	run("http", func() error {
		return httpapi.New(ag, cfg.WebOrigin, cfg.WebToken).Serve(ctx, cfg.HTTPAddr)
	})
	if cfg.TelegramToken != "" {
		run("telegram", func() error {
			return telegram.Run(ctx, cfg.TelegramToken, cfg.TelegramAllowed, ag)
		})
	} else {
		slog.Warn("TELEGRAM_BOT_TOKEN is empty: telegram adapter disabled, web API only")
	}
	wg.Wait()
}
