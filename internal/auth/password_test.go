package auth

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("expected valid password")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("expected invalid password")
	}
}

func TestSessionTokenHash(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || hash == "" {
		t.Fatal("expected token and hash")
	}
	if HashToken(token) != hash {
		t.Fatal("expected stable token hash")
	}
}
