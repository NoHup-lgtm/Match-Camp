package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"matchcamp/internal/config"
	"matchcamp/internal/database"
	"matchcamp/internal/server"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	redisClient := database.OpenRedis(cfg.RedisURL)
	defer redisClient.Close()

	app := server.New(server.Config{
		DB:                   db,
		Redis:                redisClient,
		Log:                  log,
		SessionCookieName:    cfg.SessionCookieName,
		SessionCookieSecure:  cfg.SessionCookieSecure,
		AllowedEmailDomains:  cfg.AllowedEmailDomains,
		GoogleClientID:       cfg.GoogleClientID,
		GoogleClientSecret:   cfg.GoogleClientSecret,
		GoogleRedirectURL:    cfg.GoogleRedirectURL,
		PublicBaseURL:        cfg.PublicBaseURL,
		UploadDir:            cfg.UploadDir,
		MaxProfilePhotoBytes: cfg.MaxProfilePhotoBytes,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "error", err)
		os.Exit(1)
	}
}
