package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func mustKey(t *testing.T) *MasterKey {
	t.Helper()
	buf := make([]byte, masterKeyLen)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	mk, err := LoadMasterKey(base64.StdEncoding.EncodeToString(buf))
	if err != nil {
		t.Fatal(err)
	}
	return mk
}

func TestLoadMasterKey(t *testing.T) {
	// Wrong length
	if _, err := LoadMasterKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected error on short key")
	}
	// Invalid base64
	if _, err := LoadMasterKey("not base64!!!"); err == nil {
		t.Fatal("expected error on bad base64")
	}
	// Happy path
	buf := make([]byte, masterKeyLen)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	mk, err := LoadMasterKey(base64.StdEncoding.EncodeToString(buf))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if mk == nil {
		t.Fatal("nil master key")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	mk := mustKey(t)

	cases := []struct {
		name string
		in   []byte
	}{
		{"empty", []byte("")},
		{"short", []byte("hello")},
		{"binary", []byte{0x00, 0x01, 0x02, 0xff, 0xfe}},
		{"password", []byte(`{"username":"admin","password":"sekret"}`)},
		{"long", bytes.Repeat([]byte("x"), 4096)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct, nonce, err := mk.Encrypt(tc.in)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			if tc.name != "empty" && bytes.Equal(ct, tc.in) {
				t.Fatal("ciphertext must differ from plaintext")
			}
			pt, err := mk.Decrypt(ct, nonce)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if !bytes.Equal(pt, tc.in) {
				t.Fatalf("round-trip mismatch: got %q want %q", pt, tc.in)
			}
		})
	}
}

func TestDecryptWithWrongNonce(t *testing.T) {
	mk := mustKey(t)
	ct, _, err := mk.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	// nonce must be 12 bytes for GCM
	badNonce := make([]byte, 12)
	if _, err := mk.Decrypt(ct, badNonce); err == nil {
		t.Fatal("expected auth failure with wrong nonce")
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	const pw = "correct-horse-battery-staple"

	h, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if h == pw {
		t.Fatal("hash must not equal plaintext")
	}

	ok, err := VerifyPassword(pw, h)
	if err != nil || !ok {
		t.Fatalf("verify correct: ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword("wrong", h)
	if err != nil || ok {
		t.Fatalf("verify wrong: ok=%v err=%v", ok, err)
	}

	if _, err := HashPassword(""); err == nil {
		t.Fatal("empty password should error")
	}
}

func TestRandomTokenAndHash(t *testing.T) {
	tok1, err := RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	if tok1 == tok2 {
		t.Fatal("random tokens should differ")
	}
	if len(tok1) != 64 { // hex of 32 bytes
		t.Fatalf("want 64 hex chars, got %d", len(tok1))
	}
	if HashToken("same") != HashToken("same") {
		t.Fatal("hash should be deterministic")
	}
	if HashToken("a") == HashToken("b") {
		t.Fatal("different inputs should produce different hashes")
	}
}
