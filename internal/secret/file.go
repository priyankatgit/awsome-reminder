package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	metaKey  = "sync_uri_enc"
	keyFile  = "sync.key"
	prefix   = "v1:"
	keyBytes = 32
)

var aad = []byte("awesome-reminder atlas-uri v1")

var errUnreadable = errors.New("the saved connection string can't be read; paste it again")

func (s *store) keyPath() string { return filepath.Join(s.dataDir, keyFile) }

// loadKey reads the key file, creating it when create is set and it is absent.
func (s *store) loadKey(create bool) ([]byte, error) {
	b, err := os.ReadFile(s.keyPath())
	if err == nil && len(b) == keyBytes {
		return b, nil
	}
	if !create {
		return nil, errUnreadable
	}
	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.keyPath(), key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func (s *store) fileSet(v string) error {
	key, err := s.loadKey(true)
	if err != nil {
		return err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(v), aad) // nonce ‖ ciphertext
	return s.meta.SetMeta(metaKey, prefix+base64.StdEncoding.EncodeToString(sealed))
}

func (s *store) fileGet() (string, error) {
	enc, ok, err := s.meta.Meta(metaKey)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	if !strings.HasPrefix(enc, prefix) {
		return "", errUnreadable
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, prefix))
	if err != nil {
		return "", errUnreadable
	}
	key, err := s.loadKey(false)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errUnreadable
	}
	n := gcm.NonceSize()
	pt, err := gcm.Open(nil, raw[:n], raw[n:], aad)
	if err != nil {
		return "", errUnreadable
	}
	return string(pt), nil
}

func (s *store) fileDelete() error {
	if err := s.meta.DeleteMeta(metaKey); err != nil {
		return err
	}
	if err := os.Remove(s.keyPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
