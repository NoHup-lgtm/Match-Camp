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
	"matchcamp/internal/storage"
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

	redisClient := database.OpenRedis(database.RedisConfig{
		URL:      cfg.RedisURL,
		Password: cfg.RedisPassword,
		TLS:      cfg.RedisTLS,
	})
	defer redisClient.Close()

	objectStore, err := storage.New(ctx, storage.Config{
		Driver:          cfg.StorageDriver,
		LocalDir:        cfg.UploadDir,
		LocalPublicBase: cfg.PublicBaseURL,
		R2Endpoint:      cfg.R2Endpoint,
		R2Bucket:        cfg.R2Bucket,
		R2AccessKeyID:   cfg.R2AccessKeyID,
		R2SecretKey:     cfg.R2SecretAccessKey,
		R2PublicBaseURL: cfg.R2PublicBaseURL,
	})
	if err != nil {
		log.Error("open object storage", "error", err)
		os.Exit(1)
	}

	app := server.New(server.Config{
		DB:                   db,
		Redis:                redisClient,
		Storage:              objectStore,
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
		AllowedOrigins:       cfg.AllowedOrigins,
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
