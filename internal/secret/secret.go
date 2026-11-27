// Package secret stores the Atlas connection string. It prefers the OS
// keychain and falls back to an AES-256-GCM encrypted value in the database
// whose key lives in a separate file. The value never appears in errors.
package secret

import (
	"errors"
	"runtime"

	"github.com/zalando/go-keyring"
)

const (
	service = "dev.awesome-reminder.app"
	account = "atlas-uri"

	kindFile = "Encrypted file on this computer"
)

// ErrNotFound means no connection string has been saved.
var ErrNotFound = errors.New("no connection string saved")

// MetaStore is the slice of *store.Store the fallback needs.
type MetaStore interface {
	Meta(key string) (string, bool, error)
	SetMeta(key, value string) error
	DeleteMeta(key string) error
}

// Store keeps one secret.
type Store interface {
	Get() (string, error) // ErrNotFound when unset
	Set(v string) error
	Delete() error // clears keychain entry, ciphertext and key file; missing pieces are not errors
	Kind() string  // where the secret lives, for the UI
}

// keychain is the subset of go-keyring used, injectable for tests.
type keychain interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type osKeychain struct{}

func (osKeychain) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (osKeychain) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (osKeychain) Delete(s, u string) error        { return keyring.Delete(s, u) }

type store struct {
	dataDir string
	meta    MetaStore
	kc      keychain
}

// New returns a Store backed by the OS keychain, falling back to an
// encrypted file under dataDir plus the meta table.
func New(dataDir string, meta MetaStore) Store { return newStore(dataDir, meta, osKeychain{}) }

func newStore(dataDir string, meta MetaStore, kc keychain) Store {
	return &store{dataDir: dataDir, meta: meta, kc: kc}
}

func (s *store) Get() (string, error) {
	if v, err := s.kc.Get(service, account); err == nil {
		return v, nil
	}
	// Not found or keychain unavailable: look at the fallback.
	return s.fileGet()
}

func (s *store) Set(v string) error {
	if err := s.kc.Set(service, account, v); err == nil {
		// Drop any older fallback copy so Kind and Get stay unambiguous.
		return s.fileDelete()
	}
	if err := s.fileSet(v); err != nil {
		return err
	}
	_ = s.kc.Delete(service, account) // clear a stale keychain entry; best effort
	return nil
}

func (s *store) Delete() error {
	// A failing or absent keychain holds nothing we can reach, so its errors
	// (including not-found) are not fatal here.
	_ = s.kc.Delete(service, account)
	return s.fileDelete()
}

func (s *store) Kind() string {
	if _, ok, _ := s.meta.Meta(metaKey); ok {
		return kindFile
	}
	if _, err := s.kc.Get(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return kindFile
	}
	switch runtime.GOOS {
	case "windows":
		return "Windows Credential Manager"
	case "darwin":
		return "macOS Keychain"
	default:
		return "Secret Service"
	}
}
