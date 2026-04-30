package server

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseChatPayloadRejectsExtraFields(t *testing.T) {
	_, err := parseChatPayloadBytes([]byte(`{
		"conversation_id":"00000000-0000-0000-0000-000000000001",
		"text":"oi",
		"image":"data"
	}`))
	if err == nil {
		t.Fatal("expected extra chat field to be rejected")
	}
}

func TestValidateChatPayloadRejectsMediaLinks(t *testing.T) {
	err := validateChatPayload(chatMessageRequest{
		ConversationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Text:           "https://example.com/foto.jpg",
	})
	if err == nil {
		t.Fatal("expected links to be rejected")
	}
}

func TestValidateChatPayloadAcceptsPlainText(t *testing.T) {
	err := validateChatPayload(chatMessageRequest{
		ConversationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Text:           "mensagem normal",
	})
	if err != nil {
		t.Fatalf("expected plain text to be accepted: %v", err)
	}
}

func TestNormalizePair(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	b := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	low, high := normalizePair(a, b)
	if low != b || high != a {
		t.Fatal("expected normalized uuid pair")
	}
}
