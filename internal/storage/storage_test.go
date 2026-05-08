package storage

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProfilePhotoKey(t *testing.T) {
	userID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	key, err := ProfilePhotoKey(userID, 2, ".png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "profile-photos/"+userID.String()+"/2-") {
		t.Fatalf("unexpected key %q", key)
	}
	if !strings.HasSuffix(key, ".png") {
		t.Fatalf("expected .png suffix, got %q", key)
	}
}

func TestProfilePhotoKeyRejectsInvalidPosition(t *testing.T) {
	_, err := ProfilePhotoKey(uuid.New(), 4, ".png")
	if err == nil {
		t.Fatal("expected invalid position error")
	}
}

func TestProfilePhotoKeyRejectsPathExtension(t *testing.T) {
	_, err := ProfilePhotoKey(uuid.New(), 0, "../x")
	if err == nil {
		t.Fatal("expected invalid extension error")
	}
}

func TestLocalStoreKeyFromURL(t *testing.T) {
	store := NewLocalStore(t.TempDir(), "http://localhost:8080")
	key, ok := store.KeyFromURL("http://localhost:8080/uploads/profile-photos/user/0-a.png")
	if !ok {
		t.Fatal("expected key")
	}
	if key != "profile-photos/user/0-a.png" {
		t.Fatalf("unexpected key %q", key)
	}
}
