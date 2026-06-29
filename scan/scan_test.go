package scan

import (
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
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.jsonl")
	writeFile(t, p, "no sessions here")
	fp := Fingerprint(p)

	t.Run("skip unchanged dead path and carry it forward", func(t *testing.T) {
		c := New(nil, map[string]string{p: fp}, "v1")
		if !c.Skip(p) {
			t.Fatal("expected skip for unchanged dead path")
		}
		if c.DeadOut()[p] != fp {
			t.Error("skipped path was not carried into DeadOut")
		}
	})
	t.Run("do not skip changed dead path", func(t *testing.T) {
		c := New(nil, map[string]string{p: "stale-fingerprint"}, "v1")
		if c.Skip(p) {
			t.Error("expected no skip when fingerprint differs")
		}
	})
	t.Run("Dead records current fingerprint", func(t *testing.T) {
		c := New(nil, nil, "v1")
		c.Dead(p)
		if c.DeadOut()[p] != fp {
			t.Errorf("DeadOut[%s] = %q, want %q", p, c.DeadOut()[p], fp)
		}
	})
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
