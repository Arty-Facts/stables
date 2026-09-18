// Package sealbox password-encrypts the baked models.json + secrets.env so a
// build-secrets binary only yields its secrets when the installer password is
// correct. Wrong or missing password -> secrets stay sealed in the binary.
package sealbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// magic prefixes every sealed payload so install can distinguish an encrypted
// blob from a plaintext one (minimal/extension builds bake nothing).
const magic = "STB1"

const (
	saltLen  = 16
	nonceLen = 12
	keyLen   = 32
	iters    = 200_000
)

// IsEncrypted reports whether blob is a sealbox payload.
func IsEncrypted(blob []byte) bool {
	return strings.HasPrefix(string(blob), magic+":")
}

// Encrypt seals plaintext with password, returning
// "STB1:<base64(salt|nonce|ciphertext)>".
func Encrypt(password string, plaintext []byte) ([]byte, error) {
	if password == "" {
		return nil, errors.New("sealbox: empty password")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key := deriveKey([]byte(password), salt, iters, keyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	payload := make([]byte, 0, saltLen+nonceLen+len(ct))
	payload = append(payload, salt...)
	payload = append(payload, nonce...)
	payload = append(payload, ct...)
	return []byte(magic + ":" + base64.StdEncoding.EncodeToString(payload)), nil
}

// Decrypt opens a sealbox blob with password. A wrong password fails GCM
// authentication and returns an error.
func Decrypt(password string, blob []byte) ([]byte, error) {
	if !IsEncrypted(blob) {
		return nil, errors.New("sealbox: not an encrypted payload")
	}
	if password == "" {
		return nil, errors.New("sealbox: empty password")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(blob), magic+":"))
	if err != nil {
		return nil, err
	}
	if len(payload) < saltLen+nonceLen {
		return nil, errors.New("sealbox: truncated payload")
	}
	salt := payload[:saltLen]
	nonce := payload[saltLen : saltLen+nonceLen]
	ct := payload[saltLen+nonceLen:]
	key := deriveKey([]byte(password), salt, iters, keyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// deriveKey is PBKDF2-HMAC-SHA256.
func deriveKey(password, salt []byte, iters, keyLen int) []byte {
	h := func(data []byte) []byte {
		m := hmac.New(sha256.New, password)
		m.Write(data)
		return m.Sum(nil)
	}
	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, numBlocks*hashLen)
	var block [4]byte
	for i := 1; i <= numBlocks; i++ {
		block[0] = byte(i >> 24)
		block[1] = byte(i >> 16)
		block[2] = byte(i >> 8)
		block[3] = byte(i)
		u := h(append(append([]byte{}, salt...), block[:]...))
		t := append([]byte(nil), u...)
		for j := 1; j < iters; j++ {
			u = h(u)
			for k := range t {
				t[k] ^= u[k]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
