package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oxsean/fav/internal/fileio"
)

// KeyFile is the server's key for the secrets it keeps (tracker tokens), beside the database but never in a backup.
// TEND_SERVER_KEY, when set, is used instead.
const KeyFile = "server.key"

// Sealer encrypts the secrets the database holds (AES-256-GCM).
type Sealer struct{ aead cipher.AEAD }

// LoadSealer reads the key from TEND_SERVER_KEY or home's key file, making the file (0600) the first time.
func LoadSealer(home string) (*Sealer, error) {
	var key []byte
	if v := os.Getenv("TEND_SERVER_KEY"); v != "" {
		sum := sha256.Sum256([]byte(v))
		key = sum[:]
	} else {
		path := filepath.Join(home, KeyFile)
		b, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			b = make([]byte, 32)
			rand.Read(b)
			if err := fileio.WriteFile(path, b, 0o600); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		case len(b) != 32:
			return nil, fmt.Errorf("%s: not a 32-byte key", path)
		}
		key = b
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plain []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, plain, nil)
}

func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed secret too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
