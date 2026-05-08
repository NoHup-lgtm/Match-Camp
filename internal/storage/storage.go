package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type ObjectStore interface {
	Put(ctx context.Context, key string, contentType string, data []byte) (string, error)
	Delete(ctx context.Context, key string) error
	KeyFromURL(rawURL string) (string, bool)
}

type Config struct {
	Driver          string
	LocalDir        string
	LocalPublicBase string
	R2Endpoint      string
	R2Bucket        string
	R2AccessKeyID   string
	R2SecretKey     string
	R2PublicBaseURL string
}

func New(ctx context.Context, cfg Config) (ObjectStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "local":
		return NewLocalStore(cfg.LocalDir, cfg.LocalPublicBase), nil
	case "r2":
		return NewR2Store(ctx, R2Config{
			Endpoint:      cfg.R2Endpoint,
			Bucket:        cfg.R2Bucket,
			AccessKeyID:   cfg.R2AccessKeyID,
			SecretKey:     cfg.R2SecretKey,
			PublicBaseURL: cfg.R2PublicBaseURL,
		})
	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.Driver)
	}
}

func ProfilePhotoKey(userID uuid.UUID, position int, ext string) (string, error) {
	if position < 0 || position > 3 {
		return "", fmt.Errorf("invalid profile photo position")
	}
	if ext == "" || strings.Contains(ext, "/") || strings.Contains(ext, "\\") || strings.Contains(ext, "..") {
		return "", fmt.Errorf("invalid profile photo extension")
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("profile-photos/%s/%d-%s%s", userID.String(), position, hex.EncodeToString(buf), ext), nil
}

func DetectImageExtension(data []byte) (contentType string, ext string, ok bool) {
	contentType = http.DetectContentType(data)
	switch contentType {
	case "image/jpeg":
		return contentType, ".jpg", true
	case "image/png":
		return contentType, ".png", true
	case "image/webp":
		return contentType, ".webp", true
	default:
		return contentType, "", false
	}
}

func safeLocalPath(root, key string) (string, error) {
	clean := path.Clean("/" + key)
	if clean == "/" || strings.Contains(clean, "..") {
		return "", fmt.Errorf("invalid object key")
	}
	full := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("object key escapes root")
	}
	return fullAbs, nil
}

func readSeek(data []byte) *bytes.Reader {
	return bytes.NewReader(data)
}
