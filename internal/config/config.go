package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	Provider       string // "gemini" or "claude"
	GeminiKey      string
	GeminiModel    string
	AnthropicKey   string
	AnthropicModel string

	TelegramToken   string
	TelegramAllowed map[int64]bool

	LokiURL string
	PromURL string
	Loc     *time.Location

	HTTPAddr  string
	WebOrigin string
	WebToken  string
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func Load() (*Config, error) {
	c := &Config{
		Provider:        strings.ToLower(env("LLM_PROVIDER", "gemini")),
		GeminiKey:       env("GEMINI_API_KEY", ""),
		GeminiModel:     env("GEMINI_MODEL", "gemini-2.5-flash"),
		AnthropicKey:    env("ANTHROPIC_API_KEY", ""),
		AnthropicModel:  env("ANTHROPIC_MODEL", "claude-sonnet-5-5"),
		TelegramToken:   env("TELEGRAM_BOT_TOKEN", ""),
		TelegramAllowed: map[int64]bool{},
		LokiURL:         strings.TrimRight(env("LOKI_URL", "http://localhost:3100"), "/"),
		PromURL:         strings.TrimRight(env("PROMETHEUS_URL", "http://localhost:9090"), "/"),
		HTTPAddr:        env("HTTP_ADDR", ":8081"),
		WebOrigin:       env("WEB_ORIGIN", "http://localhost:5173"),
		WebToken:        env("WEB_API_TOKEN", ""),
	}

	if c.Provider == "anthropic" {
		c.Provider = "claude"
	}
	switch c.Provider {
	case "gemini":
		if c.GeminiKey == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=gemini but GEMINI_API_KEY is empty (see .env)")
		}
	case "claude":
		if c.AnthropicKey == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=claude but ANTHROPIC_API_KEY is empty (see .env)")
		}
	default:
		return nil, fmt.Errorf("LLM_PROVIDER must be gemini or claude, got %q", c.Provider)
	}

	for _, s := range strings.Split(env("TELEGRAM_ALLOWED_USER_IDS", ""), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("TELEGRAM_ALLOWED_USER_IDS: %q is not a number", s)
		}
		c.TelegramAllowed[id] = true
	}

	loc, err := time.LoadLocation(env("TIMEZONE", "Asia/Jakarta"))
	if err != nil {
		return nil, fmt.Errorf("TIMEZONE: %w", err)
	}
	c.Loc = loc
	return c, nil
}
