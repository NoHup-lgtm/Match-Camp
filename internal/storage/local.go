package storage

import (
	"context"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type LocalStore struct {
	root          string
	publicBaseURL string
}

func NewLocalStore(root, publicBaseURL string) *LocalStore {
	return &LocalStore{
		root:          root,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
	}
}

func (s *LocalStore) Put(_ context.Context, key string, _ string, data []byte) (string, error) {
	fullPath, err := safeLocalPath(s.root, key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return "", err
	}
	assetPath := "/uploads/" + path.Clean(key)
	if s.publicBaseURL == "" {
		return assetPath, nil
	}
	return s.publicBaseURL + assetPath, nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	fullPath, err := safeLocalPath(s.root, key)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *LocalStore) KeyFromURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if s.publicBaseURL != "" {
		base, err := url.Parse(s.publicBaseURL)
		if err == nil && parsed.Host != "" && parsed.Host != base.Host {
			return "", false
		}
	}
	if !strings.HasPrefix(parsed.Path, "/uploads/") {
		return "", false
	}
	return strings.TrimPrefix(parsed.Path, "/uploads/"), true
}
