package config

import (
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr            string
	DatabaseURL         string
	RedisURL            string
	LogLevel            slog.Level
	SessionCookieName   string
	SessionCookieSecure bool
	AllowedEmailDomains []string
	GoogleClientID      string
	GoogleClientSecret  string
	GoogleRedirectURL   string
}

func Load() Config {
	return Config{
		HTTPAddr:            env("HTTP_ADDR", ":8080"),
		DatabaseURL:         env("DATABASE_URL", "postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable"),
		RedisURL:            env("REDIS_URL", "redis://localhost:6379/0"),
		LogLevel:            parseLogLevel(env("LOG_LEVEL", "info")),
		SessionCookieName:   env("SESSION_COOKIE_NAME", "matchcamp_session"),
		SessionCookieSecure: envBool("SESSION_COOKIE_SECURE", false),
		AllowedEmailDomains: splitCSV(env("ALLOWED_EMAIL_DOMAINS", "")),
		GoogleClientID:      env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:  env("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:   env("GOOGLE_REDIRECT_URL", "http://localhost:8080/v1/auth/google/callback"),
	}
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes"
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseLogLevel(value string) slog.Level {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
