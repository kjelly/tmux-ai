package artifact

import (
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
