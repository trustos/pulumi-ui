package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validHexKey() string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return hex.EncodeToString(key)
}

func TestNewEncryptor_ValidKey(t *testing.T) {
	enc, err := NewEncryptor(validHexKey())
	require.NoError(t, err)
	assert.NotNil(t, enc)
}

func TestNewEncryptor_InvalidHex(t *testing.T) {
	_, err := NewEncryptor("not-hex")
	assert.Error(t, err)
}

func TestNewEncryptor_WrongLength(t *testing.T) {
	_, err := NewEncryptor("0011223344") // too short
	assert.Error(t, err)
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	enc, err := NewEncryptor(validHexKey())
	require.NoError(t, err)

	plaintext := "hello world, this is a secret"
	ciphertext, err := enc.Encrypt(plaintext)
	require.NoError(t, err)
	assert.NotEmpty(t, ciphertext)

	decrypted, err := enc.Decrypt(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecrypt_EmptyString(t *testing.T) {
	enc, err := NewEncryptor(validHexKey())
	require.NoError(t, err)

	ciphertext, err := enc.Encrypt("")
	require.NoError(t, err)

	decrypted, err := enc.Decrypt(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, "", decrypted)
}

func TestEncryptDecryptBytes_RoundTrip(t *testing.T) {
	enc, err := NewEncryptor(validHexKey())
	require.NoError(t, err)

	data := []byte{0x00, 0x01, 0xFF, 0xFE}
	ciphertext, err := enc.EncryptBytes(data)
	require.NoError(t, err)

	decrypted, err := enc.DecryptBytes(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, data, decrypted)
}

func TestDecrypt_TooShort(t *testing.T) {
	enc, err := NewEncryptor(validHexKey())
	require.NoError(t, err)

	_, err = enc.Decrypt([]byte{0x01, 0x02})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestDecrypt_WrongKey(t *testing.T) {
	enc1, _ := NewEncryptor(validHexKey())

	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(i + 1)
	}
	enc2, _ := NewEncryptor(hex.EncodeToString(key2))

	ciphertext, err := enc1.Encrypt("secret")
	require.NoError(t, err)

	_, err = enc2.Decrypt(ciphertext)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decryption failed")
}

func TestEncrypt_ProducesDifferentCiphertext(t *testing.T) {
	enc, _ := NewEncryptor(validHexKey())

	ct1, _ := enc.Encrypt("same text")
	ct2, _ := enc.Encrypt("same text")

	assert.NotEqual(t, ct1, ct2, "each encryption should produce unique ciphertext (random nonce)")
}

// --- Passphrase-based encryption tests ---

func testSalt() []byte {
	salt := make([]byte, 16)
	rand.Read(salt)
	return salt
}

func TestEncryptWithPassphrase_RoundTrip(t *testing.T) {
	salt := testSalt()
	plaintext := []byte("nebula-private-key-pem-data")

	ct, err := EncryptWithPassphrase(plaintext, "my-passphrase", salt)
	require.NoError(t, err)
	assert.NotEmpty(t, ct)

	decrypted, err := DecryptWithPassphrase(ct, "my-passphrase", salt)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptWithPassphrase_EmptyPlaintext(t *testing.T) {
	salt := testSalt()

	ct, err := EncryptWithPassphrase([]byte{}, "pass", salt)
	require.NoError(t, err)

	decrypted, err := DecryptWithPassphrase(ct, "pass", salt)
	require.NoError(t, err)
	assert.Empty(t, decrypted)
}

func TestDecryptWithPassphrase_WrongPassphrase(t *testing.T) {
	salt := testSalt()

	ct, err := EncryptWithPassphrase([]byte("secret"), "correct-pass", salt)
	require.NoError(t, err)

	_, err = DecryptWithPassphrase(ct, "wrong-pass", salt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decryption failed")
}

func TestDecryptWithPassphrase_WrongSalt(t *testing.T) {
	salt1 := testSalt()
	salt2 := testSalt()

	ct, err := EncryptWithPassphrase([]byte("secret"), "pass", salt1)
	require.NoError(t, err)

	_, err = DecryptWithPassphrase(ct, "pass", salt2)
	assert.Error(t, err)
}

func TestDecryptWithPassphrase_TooShort(t *testing.T) {
	_, err := DecryptWithPassphrase([]byte{0x01}, "pass", testSalt())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestEncryptWithPassphrase_DifferentNonces(t *testing.T) {
	salt := testSalt()
	plaintext := []byte("same data")

	ct1, _ := EncryptWithPassphrase(plaintext, "pass", salt)
	ct2, _ := EncryptWithPassphrase(plaintext, "pass", salt)

	assert.NotEqual(t, ct1, ct2, "each encryption should produce unique ciphertext")
}

func TestEncryptWithPassphrase_SameSaltDeterminesKey(t *testing.T) {
	// Same passphrase + salt should derive the same key, so cross-decrypt works.
	salt := testSalt()
	plaintext := []byte("cross-decrypt test")

	ct, err := EncryptWithPassphrase(plaintext, "shared-pass", salt)
	require.NoError(t, err)

	decrypted, err := DecryptWithPassphrase(ct, "shared-pass", salt)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}
