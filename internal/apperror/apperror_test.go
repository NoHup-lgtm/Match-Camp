package apperror

import (
	"net/http"
	"testing"
)

func TestLookupKnownError(t *testing.T) {
	def := Lookup("invalid_credentials")
	if def.Status != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, def.Status)
	}
	if def.Message == "" {
		t.Fatal("expected public message")
	}
}

func TestLookupUnknownErrorFallsBack(t *testing.T) {
	def := Lookup("does_not_exist")
	if def.Code != "internal_error" {
		t.Fatalf("expected internal_error, got %s", def.Code)
	}
}
