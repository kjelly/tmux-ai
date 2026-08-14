package artifact

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fileStore struct {
	root     string
	keep     bool
	limits   Limits
	manifest Manifest
}

func New(root string, keep bool, limits Limits) (Store, error) {
	if limits.MaxFilesPerTurn <= 0 || limits.MaxFileBytes < 0 || limits.MaxTurnBytes < 0 || limits.MaxRunFiles <= 0 || limits.MaxRunBytes < 0 {
		return nil, fmt.Errorf("invalid artifact limits")
	}
	var err error
	if root == "" {
		root, err = os.MkdirTemp("", "tmux-ai-artifacts-")
	} else {
		err = os.MkdirAll(root, 0o700)
	}
	if err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "objects"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "rounds"), 0o700); err != nil {
		return nil, err
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return &fileStore{root: root, keep: keep, limits: limits, manifest: Manifest{Version: 1, RunID: hex.EncodeToString(b)}}, nil
}

func (s *fileStore) Root() string { return s.root }

func (s *fileStore) NewOutbox(memberID string, round int) (string, error) {
	memberID = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, memberID)
	if memberID == "" || round <= 0 {
		return "", fmt.Errorf("invalid outbox member or round")
	}
	path := filepath.Join(s.root, "rounds", fmt.Sprintf("%04d-%s", round, memberID), "outbox")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func (s *fileStore) Collect(memberID string, round int, outbox string, claims []Claim) ([]Ref, error) {
	abs, err := filepath.Abs(outbox)
	if err != nil {
		return nil, err
	}
	rootOutbox, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve outbox: %w", err)
	}
	claimByPath := make(map[string]Claim, len(claims))
	for _, claim := range claims {
		path, err := validateRelativePath(claim.RelPath)
		if err != nil {
			return nil, err
		}
		if _, ok := claimByPath[path]; ok {
			return nil, fmt.Errorf("duplicate artifact claim %q", claim.RelPath)
		}
		kind, err := validateMetadata(claim.Kind, "kind")
		if err != nil {
			return nil, err
		}
		title, err := validateMetadata(claim.Title, "title")
		if err != nil {
			return nil, err
		}
		claim.RelPath, claim.Kind, claim.Title = path, kind, title
		claimByPath[path] = claim
	}
	var files []string
	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == abs {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact %q is a symlink", rel)
		}
		if d.IsDir() {
			return nil
		}
		// The receipt is control-plane state the runner asked for; it must not
		// be snapshotted, counted against artifact limits, or relayed onwards.
		if rel == ReceiptFileName || rel == TaskFileName {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact %q is not a regular file", rel)
		}
		if info.Mode().Perm() == 0 {
			return fmt.Errorf("artifact %q is unreadable", rel)
		}
		if sysNlink(info) > 1 {
			return fmt.Errorf("artifact %q has multiple hard links", rel)
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if real != rootOutbox && !strings.HasPrefix(real, rootOutbox+string(filepath.Separator)) {
			return fmt.Errorf("artifact %q escapes outbox", rel)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for path := range claimByPath {
		found := false
		for _, file := range files {
			if file == path {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("claimed artifact %q was not found", path)
		}
	}
	// A caller may need to retry transport after a source turn completed. The
	// source outbox is immutable evidence at that point, so collecting the
	// exact same turn again must reuse the original snapshots rather than try
	// to create the same object names a second time.
	if existing := s.refsForTurn(memberID, round); len(existing) > 0 {
		if err := verifyCollectedTurn(abs, files, existing); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if len(files) > s.limits.MaxFilesPerTurn {
		return nil, fmt.Errorf("artifact count %d exceeds per-turn limit %d", len(files), s.limits.MaxFilesPerTurn)
	}
	var refs []Ref
	var turnBytes int64
	for _, rel := range files {
		path := filepath.Join(abs, rel)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Size() > s.limits.MaxFileBytes {
			return nil, fmt.Errorf("artifact %q exceeds per-file limit", rel)
		}
		turnBytes += info.Size()
		if turnBytes > s.limits.MaxTurnBytes {
			return nil, fmt.Errorf("artifacts exceed per-turn byte limit")
		}
		if len(s.manifest.Artifacts)+len(refs)+1 > s.limits.MaxRunFiles {
			return nil, fmt.Errorf("artifacts exceed run file limit")
		}
		if s.runBytes()+turnBytes > s.limits.MaxRunBytes {
			return nil, fmt.Errorf("artifacts exceed run byte limit")
		}
		sum, n, err := hashFile(path)
		if err != nil {
			return nil, err
		}
		if n != info.Size() {
			return nil, fmt.Errorf("artifact %q changed while collecting", rel)
		}
		id := fmt.Sprintf("a-r%04d-%02d-%s", round, len(refs)+1, sum[:8])
		object := id
		dst := filepath.Join(s.root, "objects", object)
		if err := copyFile(path, dst); err != nil {
			return nil, fmt.Errorf("snapshot artifact %q: %w", rel, err)
		}
		dstSum, dstN, err := hashFile(dst)
		if err != nil || dstN != n || dstSum != sum {
			_ = os.Remove(dst)
			return nil, fmt.Errorf("verify snapshot %q failed", rel)
		}
		claim := claimByPath[rel]
		refs = append(refs, Ref{ID: id, ProducerID: memberID, Round: round, OriginalName: rel, Kind: claim.Kind, Title: claim.Title, MediaType: mediaType(path), SizeBytes: n, SHA256: sum, ObjectName: object, RuntimePath: dst})
	}
	s.manifest.Artifacts = append(s.manifest.Artifacts, refs...)
	if err := s.writeManifest(); err != nil {
		return nil, err
	}
	return refs, nil
}

func (s *fileStore) refsForTurn(memberID string, round int) []Ref {
	var refs []Ref
	for _, ref := range s.manifest.Artifacts {
		if ref.ProducerID == memberID && ref.Round == round {
			ref.RuntimePath = filepath.Join(s.root, "objects", ref.ObjectName)
			refs = append(refs, ref)
		}
	}
	return refs
}

func verifyCollectedTurn(outbox string, files []string, refs []Ref) error {
	sort.Strings(files)
	if len(files) != len(refs) {
		return fmt.Errorf("artifact outbox changed after snapshot")
	}
	for i, ref := range refs {
		if files[i] != ref.OriginalName {
			return fmt.Errorf("artifact outbox changed after snapshot")
		}
		sum, n, err := hashFile(filepath.Join(outbox, files[i]))
		if err != nil {
			return err
		}
		if n != ref.SizeBytes || sum != ref.SHA256 {
			return fmt.Errorf("artifact %s changed after snapshot", ref.ID)
		}
	}
	return nil
}

func (s *fileStore) Resolve(ids []string) ([]Ref, error) {
	byID := make(map[string]Ref, len(s.manifest.Artifacts))
	for _, ref := range s.manifest.Artifacts {
		byID[ref.ID] = ref
	}
	refs := make([]Ref, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("duplicate artifact id %q", id)
		}
		seen[id] = true
		ref, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown artifact id %q", id)
		}
		ref.RuntimePath = filepath.Join(s.root, "objects", ref.ObjectName)
		refs = append(refs, ref)
	}
	return refs, nil
}

func (s *fileStore) Verify(refs []Ref) error {
	for _, ref := range refs {
		path := filepath.Join(s.root, "objects", ref.ObjectName)
		sum, n, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("verify artifact %s: %w", ref.ID, err)
		}
		if n != ref.SizeBytes || sum != ref.SHA256 {
			return fmt.Errorf("artifact %s changed after snapshot", ref.ID)
		}
	}
	return nil
}
func (s *fileStore) Manifest() Manifest {
	result := s.manifest
	result.Artifacts = append([]Ref(nil), s.manifest.Artifacts...)
	return result
}
func (s *fileStore) Close() error {
	if s.keep {
		return nil
	}
	return os.RemoveAll(s.root)
}
func (s *fileStore) runBytes() int64 {
	var n int64
	for _, ref := range s.manifest.Artifacts {
		n += ref.SizeBytes
	}
	return n
}
func (s *fileStore) writeManifest() error {
	data, err := json.MarshalIndent(s.manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.root, "manifest.json"), data, 0o600)
}
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}
func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
func mediaType(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	b := make([]byte, 512)
	n, _ := f.Read(b)
	detected := http.DetectContentType(b[:n])
	if detected == "application/octet-stream" {
		if byExt := mime.TypeByExtension(filepath.Ext(path)); byExt != "" {
			return byExt
		}
	}
	return detected
}
