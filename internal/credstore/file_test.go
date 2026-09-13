package credstore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func testCred(serviceURL, login string) Credential {
	return Credential{
		ServiceURL: serviceURL,
		Token:      "tok-" + login,
		Login:      login,
		UserID:     42,
		StoredAt:   time.Now().UTC().Truncate(time.Second),
	}
}

func TestFileStore_Backend(t *testing.T) {
	fs, err := openFileStoreDir(t.TempDir())
	if err != nil {
		t.Fatalf("openFileStoreDir: %v", err)
	}
	if got := fs.Backend(); got != "file" {
		t.Fatalf("Backend() = %q, want %q", got, "file")
	}
}

func TestFileStore_RoundTrip(t *testing.T) {
	fs, err := openFileStoreDir(t.TempDir())
	if err != nil {
		t.Fatalf("openFileStoreDir: %v", err)
	}
	in := testCred("https://ghstories.fly.dev", "alice")
	if err := fs.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := fs.Load(in.ServiceURL)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *out != in {
		t.Fatalf("round trip mismatch: got %+v, want %+v", *out, in)
	}
}

func TestFileStore_LoadMissingReturnsErrNotFound(t *testing.T) {
	fs, err := openFileStoreDir(t.TempDir())
	if err != nil {
		t.Fatalf("openFileStoreDir: %v", err)
	}
	if _, err := fs.Load("https://nope.example"); err != ErrNotFound {
		t.Fatalf("Load on empty store: got %v, want ErrNotFound", err)
	}
}

func TestFileStore_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on windows")
	}
	dir := t.TempDir()
	fs, err := openFileStoreDir(dir)
	if err != nil {
		t.Fatalf("openFileStoreDir: %v", err)
	}
	if err := fs.Save(testCred("https://ghstories.fly.dev", "alice")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config directory mode = %o, want 0700", perm)
	}

	fileInfo, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatalf("stat credentials file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials file mode = %o, want 0600", perm)
	}
}

func TestFileStore_MultiAccountAndDeleteKeepsOthers(t *testing.T) {
	fs, err := openFileStoreDir(t.TempDir())
	if err != nil {
		t.Fatalf("openFileStoreDir: %v", err)
	}

	alice := testCred("https://ghstories.fly.dev", "alice")
	bob := testCred("https://self-hosted.example", "bob")
	if err := fs.Save(alice); err != nil {
		t.Fatalf("Save alice: %v", err)
	}
	if err := fs.Save(bob); err != nil {
		t.Fatalf("Save bob: %v", err)
	}

	all, err := fs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List() returned %d credentials, want 2", len(all))
	}

	if err := fs.Delete(alice.ServiceURL); err != nil {
		t.Fatalf("Delete alice: %v", err)
	}

	if _, err := fs.Load(alice.ServiceURL); err != ErrNotFound {
		t.Fatalf("Load(alice) after delete: got %v, want ErrNotFound", err)
	}
	got, err := fs.Load(bob.ServiceURL)
	if err != nil {
		t.Fatalf("Load(bob) after deleting alice: %v", err)
	}
	if *got != bob {
		t.Fatalf("bob's credential changed after deleting alice: got %+v, want %+v", *got, bob)
	}

	remaining, err := fs.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(remaining) != 1 || remaining[0] != bob {
		t.Fatalf("List after delete = %+v, want only bob", remaining)
	}
}

func TestFileStore_DeleteMissingIsNotAnError(t *testing.T) {
	fs, err := openFileStoreDir(t.TempDir())
	if err != nil {
		t.Fatalf("openFileStoreDir: %v", err)
	}
	if err := fs.Delete("https://never-saved.example"); err != nil {
		t.Fatalf("Delete on missing entry: %v", err)
	}
}

func TestFileStore_SavePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	fs1, err := openFileStoreDir(dir)
	if err != nil {
		t.Fatalf("openFileStoreDir: %v", err)
	}
	cred := testCred("https://ghstories.fly.dev", "alice")
	if err := fs1.Save(cred); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fs2, err := openFileStoreDir(dir)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	got, err := fs2.Load(cred.ServiceURL)
	if err != nil {
		t.Fatalf("Load after reopen: %v", err)
	}
	if *got != cred {
		t.Fatalf("reopened store mismatch: got %+v, want %+v", *got, cred)
	}
}
