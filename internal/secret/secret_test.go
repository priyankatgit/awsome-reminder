package secret

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

const testURI = "mongodb+srv://alice:s3cretPassw0rd@cluster0.example.mongodb.net/?retryWrites=true"

type fakeMeta map[string]string

func (m fakeMeta) Meta(k string) (string, bool, error) { v, ok := m[k]; return v, ok, nil }
func (m fakeMeta) SetMeta(k, v string) error           { m[k] = v; return nil }
func (m fakeMeta) DeleteMeta(k string) error           { delete(m, k); return nil }

type fakeKeychain struct {
	val string
	set bool
	err error // returned by every call when non-nil
}

func (f *fakeKeychain) Get(service, user string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if !f.set {
		return "", keyring.ErrNotFound
	}
	return f.val, nil
}
func (f *fakeKeychain) Set(service, user, pw string) error {
	if f.err != nil {
		return f.err
	}
	f.val, f.set = pw, true
	return nil
}
func (f *fakeKeychain) Delete(service, user string) error {
	if f.err != nil {
		return f.err
	}
	if !f.set {
		return keyring.ErrNotFound
	}
	f.val, f.set = "", false
	return nil
}

var dbusErr = errors.New("The name org.freedesktop.secrets was not provided by any .service files")

func TestKeychainPreferred(t *testing.T) {
	meta, kc := fakeMeta{}, &fakeKeychain{}
	s := newStore(t.TempDir(), meta, kc)
	if err := s.Set(testURI); err != nil {
		t.Fatal(err)
	}
	if !kc.set || kc.val != testURI {
		t.Fatal("keychain not written")
	}
	if len(meta) != 0 {
		t.Fatalf("meta should be empty, got %v", meta)
	}
	got, err := s.Get()
	if err != nil || got != testURI {
		t.Fatalf("Get = %q, %v", got, err)
	}
	want := map[string]string{"windows": "Windows Credential Manager", "darwin": "macOS Keychain"}[runtime.GOOS]
	if want == "" {
		want = "Secret Service"
	}
	if s.Kind() != want {
		t.Fatalf("Kind = %q, want %q", s.Kind(), want)
	}
}

