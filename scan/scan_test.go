package scan

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentcarto/core/domain"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func session(source, pv, fp string) domain.Session {
	s := domain.Session{ParserVersion: pv, Fingerprint: fp}
	s.SourceRef.Source = source
	return s
}

func TestNewClearsVolatileAndSkipsEmptySource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	writeFile(t, p, "x")
	fp := Fingerprint(p)

	warm := []domain.Session{
		session(p, "v1", fp),
		session("", "v1", "ignored"), // empty source must be dropped
	}
	warm[0].Status = domain.StatusRunning
	warm[0].PermissionWait = true

	c := New(warm, nil, "v1")
	got, ok := c.Reuse(p)
	if !ok {
		t.Fatal("expected reuse hit for unchanged file")
	}
	// DetectActive recomputes these, so New must clear them.
	if got.Status != "" || got.PermissionWait {
		t.Errorf("volatile fields not cleared: status=%q wait=%v", got.Status, got.PermissionWait)
	}
}

func TestReuse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	writeFile(t, p, "hello")
	fp := Fingerprint(p)

	t.Run("hit when version and fingerprint match", func(t *testing.T) {
		c := New([]domain.Session{session(p, "v2", fp)}, nil, "v2")
		if _, ok := c.Reuse(p); !ok {
			t.Error("expected hit")
		}
	})
	t.Run("miss on parser version change", func(t *testing.T) {
		c := New([]domain.Session{session(p, "v1", fp)}, nil, "v2")
		if _, ok := c.Reuse(p); ok {
			t.Error("expected miss after ParserVersion bump")
		}
	})
	t.Run("miss when file changed", func(t *testing.T) {
		c := New([]domain.Session{session(p, "v2", fp)}, nil, "v2")
		future := time.Now().Add(time.Hour)
		if err := os.Chtimes(p, future, future); err != nil {
			t.Fatal(err)
		}
		if _, ok := c.Reuse(p); ok {
			t.Error("expected miss after mtime change")
		}
	})
	t.Run("miss when not in warm", func(t *testing.T) {
		c := New(nil, nil, "v2")
		if _, ok := c.Reuse(p); ok {
			t.Error("expected miss for unknown path")
		}
	})
}

func TestSkipAndDead(t *testing.T) {
	// An excluded log and the negative cache a scan left behind for it.
	excluded := func(t *testing.T, pv string) (string, map[string]string) {
		t.Helper()
		p := filepath.Join(t.TempDir(), "empty.jsonl")
		writeFile(t, p, "no sessions here")
		c := New(nil, nil, pv)
		c.Dead(p)
		return p, c.DeadOut()
	}

	t.Run("skip unchanged dead path and carry it forward", func(t *testing.T) {
		p, dead := excluded(t, "v1")
		c := New(nil, dead, "v1")
		if !c.Skip(p) {
			t.Fatal("expected skip for unchanged dead path")
		}
		if !maps.Equal(c.DeadOut(), dead) {
			t.Errorf("DeadOut = %v, want the entry carried forward: %v", c.DeadOut(), dead)
		}
	})
	t.Run("do not skip changed dead path", func(t *testing.T) {
		p, dead := excluded(t, "v1")
		writeFile(t, p, "a conversation now")
		c := New(nil, dead, "v1")
		if c.Skip(p) {
			t.Error("expected no skip when the file changed")
		}
		if len(c.DeadOut()) != 0 {
			t.Errorf("DeadOut = %v, want the stale entry dropped", c.DeadOut())
		}
	})
	t.Run("do not skip after the parser changed", func(t *testing.T) {
		p, dead := excluded(t, "v1")
		c := New(nil, dead, "v2")
		if c.Skip(p) {
			t.Error("expected no skip: a new parser may accept this log")
		}
		if len(c.DeadOut()) != 0 {
			t.Errorf("DeadOut = %v, want the stale entry dropped", c.DeadOut())
		}
	})
	t.Run("do not skip an entry that names no parser", func(t *testing.T) {
		p, _ := excluded(t, "v1")
		c := New(nil, map[string]string{p: Fingerprint(p)}, "v1")
		if c.Skip(p) {
			t.Error("expected no skip for an entry written before the parser was recorded")
		}
	})
	t.Run("do not skip a path that was never excluded", func(t *testing.T) {
		p, _ := excluded(t, "v1")
		c := New(nil, nil, "v1")
		if c.Skip(p) {
			t.Error("expected no skip without an entry")
		}
	})
}

