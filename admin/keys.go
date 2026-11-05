package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// KeyPrefix starts every logbook API key, so a leaked key is easy to spot.
const KeyPrefix = "lbk_"

// NewKey returns a new API key, its hash and its last four characters.
func NewKey() (plaintext, hash, last4 string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plaintext = KeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, HashKey(plaintext), plaintext[len(plaintext)-4:], nil
}

// HashKey returns the hash a key is stored and looked up by. Keys carry 256
// random bits, so a plain SHA-256 is enough.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
