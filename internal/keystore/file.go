package keystore

import (
	"errors"
	"os"
	"strings"
)

// FileStore persists the encryption key as a plaintext hex string in a file.
// The file lives in the data directory alongside the SQLite database.
type FileStore struct {
	path string
}

// NewFileStore creates a FileStore at the given file path.
// Typical path: $DATA_DIR/encryption.key
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Load() (string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", nil
	}
	return key, validateHexKey(key)
}

func (s *FileStore) Save(hexKey string) error {
	return os.WriteFile(s.path, []byte(hexKey+"\n"), 0600)
}

func (s *FileStore) Description() string {
	return "file:" + s.path
}
