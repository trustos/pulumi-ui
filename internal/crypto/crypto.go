package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

type Encryptor struct {
	key []byte // 32 bytes
}

func NewEncryptor(hexKey string) (*Encryptor, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("PULUMI_UI_ENCRYPTION_KEY must be 64 hex characters (32 bytes)")
	}
	return &Encryptor{key: key}, nil
}

// Encrypt returns a []byte containing: nonce (12 bytes) || ciphertext
func (e *Encryptor) Encrypt(plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// EncryptBytes encrypts arbitrary binary data.
func (e *Encryptor) EncryptBytes(plaintext []byte) ([]byte, error) {
	return e.Encrypt(string(plaintext))
}

// DecryptBytes decrypts data and returns raw bytes instead of a string.
func (e *Encryptor) DecryptBytes(data []byte) ([]byte, error) {
	s, err := e.Decrypt(data)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// Decrypt expects: nonce (12 bytes) || ciphertext
func (e *Encryptor) Decrypt(data []byte) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong key?): %w", err)
	}
	return string(plaintext), nil
}

// deriveKeyFromPassphrase derives a 32-byte AES key from a passphrase and salt
// using PBKDF2 with SHA-256 and 100,000 iterations.
func deriveKeyFromPassphrase(passphrase string, salt []byte) []byte {
	return pbkdf2.Key([]byte(passphrase), salt, 100_000, 32, sha256.New)
}

// EncryptWithPassphrase encrypts plaintext using a key derived from the given
// passphrase and salt via PBKDF2. The salt must be provided by the caller so
// multiple values can share one PBKDF2 derivation. Returns nonce || ciphertext.
func EncryptWithPassphrase(plaintext []byte, passphrase string, salt []byte) ([]byte, error) {
	key := deriveKeyFromPassphrase(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptWithPassphrase decrypts ciphertext (nonce || ciphertext) using a key
// derived from the given passphrase and salt via PBKDF2.
func DecryptWithPassphrase(ciphertext []byte, passphrase string, salt []byte) ([]byte, error) {
	key := deriveKeyFromPassphrase(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong passphrase?): %w", err)
	}
	return plaintext, nil
}
