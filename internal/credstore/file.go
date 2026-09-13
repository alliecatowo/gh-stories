package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var _ Store = (*fileStore)(nil)

// fileStore is the headless fallback: credentials for every service URL are
// kept in one JSON file, permission-restricted to the owner. It is used
// whenever no OS keyring is usable, which `doctor` reports via Backend so a
// person on a headless box knows exactly where their token lives.
type fileStore struct {
	mu   sync.Mutex
	path string
}

// configDir returns the directory credentials live in when falling back to
// the file backend: $XDG_CONFIG_HOME/gh-stories, or ~/.config/gh-stories
// when XDG_CONFIG_HOME is unset. This is deliberately outside the repository
// and independent of the current working directory.
func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "gh-stories"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("credstore: could not determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "gh-stories"), nil
}

func newFileStore() (*fileStore, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	return openFileStoreDir(dir)
}

// openFileStoreDir opens (creating if needed) a fileStore rooted at dir. It
// is split out from newFileStore so tests can point it at a temp directory
// without needing to fake $XDG_CONFIG_HOME/$HOME.
func openFileStoreDir(dir string) (*fileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("credstore: create config directory: %w", err)
	}
	// MkdirAll does not tighten permissions on a directory that already
	// existed with looser ones, so enforce them explicitly every open.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("credstore: restrict config directory permissions: %w", err)
	}
	return &fileStore{path: filepath.Join(dir, "credentials.json")}, nil
}

func (f *fileStore) Backend() string { return "file" }

func (f *fileStore) Load(serviceURL string) (*Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.readAll()
	if err != nil {
		return nil, err
	}
	c, ok := all[serviceURL]
	if !ok {
		return nil, ErrNotFound
	}
	return &c, nil
}

func (f *fileStore) Save(cred Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.readAll()
	if err != nil {
		return err
	}
	all[cred.ServiceURL] = cred
	return f.writeAll(all)
}

func (f *fileStore) Delete(serviceURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.readAll()
	if err != nil {
		return err
	}
	if _, ok := all[serviceURL]; !ok {
		return nil
	}
	delete(all, serviceURL)
	return f.writeAll(all)
}

func (f *fileStore) List() ([]Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.readAll()
	if err != nil {
		return nil, err
	}
	out := make([]Credential, 0, len(all))
	for _, c := range all {
		out = append(out, c)
	}
	return out, nil
}

// readAll loads the on-disk map, treating a missing file as empty rather
// than an error (the common case on first run).
func (f *fileStore) readAll() (map[string]Credential, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Credential{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credstore: read credentials file: %w", err)
	}
	if len(b) == 0 {
		return map[string]Credential{}, nil
	}
	var all map[string]Credential
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("credstore: credentials file is corrupt: %w", err)
	}
	if all == nil {
		all = map[string]Credential{}
	}
	return all, nil
}

// writeAll writes the full credential map atomically (write to a temp file,
// then rename) so a crash mid-write can never leave a truncated or
// corrupted credentials file. The temp file is created with the final
// restrictive mode from the start, rather than chmod'd afterward, so the
// secret is never briefly world- or group-readable on disk.
func (f *fileStore) writeAll(all map[string]Credential) error {
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("credstore: encode credentials: %w", err)
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("credstore: write credentials file: %w", err)
	}
	// os.WriteFile applies the mode through umask, so enforce it explicitly.
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("credstore: restrict credentials file permissions: %w", err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("credstore: replace credentials file: %w", err)
	}
	return nil
}