func TestCompositeFingerprintInvalidation(t *testing.T) {
	dir := t.TempDir()
	parent, review := filepath.Join(dir, "parent"), filepath.Join(dir, "review")
	writeFile(t, parent, "question")
	writeFile(t, review, "pending")
	// The plugin chooses how to combine its dependencies.
	combined := func() string { return Fingerprint(parent) + ":" + Fingerprint(review) }
	original := combined()
	s := session(parent, "", "")
	New(nil, nil, "v1").StampWithFingerprint(&s, original)
	if s.ParserVersion != "v1" || s.Fingerprint != original {
		t.Fatalf("incorrect composite stamp: %+v", s)
	}
	for _, tc := range []struct {
		name, version, fingerprint string
		want                       bool
	}{
		{"unchanged", "v1", original, true},
		{"parser changed", "v2", original, false},
		{"dependency added", "v1", original + ":another", false},
		{"dependency removed", "v1", Fingerprint(parent), false},
		{"unknown fingerprint", "v1", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New([]domain.Session{s}, nil, tc.version)
			if _, ok := c.ReuseWithFingerprint(parent, tc.fingerprint); ok != tc.want {
				t.Fatalf("reuse=%v want %v", ok, tc.want)
			}
		})
	}
	writeFile(t, review, "completed: allow")
	c := New([]domain.Session{s}, nil, "v1")
	if _, ok := c.ReuseWithFingerprint(parent, combined()); ok {
		t.Fatal("review-only update reused a stale parent")
	}
	if _, ok := c.ReuseWithFingerprint("missing", original); ok {
		t.Fatal("unknown session reused")
	}
	c.StampWithFingerprint(&s, "replacement")
	if s.Fingerprint != original {
		t.Fatal("already-stamped session overwritten")
	}
}

func TestStamp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	writeFile(t, p, "data")
	c := New(nil, nil, "v9")

	s := session(p, "", "")
	c.Stamp(&s)
	if s.ParserVersion != "v9" || s.Fingerprint != Fingerprint(p) {
		t.Errorf("Stamp did not fill fields: pv=%q fp=%q", s.ParserVersion, s.Fingerprint)
	}

	// Stamp must not overwrite a session that already carries a version
	// (e.g. one returned by Reuse).
	pre := session(p, "old", "old-fp")
	c.Stamp(&pre)
	if pre.ParserVersion != "old" || pre.Fingerprint != "old-fp" {
		t.Error("Stamp overwrote an already-stamped session")
	}
}

func TestFingerprint(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFile(t, p, "abc")

	first := Fingerprint(p)
	if first == "" {
		t.Fatal("empty fingerprint")
	}
	if Fingerprint(p) != first {
		t.Error("fingerprint not stable across calls on an unchanged file")
	}

	t.Run("changes when file content/size changes", func(t *testing.T) {
		writeFile(t, p, "abcdef")
		if Fingerprint(p) == first {
			t.Error("fingerprint unchanged after content change")
		}
	})

	t.Run("directory fingerprint reflects its contents", func(t *testing.T) {
		sub := filepath.Join(dir, "tree")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(sub, "a"), "1")
		before := Fingerprint(sub)
		writeFile(t, filepath.Join(sub, "b"), "2")
		if Fingerprint(sub) == before {
			t.Error("directory fingerprint did not change after adding a file")
		}
	})

	t.Run("falls back to base path before #fragment", func(t *testing.T) {
		base := filepath.Join(dir, "base")
		writeFile(t, base, "payload")
		if Fingerprint(base+"#frag") != Fingerprint(base) {
			t.Error("expected #fragment path to fingerprint the same as its base file")
		}
	})
}
