package secret

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func newKeyB64(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	box, err := New(newKeyB64(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range []string{"", "s3cret", "postgres://u:p@h/db?x=1", "unicode ✓ 🔒"} {
		ct, nonce, err := box.Encrypt(pt)
		if err != nil {
			t.Fatalf("encrypt %q: %v", pt, err)
		}
		got, err := box.Decrypt(ct, nonce)
		if err != nil {
			t.Fatalf("decrypt %q: %v", pt, err)
		}
		if got != pt {
			t.Fatalf("round-trip mismatch: got %q want %q", got, pt)
		}
	}
}

// Each encryption uses a fresh nonce, so the same plaintext yields different
// ciphertext each time (no deterministic leakage).
func TestNonceIsFreshPerValue(t *testing.T) {
	box, _ := New(newKeyB64(t))
	ct1, n1, _ := box.Encrypt("same")
	ct2, n2, _ := box.Encrypt("same")
	if string(n1) == string(n2) {
		t.Fatal("nonce reused across encryptions")
	}
	if string(ct1) == string(ct2) {
		t.Fatal("ciphertext identical for same plaintext (nonce not applied)")
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	box, _ := New(newKeyB64(t))
	ct, nonce, _ := box.Encrypt("value")
	ct[0] ^= 0xff
	if _, err := box.Decrypt(ct, nonce); err == nil {
		t.Fatal("tampered ciphertext decrypted without error")
	}
}

func TestWrongKeyFails(t *testing.T) {
	a, _ := New(newKeyB64(t))
	b, _ := New(newKeyB64(t))
	ct, nonce, _ := a.Encrypt("value")
	if _, err := b.Decrypt(ct, nonce); err == nil {
		t.Fatal("decrypt succeeded under a different key")
	}
}

func TestNewKeyValidation(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := New("not-base64!!!"); err == nil {
		t.Fatal("non-base64 key accepted")
	}
	if _, err := New(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("16-byte key accepted; want 32")
	}
}
