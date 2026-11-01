package artifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreCollectsSnapshotsAndVerifies(t *testing.T) {
	store, err := New(t.TempDir(), true, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := store.NewOutbox("reviewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outbox, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, "nested", "review.md"), []byte("finding"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, "evidence.bin"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	refs, err := store.Collect("reviewer", 1, outbox, []Claim{{RelPath: "nested/review.md", Kind: "review", Title: "Round 1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].ID == "" || refs[0].RuntimePath == "" {
		t.Fatalf("unexpected refs: %#v", refs)
	}
	if err := store.Verify(refs); err != nil {
		t.Fatalf("verify snapshots: %v", err)
	}
	if err := os.WriteFile(refs[0].RuntimePath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(refs); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("verify mutation error = %v", err)
	}
	resolved, err := store.Resolve([]string{refs[1].ID})
	if err != nil || len(resolved) != 1 || resolved[0].ID != refs[1].ID {
		t.Fatalf("resolve = %#v, %v", resolved, err)
	}
}

func TestOpenRestoresAndVerifiesExistingManifest(t *testing.T) {
	root := t.TempDir()
	store, err := New(root, true, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := store.NewOutbox("reviewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, "review.md"), []byte("finding"), 0o600); err != nil {
		t.Fatal(err)
	}
	refs, err := store.Collect("reviewer", 1, outbox, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, true, DefaultLimits())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer reopened.Close()
	resolved, err := reopened.Resolve([]string{refs[0].ID})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("Resolve() = %#v, %v", resolved, err)
	}
	if err := os.WriteFile(resolved[0].RuntimePath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, true, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "verify artifact manifest") {
		t.Fatalf("Open() after mutation error = %v", err)
	}
}

func TestStoreRejectsUnsafeClaimsAndFiles(t *testing.T) {
	store, err := New(t.TempDir(), true, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := store.NewOutbox("reviewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, "review.md"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Collect("reviewer", 1, outbox, []Claim{{RelPath: "../outside"}}); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("traversal error = %v", err)
	}
	if err := os.Symlink(filepath.Join(outbox, "review.md"), filepath.Join(outbox, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Collect("reviewer", 1, outbox, nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestStoreDoesNotCollectTheControlPlaneReceipt(t *testing.T) {
	store, err := New(t.TempDir(), true, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := store.NewOutbox("reviewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, ReceiptFileName), []byte(`{"version":1,"outcome":"success","summary":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, "review.md"), []byte("finding"), 0o600); err != nil {
		t.Fatal(err)
	}

	refs, err := store.Collect("reviewer", 1, outbox, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].OriginalName != "review.md" {
		t.Fatalf("refs = %#v, want only review.md", refs)
	}
	if _, err := os.Stat(filepath.Join(outbox, ReceiptFileName)); err != nil {
		t.Fatalf("receipt file was not left in place for the runner: %v", err)
	}
}

func TestStoreCollectIsIdempotentForAnUnchangedTurn(t *testing.T) {
	store, err := New(t.TempDir(), true, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := store.NewOutbox("reviewer", 5)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(outbox, "review.md")
	if err := os.WriteFile(path, []byte("finding"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := store.Collect("reviewer", 5, outbox, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Collect("reviewer", 5, outbox, nil)
	if err != nil {
		t.Fatalf("second collect = %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID || first[0].RuntimePath != second[0].RuntimePath {
		t.Fatalf("collect refs differ: first=%#v second=%#v", first, second)
	}
	if got := len(store.Manifest().Artifacts); got != 1 {
		t.Fatalf("manifest contains %d artifacts, want 1", got)
	}
	if err := os.WriteFile(path, []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Collect("reviewer", 5, outbox, nil); err == nil || !strings.Contains(err.Error(), "changed after snapshot") {
		t.Fatalf("mutated second collect error = %v, want snapshot mutation failure", err)
	}
}

func TestOpenRejectsUnusableStore(t *testing.T) {
	t.Run("empty root", func(t *testing.T) {
		if _, err := Open("", true, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "artifact root is required") {
			t.Fatalf("Open() error = %v", err)
		}
	})
	t.Run("invalid limits", func(t *testing.T) {
		if _, err := Open(t.TempDir(), true, Limits{}); err == nil || !strings.Contains(err.Error(), "invalid artifact limits") {
			t.Fatalf("Open() error = %v", err)
		}
	})
	t.Run("missing manifest", func(t *testing.T) {
		if _, err := Open(t.TempDir(), true, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "read artifact manifest") {
			t.Fatalf("Open() error = %v", err)
		}
	})
	t.Run("invalid manifest json", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(root, true, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "parse artifact manifest") {
			t.Fatalf("Open() error = %v", err)
		}
	})
	t.Run("unsupported manifest version", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"version":2,"run_id":"abc"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(root, true, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "invalid artifact manifest") {
			t.Fatalf("Open() error = %v", err)
		}
	})
	t.Run("missing run id", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"version":1,"run_id":""}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(root, true, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "invalid artifact manifest") {
			t.Fatalf("Open() error = %v", err)
		}
	})
}

func TestNewResolvesRelativeRootAndWritesManifest(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(t.TempDir(), "artifact-root")
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(relative) {
		t.Skip("temporary directory is not addressable with a relative path")
	}
	store, err := New(relative, true, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !filepath.IsAbs(store.Root()) {
		t.Fatalf("store root %q is not absolute", store.Root())
	}
	if store.Root() != root {
		t.Fatalf("store root = %q, want %q", store.Root(), root)
	}
	data, err := os.ReadFile(filepath.Join(store.Root(), "manifest.json"))
	if err != nil {
		t.Fatalf("read initial manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse initial manifest: %v", err)
	}
	if manifest.Version != 1 || manifest.RunID == "" {
		t.Fatalf("initial manifest = %#v", manifest)
	}
}
