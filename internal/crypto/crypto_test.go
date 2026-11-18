package crypto

import (
	"strings"
	"testing"
)

func TestBox_RoundTrip(t *testing.T) {
	b, err := NewBox("master-secret-1234567890")
	if err != nil {
		t.Fatal(err)
	}
	plaintexts := []string{
		`{"token":"kaggle-key-abc123"}`,
		"simple-token",
		"",
		strings.Repeat("x", 4096),
		"unicode-şifre-✓",
	}
	for _, pt := range plaintexts {
		enc, err := b.Encrypt(pt)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(enc, pt) && pt != "" {
			t.Fatal("ciphertext contains plaintext")
		}
		dec, err := b.Decrypt(enc)
		if err != nil {
			t.Fatal(err)
		}
		if dec != pt {
			t.Fatalf("roundtrip mismatch: got %q want %q", dec, pt)
		}
	}
}

func TestBox_WrongKeyFails(t *testing.T) {
	b1, _ := NewBox("master-secret-1234567890")
	b2, _ := NewBox("different-secret-0987654321")
	enc, err := b1.Encrypt("topsecret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestBox_MinLength(t *testing.T) {
	if _, err := NewBox("short"); err == nil {
		t.Fatal("short master key must be rejected")
	}
}
