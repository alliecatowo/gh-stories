package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// keyringService is the service name every gh-stories entry is filed under
// in the OS credential store. The account name within that service is the
// Stories service URL, so multiple services (e.g. self-hosted vs. the
// default) each get their own keychain entry.
const keyringService = "gh-stories"

// keyringProbeAccount is a throwaway account name used only to verify, once
// per process, that the keyring backend actually works on this machine
// (there is a real backend running, and this process can reach it without
// blocking on an interactive prompt it can't satisfy). Never used to store a
// real credential.
const keyringProbeAccount = "__gh-stories-probe__"

var _ Store = (*keyringStore)(nil)

// keyringStore stores tokens in the OS credential store (macOS Keychain,
// Windows Credential Manager, libsecret on Linux via go-keyring). Because
// most keyring backends only support get/set/delete by exact key and cannot
// enumerate their own contents, keyringStore also keeps a small sidecar
// index file recording which service URLs currently have a credential
// stored. That index file holds no secrets — only service URLs, logins and
// timestamps — so it does not need the same treatment as the file-backend's
// credentials.json, but it is still permission-restricted defensively.
type keyringStore struct {
	mu        sync.Mutex
	indexPath string
}

// newKeyringStore attempts to construct a working keyring-backed Store. The
// second return value is false when no usable keyring is available (headless
// Linux with no Secret Service, an unsupported platform, or a probe that
// failed for any other reason) — callers should fall back to the file
// backend in that case rather than treating it as fatal.
func newKeyringStore() (*keyringStore, bool) {
	dir, err := configDir()
	if err != nil {
		return nil, false
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false
	}
	_ = os.Chmod(dir, 0o700)

	ks := &keyringStore{indexPath: filepath.Join(dir, "index.json")}
	if !ks.probe() {
		return nil, false
	}
	return ks, true
}

// probe verifies the keyring backend is actually usable by round-tripping a
// throwaway secret. This is what lets doctor and Open honestly report "file"
// instead of hanging or erroring deep inside a login flow on a machine with
// no keyring daemon running.
func (k *keyringStore) probe() bool {
	if err := keyring.Set(keyringService, keyringProbeAccount, "probe"); err != nil {
		return false
	}
	_, err := keyring.Get(keyringService, keyringProbeAccount)
	_ = keyring.Delete(keyringService, keyringProbeAccount)
	return err == nil
}

func (k *keyringStore) Backend() string { return "keyring" }

func (k *keyringStore) Load(serviceURL string) (*Credential, error) {
	raw, err := keyring.Get(keyringService, serviceURL)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("credstore: read from OS keyring: %w", err)
	}
	var c Credential
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("credstore: stored credential is corrupt: %w", err)
	}
	return &c, nil
}

func (k *keyringStore) Save(cred Credential) error {
	b, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("credstore: encode credential: %w", err)
	}
	if err := keyring.Set(keyringService, cred.ServiceURL, string(b)); err != nil {
		return fmt.Errorf("credstore: write to OS keyring: %w", err)
	}
	return k.indexAdd(cred.ServiceURL)
}

func (k *keyringStore) Delete(serviceURL string) error {
	err := keyring.Delete(keyringService, serviceURL)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("credstore: delete from OS keyring: %w", err)
	}
	return k.indexRemove(serviceURL)
}

func (k *keyringStore) List() ([]Credential, error) {
	urls, err := k.readIndex()
	if err != nil {
		return nil, err
	}
	out := make([]Credential, 0, len(urls))
	for _, u := range urls {
		c, err := k.Load(u)
		if errors.Is(err, ErrNotFound) {
			// The index and the keyring drifted apart (e.g. the entry was
			// removed by something other than this package); skip rather
			// than fail the whole listing.
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, nil
}

func (k *keyringStore) readIndex() ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	b, err := os.ReadFile(k.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credstore: read service index: %w", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	var urls []string
	if err := json.Unmarshal(b, &urls); err != nil {
		return nil, fmt.Errorf("credstore: service index is corrupt: %w", err)
	}
	return urls, nil
}

func (k *keyringStore) writeIndex(urls []string) error {
	b, err := json.MarshalIndent(urls, "", "  ")
	if err != nil {
		return fmt.Errorf("credstore: encode service index: %w", err)
	}
	tmp := k.indexPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("credstore: write service index: %w", err)
	}
	if err := os.Rename(tmp, k.indexPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("credstore: replace service index: %w", err)
	}
	return nil
}

func (k *keyringStore) indexAdd(serviceURL string) error {
	k.mu.Lock()
	urls, err := k.readIndexLocked()
	if err != nil {
		k.mu.Unlock()
		return err
	}
	for _, u := range urls {
		if u == serviceURL {
			k.mu.Unlock()
			return nil
		}
	}
	urls = append(urls, serviceURL)
	err = k.writeIndexLocked(urls)
	k.mu.Unlock()
	return err
}

func (k *keyringStore) indexRemove(serviceURL string) error {
	k.mu.Lock()
	urls, err := k.readIndexLocked()
	if err != nil {
		k.mu.Unlock()
		return err
	}
	kept := urls[:0]
	for _, u := range urls {
		if u != serviceURL {
			kept = append(kept, u)
		}
	}
	err = k.writeIndexLocked(kept)
	k.mu.Unlock()
	return err
}

// readIndexLocked/writeIndexLocked are the same as readIndex/writeIndex but
// assume k.mu is already held, for use from the add/remove helpers above
// (which need read-modify-write atomicity under one lock).
func (k *keyringStore) readIndexLocked() ([]string, error) {
	b, err := os.ReadFile(k.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credstore: read service index: %w", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	var urls []string
	if err := json.Unmarshal(b, &urls); err != nil {
		return nil, fmt.Errorf("credstore: service index is corrupt: %w", err)
	}
	return urls, nil
}

func (k *keyringStore) writeIndexLocked(urls []string) error {
	return k.writeIndex(urls)
}
