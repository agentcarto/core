package transaction

import (
	"context"
	"github.com/agentcarto/core/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyAtomicWrite(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "x")
	_ = os.WriteFile(p, []byte("old"), 0600)
	plan := domain.MutationPlan{AllowedRoots: []string{d}, Writes: []domain.FileWrite{{Path: p, Data: []byte("new"), Mode: 0600}}}
	if _, e := Apply(context.Background(), plan); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "new" {
		t.Fatal(string(b))
	}
}

// When to does not exist, from is renamed wholesale.
func TestMoveMergeRenameWhole(t *testing.T) {
	d := t.TempDir()
	from := filepath.Join(d, "old")
	to := filepath.Join(d, "new")
	_ = os.MkdirAll(from, 0700)
	_ = os.WriteFile(filepath.Join(from, "a.jsonl"), []byte("x"), 0600)
	var r domain.MutationResult
	if e := moveMerge(from, to, &r); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(from); !os.IsNotExist(e) {
		t.Fatal("from should be gone")
	}
	if _, e := os.Stat(filepath.Join(to, "a.jsonl")); e != nil {
		t.Fatal("file should be moved")
	}
}

// When to exists, entries are merged flatly; name collisions are skipped with a
// warning and their residue is left behind.
func TestMoveMergeFlatAndCollision(t *testing.T) {
	d := t.TempDir()
	from := filepath.Join(d, "old")
	to := filepath.Join(d, "new")
	// A colliding subdirectory, given contents to confirm it is not merged recursively.
	_ = os.MkdirAll(filepath.Join(from, "sub"), 0700)
	_ = os.WriteFile(filepath.Join(from, "sub", "inner.txt"), []byte("from"), 0600)
	_ = os.WriteFile(filepath.Join(from, "fresh.jsonl"), []byte("x"), 0600)
	_ = os.MkdirAll(filepath.Join(to, "sub"), 0700)

	var r domain.MutationResult
	if e := moveMerge(from, to, &r); e != nil {
		t.Fatal(e)
	}
	// Non-colliding entries have been moved.
	if _, e := os.Stat(filepath.Join(to, "fresh.jsonl")); e != nil {
		t.Fatal("non-colliding entry should be moved")
	}
	// The colliding sub is not merged recursively and stays behind in from (residue).
	if _, e := os.Stat(filepath.Join(from, "sub", "inner.txt")); e != nil {
		t.Fatal("colliding subdir must NOT be merged recursively; residue stays in from")
	}
	if _, e := os.Stat(filepath.Join(to, "sub", "inner.txt")); !os.IsNotExist(e) {
		t.Fatal("colliding subdir contents must not be merged into to")
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("expected 1 collision warning, got %v", r.Warnings)
	}
	// from is not empty, so it is not removed and remains.
	if _, e := os.Stat(from); e != nil {
		t.Fatal("non-empty from should remain")
	}
}

func TestRejectOutsideRoot(t *testing.T) {
	d := t.TempDir()
	if Validate(domain.MutationPlan{AllowedRoots: []string{d}, Writes: []domain.FileWrite{{Path: filepath.Join(d, "..", "x"), Data: []byte("x")}}}) == nil {
		t.Fatal("expected rejection")
	}
}