func TestFallbackWhenKeychainUnavailable(t *testing.T) {
	dir, meta := t.TempDir(), fakeMeta{}
	kc := &fakeKeychain{err: dbusErr}
	s := newStore(dir, meta, kc)
	if err := s.Set(testURI); err != nil {
		t.Fatal(err)
	}
	enc := meta["sync_uri_enc"]
	if !strings.HasPrefix(enc, "v1:") {
		t.Fatalf("meta = %q", enc)
	}
	if strings.Contains(enc, testURI) || strings.Contains(enc, "s3cretPassw0rd") {
		t.Fatal("ciphertext leaks the URI")
	}
	if _, err := os.Stat(filepath.Join(dir, "sync.key")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get()
	if err != nil || got != testURI {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if s.Kind() != "Encrypted file on this computer" {
		t.Fatalf("Kind = %q", s.Kind())
	}
}

func fallbackStore(t *testing.T) (Store, string, fakeMeta) {
	dir, meta := t.TempDir(), fakeMeta{}
	s := newStore(dir, meta, &fakeKeychain{err: dbusErr})
	if err := s.Set(testURI); err != nil {
		t.Fatal(err)
	}
	return s, dir, meta
}

const unreadable = "the saved connection string can't be read; paste it again"

func TestFallbackTamperedFails(t *testing.T) {
	s, _, meta := fallbackStore(t)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(meta["sync_uri_enc"], "v1:"))
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	meta["sync_uri_enc"] = "v1:" + base64.StdEncoding.EncodeToString(raw)
	_, err = s.Get()
	if err == nil || err.Error() != unreadable {
		t.Fatalf("err = %v", err)
	}
}

func TestFallbackKeyFileMissing(t *testing.T) {
	s, dir, _ := fallbackStore(t)
	if err := os.Remove(filepath.Join(dir, "sync.key")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get()
	if err == nil || err.Error() != unreadable {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteClearsEverything(t *testing.T) {
	// keychain path
	meta, kc := fakeMeta{}, &fakeKeychain{}
	s := newStore(t.TempDir(), meta, kc)
	if err := s.Set(testURI); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if kc.set {
		t.Fatal("keychain entry remains")
	}
	if _, err := s.Get(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// fallback path
	fs, dir, fmeta := fallbackStore(t)
	if err := fs.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fmeta["sync_uri_enc"]; ok {
		t.Fatal("ciphertext remains")
	}
	if _, err := os.Stat(filepath.Join(dir, "sync.key")); !os.IsNotExist(err) {
		t.Fatalf("key file remains: %v", err)
	}
	if _, err := fs.Get(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// deleting again is not an error
	if err := fs.Delete(); err != nil {
		t.Fatal(err)
	}
}

func TestGetUnsetIsErrNotFound(t *testing.T) {
	for _, kc := range []*fakeKeychain{{}, {err: dbusErr}} {
		s := newStore(t.TempDir(), fakeMeta{}, kc)
		if _, err := s.Get(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestSetClearsStaleKeychainEntry(t *testing.T) {
	// A value in the keychain, then the keychain starts failing on Set only.
	kc := &fakeKeychain{val: "old", set: true}
	meta := fakeMeta{}
	s := &store{dataDir: t.TempDir(), meta: meta, kc: &failSet{kc}}
	if err := s.Set(testURI); err != nil {
		t.Fatal(err)
	}
	if kc.set {
		t.Fatal("stale keychain entry not cleared")
	}
	if got, err := s.Get(); err != nil || got != testURI {
		t.Fatalf("Get = %q, %v", got, err)
	}
}

type failSet struct{ *fakeKeychain }

func (f *failSet) Set(string, string, string) error { return dbusErr }

type failDelete struct{ *fakeKeychain }

func (f *failDelete) Delete(string, string) error { return errors.New("keychain is locked") }

func TestDeleteReportsSurvivingKeychainEntry(t *testing.T) {
	kc := &fakeKeychain{val: testURI, set: true}
	dir, meta := t.TempDir(), fakeMeta{}
	s := &store{dataDir: dir, meta: meta, kc: &failDelete{kc}}
	if err := s.fileSet(testURI); err != nil {
		t.Fatal(err)
	}
	err := s.Delete()
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "s3cretPassw0rd") {
		t.Fatal("error leaks URI")
	}
	if _, ok := meta["sync_uri_enc"]; ok {
		t.Fatal("ciphertext remains")
	}
	if _, e := os.Stat(filepath.Join(dir, "sync.key")); !os.IsNotExist(e) {
		t.Fatal("key file remains")
	}
}

func TestDeleteOnUnavailableKeychainSucceeds(t *testing.T) {
	dir, meta := t.TempDir(), fakeMeta{}
	s := &store{dataDir: dir, meta: meta, kc: &fakeKeychain{err: dbusErr}}
	if err := s.fileSet(testURI); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, ok := meta["sync_uri_enc"]; ok {
		t.Fatal("ciphertext remains")
	}
	if _, e := os.Stat(filepath.Join(dir, "sync.key")); !os.IsNotExist(e) {
		t.Fatal("key file remains")
	}
}

// mapKeychain keeps one value per account, like a real keychain.
type mapKeychain struct {
	vals map[string]string
	err  error
}

func (m *mapKeychain) Get(service, user string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	v, ok := m.vals[user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}
func (m *mapKeychain) Set(service, user, pw string) error {
	if m.err != nil {
		return m.err
	}
	m.vals[user] = pw
	return nil
}
func (m *mapKeychain) Delete(service, user string) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.vals[user]; !ok {
		return keyring.ErrNotFound
	}
	delete(m.vals, user)
	return nil
}

func TestNamedSecretsIndependent(t *testing.T) {
	const token = "tk_abcdefghijklmnopqrstuvwxyz012"
	t.Run("keychain", func(t *testing.T) {
		dir, meta, kc := t.TempDir(), fakeMeta{}, &mapKeychain{vals: map[string]string{}}
		atlas := newNamedStore(dir, meta, kc, AtlasURI)
		push := newNamedStore(dir, meta, kc, PushToken)
		if err := atlas.Set(testURI); err != nil {
			t.Fatal(err)
		}
		if err := push.Set(token); err != nil {
			t.Fatal(err)
		}
		if kc.vals["atlas-uri"] != testURI || kc.vals["ntfy-token"] != token {
			t.Fatalf("keychain accounts = %v", kc.vals)
		}
		if err := atlas.Delete(); err != nil {
			t.Fatal(err)
		}
		if got, err := push.Get(); err != nil || got != token {
			t.Fatalf("push Get = %q, %v", got, err)
		}
		if _, err := atlas.Get(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("atlas err = %v", err)
		}
	})
	t.Run("file fallback", func(t *testing.T) {
		dir, meta, kc := t.TempDir(), fakeMeta{}, &mapKeychain{err: dbusErr}
		atlas := newNamedStore(dir, meta, kc, AtlasURI)
		push := newNamedStore(dir, meta, kc, PushToken)
		if err := atlas.Set(testURI); err != nil {
			t.Fatal(err)
		}
		if err := push.Set(token); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"sync_uri_enc", "push_token_enc"} {
			if _, ok := meta[k]; !ok {
				t.Fatalf("meta %s missing", k)
			}
		}
		for _, f := range []string{"sync.key", "push.key"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Fatal(err)
			}
		}
		if err := atlas.Delete(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "sync.key")); !os.IsNotExist(err) {
			t.Fatalf("sync.key remains: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "push.key")); err != nil {
			t.Fatalf("push.key gone: %v", err)
		}
		if got, err := push.Get(); err != nil || got != token {
			t.Fatalf("push Get = %q, %v", got, err)
		}
		if push.Kind() != "Encrypted file on this computer" {
			t.Fatalf("Kind = %q", push.Kind())
		}
	})
}

func TestPushTokenDeleteErrorText(t *testing.T) {
	s := &store{dataDir: t.TempDir(), meta: fakeMeta{}, kc: &failDelete{&fakeKeychain{val: "tk_x", set: true}}, name: PushToken}
	err := s.Delete()
	if err == nil || !strings.Contains(err.Error(), "couldn't remove the access token from the system keychain") {
		t.Fatalf("err = %v", err)
	}
}
