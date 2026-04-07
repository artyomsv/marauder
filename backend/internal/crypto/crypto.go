// Package crypto provides the primitives used for secret handling:
//
//   - AES-256-GCM symmetric encryption for data at rest, keyed by the
//     master key loaded from MARAUDER_MASTER_KEY.
//   - Argon2id password hashing and verification.
//   - Random token generation helpers.
//
// Everything here is designed to panic on programmer error (e.g. wrong key
// length) and to return errors on user-supplied input errors.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	masterKeyLen = 32 // AES-256

	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonSaltLen = 16
	argonKeyLen  = 32
)

// MasterKey wraps the 32-byte AES-256 key loaded from configuration.
type MasterKey struct {
	key [masterKeyLen]byte
}

// LoadMasterKey decodes a base64-encoded 32-byte key.
func LoadMasterKey(b64 string) (*MasterKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("master key is not valid base64: %w", err)
	}
	if len(raw) != masterKeyLen {
		return nil, fmt.Errorf("master key must be exactly %d bytes after base64 decode, got %d", masterKeyLen, len(raw))
	}
	var mk MasterKey
	copy(mk.key[:], raw)
	return &mk, nil
}

// Encrypt produces (ciphertext, nonce) using AES-256-GCM.
// The nonce is random for every call.
func (m *MasterKey) Encrypt(plaintext []byte) (ct, nonce []byte, err error) {
	block, err := aes.NewCipher(m.key[:])
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ct = gcm.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

// Decrypt reverses Encrypt.
func (m *MasterKey) Decrypt(ct, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(m.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("bad nonce size")
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// EncryptString is a convenience wrapper.
func (m *MasterKey) EncryptString(s string) ([]byte, []byte, error) {
	return m.Encrypt([]byte(s))
}

// DecryptString is a convenience wrapper.
func (m *MasterKey) DecryptString(ct, nonce []byte) (string, error) {
	b, err := m.Decrypt(ct, nonce)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// HashPassword returns a PHC-style argon2id encoded password hash.
// The format is:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<base64 salt>$<base64 hash>
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// VerifyPassword checks a plaintext password against a PHC-encoded hash.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// Expected: ["", "argon2id", "v=19", "m=65536,t=3,p=4", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("bad version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, fmt.Errorf("bad params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("bad salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("bad hash: %w", err)
	}
	// Argon2id keys are at most a few KB; len(want) easily fits in
	// uint32. The bound check is explicit so a future maintainer who
	// removes the #nosec annotation still has the guard.
	if len(want) > int(^uint32(0)) {
		return false, errors.New("hash field is implausibly large")
	}
	wantLen := uint32(len(want)) // #nosec G115 -- bounded above
	got := argon2.IDKey([]byte(password), salt, t, m, p, wantLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// RandomToken returns a cryptographically-secure random token, hex-encoded.
// length is the number of random bytes (the returned string is 2*length chars).
func RandomToken(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashToken returns the SHA-256 hash of a token, hex-encoded. Used for
// storing refresh tokens server-side: we never keep the plaintext.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
