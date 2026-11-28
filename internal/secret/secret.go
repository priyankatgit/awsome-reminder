// Package secret stores the Atlas connection string. It prefers the OS
// keychain and falls back to an AES-256-GCM encrypted value in the database
// whose key lives in a separate file. The value never appears in errors.
package secret

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/zalando/go-keyring"
)

const (
	service = "dev.awesome-reminder.app"

	kindFile = "Encrypted file on this computer"
)

// Name identifies one secret: its keychain account, meta key and key file.
type Name struct {
	account string
	metaKey string
	keyFile string
	noun    string // how the UI refers to the value, used in error text
	aad     []byte
}

var (
	// AtlasURI is the Atlas connection string.
	AtlasURI = Name{account: "atlas-uri", metaKey: "sync_uri_enc", keyFile: "sync.key",
		noun: "connection string", aad: []byte("awesome-reminder atlas-uri v1")}
	// PushToken is the ntfy access token.
	PushToken = Name{account: "ntfy-token", metaKey: "push_token_enc", keyFile: "push.key",
		noun: "access token", aad: []byte("awesome-reminder ntfy-token v1")}
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
	name    Name // zero value means AtlasURI
}

func (s *store) n() Name {
	if s.name.account == "" {
		return AtlasURI
	}
	return s.name
}

// New returns a Store backed by the OS keychain, falling back to an
// encrypted file under dataDir plus the meta table.
func New(dataDir string, meta MetaStore) Store { return NewNamed(dataDir, meta, AtlasURI) }

// NewNamed is New for the secret called name.
func NewNamed(dataDir string, meta MetaStore, name Name) Store {
	return newNamedStore(dataDir, meta, osKeychain{}, name)
}

func newStore(dataDir string, meta MetaStore, kc keychain) Store {
	return newNamedStore(dataDir, meta, kc, AtlasURI)
}

func newNamedStore(dataDir string, meta MetaStore, kc keychain, name Name) Store {
	return &store{dataDir: dataDir, meta: meta, kc: kc, name: name}
}

func (s *store) Get() (string, error) {
	if v, err := s.kc.Get(service, s.n().account); err == nil {
		return v, nil
	}
	// Not found or keychain unavailable: look at the fallback.
	return s.fileGet()
}

func (s *store) Set(v string) error {
	if err := s.kc.Set(service, s.n().account, v); err == nil {
		// Drop any older fallback copy so Kind and Get stay unambiguous.
		return s.fileDelete()
	}
	if err := s.fileSet(v); err != nil {
		return err
	}
	_ = s.kc.Delete(service, s.n().account) // clear a stale keychain entry; best effort
	return nil
}

func (s *store) Delete() error {
	kcErr := s.kc.Delete(service, s.n().account)
	if errors.Is(kcErr, keyring.ErrNotFound) {
		kcErr = nil
	}
	fileErr := s.fileDelete() // always runs
	if kcErr != nil {
		// With no usable keychain every call errors; only report when the
		// entry demonstrably survives.
		if v, err := s.kc.Get(service, s.n().account); err == nil && v != "" {
			return errors.Join(fmt.Errorf("couldn't remove the %s from the system keychain", s.n().noun), fileErr)
		}
	}
	return fileErr
}

func (s *store) Kind() string {
	if _, ok, _ := s.meta.Meta(s.n().metaKey); ok {
		return kindFile
	}
	if _, err := s.kc.Get(service, s.n().account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
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

// sysKeychain is the keychain ClearKeychain acts on, swappable for tests.
var sysKeychain keychain = osKeychain{}

// ClearKeychain removes the saved secrets (the connection string and the
// phone push access token) from the OS keychain, where one entry is shared by
// every data folder. A missing entry is not an error, and neither is a
// keychain that can't be used at all (Linux without Secret Service): like
// Store.Delete, it fails only when an entry demonstrably survives. The
// encrypted-file fallbacks live in the data folder and are not touched.
func ClearKeychain() error {
	var errs []error
	for _, n := range []Name{AtlasURI, PushToken} {
		errs = append(errs, clearAccount(n.account))
	}
	return errors.Join(errs...)
}

func clearAccount(account string) error {
	err := sysKeychain.Delete(service, account)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if v, gerr := sysKeychain.Get(service, account); gerr == nil && v != "" {
		return err
	}
	return nil
}
