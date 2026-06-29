package transaction

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agentcarto/core/domain"
)

func TestValidate(t *testing.T) {
	d := t.TempDir()
	mk := func(p domain.MutationPlan) error { return Validate(p) }

	t.Run("empty plan rejected", func(t *testing.T) {
		if mk(domain.MutationPlan{AllowedRoots: []string{d}}) == nil {
			t.Error("empty plan must be rejected")
		}
	})
	t.Run("nested path accepted", func(t *testing.T) {
		p := filepath.Join(d, "deep", "nested", "x")
		if e := mk(domain.MutationPlan{AllowedRoots: []string{d}, Writes: []domain.FileWrite{{Path: p}}}); e != nil {
			t.Errorf("nested path under root should pass: %v", e)
		}
	})
	t.Run("sibling-prefix path is not inside root", func(t *testing.T) {
		// AllowedRoots is /.../root; "/.../root-evil" shares a string prefix but
		// is a different directory and must be rejected.
		root := filepath.Join(d, "root")
		evil := filepath.Join(d, "root-evil", "x")
		if mk(domain.MutationPlan{AllowedRoots: []string{root}, Writes: []domain.FileWrite{{Path: evil}}}) == nil {
			t.Error("sibling sharing a name prefix must be rejected")
		}
	})
	t.Run("move with destination outside root rejected", func(t *testing.T) {
		plan := domain.MutationPlan{
			AllowedRoots: []string{d},
			Moves:        []domain.PathMove{{From: filepath.Join(d, "a"), To: filepath.Join(d, "..", "escape")}},
		}
		if mk(plan) == nil {
			t.Error("move escaping the root must be rejected")
		}
	})
}

func TestApplyRollbackRestoresExisting(t *testing.T) {
	d := t.TempDir()
	good := filepath.Join(d, "a.txt")
	if err := os.WriteFile(good, []byte("orig"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Second write fails: its parent "blocker" is a file, so MkdirAll errors.
	blocker := filepath.Join(d, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "sub", "x.txt")

	plan := domain.MutationPlan{
		AllowedRoots: []string{d},
		Writes: []domain.FileWrite{
			{Path: good, Data: []byte("new"), Mode: 0o600},
			{Path: bad, Data: []byte("data"), Mode: 0o600},
		},
	}
	r, err := Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected the plan to fail on the second write")
	}
	// The key invariant: the completed first write is rolled back to its original
	// content. (Whether the never-written bad path also appears in RolledBack is
	// an OS-specific detail of when the failure surfaces, so only require good.)
	if b, _ := os.ReadFile(good); string(b) != "orig" {
		t.Errorf("first write was not rolled back: content = %q", b)
	}
	if !contains(r.RolledBack, good) {
		t.Errorf("RolledBack = %v, want it to include %s", r.RolledBack, good)
	}
	if !contains(r.Pending, bad) {
		t.Errorf("Pending = %v, want it to include %s", r.Pending, bad)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestApplyRollbackRemovesNewFile(t *testing.T) {
	d := t.TempDir()
	fresh := filepath.Join(d, "fresh.txt") // did not exist before
	blocker := filepath.Join(d, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "sub", "x.txt")

	plan := domain.MutationPlan{
		AllowedRoots: []string{d},
		Writes: []domain.FileWrite{
			{Path: fresh, Data: []byte("new")},
			{Path: bad, Data: []byte("data")},
		},
	}
	if _, err := Apply(context.Background(), plan); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Error("a newly created file must be removed on rollback")
	}
}

func TestApplyCancelledContext(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "x.txt")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan := domain.MutationPlan{AllowedRoots: []string{d}, Writes: []domain.FileWrite{{Path: p, Data: []byte("v")}}}
	_, err := Apply(ctx, plan)
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Error("no file should be written when context is already cancelled")
	}
}

func TestApplyDefaultMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes")
	}
	d := t.TempDir()
	p := filepath.Join(d, "x.txt")
	// Mode 0 must default to 0600.
	plan := domain.MutationPlan{AllowedRoots: []string{d}, Writes: []domain.FileWrite{{Path: p, Data: []byte("v")}}}
	if _, err := Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", st.Mode().Perm())
	}
}
