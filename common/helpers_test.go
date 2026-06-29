package common

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentcarto/core/domain"
)

func TestIDFromPath(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// A trailing segment of >=16 chars after the last "-" is treated as the
		// real id; the extension is stripped first.
		{"uuid suffix", "/x/rollout-2025-01-02-0123456789abcdef0.jsonl", "0123456789abcdef0"},
		{"exactly 16", "/x/foo-0123456789abcdef.txt", "0123456789abcdef"},
		{"short suffix kept whole", "/x/a-b.json", "a-b"},
		{"no dash", "/x/abcdef.json", "abcdef"},
		{"15-char suffix kept whole", "/x/foo-0123456789abcde.txt", "foo-0123456789abcde"},
		{"no extension", "/x/session-0123456789abcdef0", "0123456789abcdef0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IDFromPath(c.in); got != c.want {
				t.Errorf("IDFromPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCleanTitle(t *testing.T) {
	t.Run("collapses whitespace", func(t *testing.T) {
		if got := CleanTitle("  a\t b\n  c "); got != "a b c" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := CleanTitle(""); got != "" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("truncates to 200 runes (multibyte-safe)", func(t *testing.T) {
		got := CleanTitle(strings.Repeat("あ", 250))
		if n := len([]rune(got)); n != 200 {
			t.Errorf("rune length = %d, want 200", n)
		}
		// Must remain valid UTF-8 (not cut mid-rune).
		if strings.Contains(got, "�") {
			t.Error("truncation produced an invalid rune")
		}
	})
}

func TestTitleCandidate(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain", "do the thing", "do the thing"},
		{"user_query unwrapped", "<user_query>real ask</user_query>", "real ask"},
		{"command noise", "<command-name>/foo</command-name>", ""},
		{"caveat noise (case-insensitive)", "Caveat: the messages below", ""},
		{"system reminder after spaces", "   <system-reminder>x", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TitleCandidate(c.in); got != c.want {
				t.Errorf("TitleCandidate(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTitle(t *testing.T) {
	// The first user event with non-noise content wins; noise is skipped.
	events := []domain.Event{
		{Kind: domain.EventSystem, Text: "boot"},
		{Kind: domain.EventUser, Text: "<command-name>/init</command-name>"},
		{Kind: domain.EventUser, Text: "  build   the app  "},
	}
	if got := Title(events, "def"); got != "build the app" {
		t.Errorf("Title = %q, want %q", got, "build the app")
	}
	if got := Title(nil, "fallback"); got != "fallback" {
		t.Errorf("Title(nil) = %q, want fallback", got)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct{ in, want string }{
		{"~", home},
		{"~/x/y", filepath.Join(home, "x/y")},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
		{"~user", "~user"}, // ~otheruser is intentionally NOT expanded
	}
	for _, c := range cases {
		if got := ExpandHome(c.in); got != c.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestProcessMatches(t *testing.T) {
	s := domain.Session{SessionID: "sess-123"}
	s.SourceRef.Source = "/a/b"
	cases := []struct {
		name string
		ps   []domain.Process
		want bool
	}{
		{"exact open file", []domain.Process{{OpenFiles: []string{"/a/b"}}}, true},
		{"child open file", []domain.Process{{OpenFiles: []string{"/a/b/c.json"}}}, true},
		{"sibling prefix is not a match", []domain.Process{{OpenFiles: []string{"/a/bc"}}}, false},
		{"args carry session id", []domain.Process{{Args: []string{"--resume", "sess-123"}}}, true},
		{"no match", []domain.Process{{OpenFiles: []string{"/x"}, Args: []string{"y"}}}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProcessMatches(s, c.ps); got != c.want {
				t.Errorf("ProcessMatches = %v, want %v", got, c.want)
			}
		})
	}
}

func TestActiveStatus(t *testing.T) {
	cases := []struct {
		kind        domain.EventKind
		userRunning bool
		want        domain.Status
	}{
		{domain.EventTurnComplete, false, domain.StatusReady},
		{domain.EventReasoning, false, domain.StatusRunning},
		{domain.EventToolCall, false, domain.StatusRunning},
		{domain.EventStream, false, domain.StatusRunning},
		{domain.EventUser, true, domain.StatusRunning},
		{domain.EventUser, false, domain.StatusOther},
		{domain.EventSystem, false, domain.StatusOther},
	}
	for _, c := range cases {
		if got := ActiveStatus(c.kind, c.userRunning); got != c.want {
			t.Errorf("ActiveStatus(%v, %v) = %v, want %v", c.kind, c.userRunning, got, c.want)
		}
	}
}

func TestLastMeaningful(t *testing.T) {
	events := []domain.Event{
		{Kind: domain.EventUser},
		{Kind: domain.EventToolCall},
		{Kind: domain.EventMeta},
		{Kind: domain.EventSystem},
	}
	if got := LastMeaningful(events); got != domain.EventToolCall {
		t.Errorf("LastMeaningful = %v, want tool_call", got)
	}
	if got := LastMeaningful([]domain.Event{{Kind: domain.EventMeta}}); got != "" {
		t.Errorf("LastMeaningful(meta only) = %q, want empty", got)
	}
}

func TestMaxMTime(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	for name, mt := range map[string]time.Time{"a.txt": old, "b.txt": recent} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	if got := MaxMTime(dir); !got.Equal(recent) {
		t.Errorf("MaxMTime(dir) = %v, want %v (the newest file)", got, recent)
	}

	f := filepath.Join(dir, "a.txt")
	if got := MaxMTime(f); !got.Equal(old) {
		t.Errorf("MaxMTime(file) = %v, want its own mtime %v", got, old)
	}

	if got := MaxMTime(filepath.Join(dir, "missing")); !got.IsZero() {
		t.Errorf("MaxMTime(missing) = %v, want zero", got)
	}
}

func TestJSONLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log.jsonl")
	// line 2 is invalid JSON and must be skipped, but line numbering counts it.
	content := "{\"i\":1}\nnot json\n{\"i\":3}\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen []int
	err := JSONLines(context.Background(), p, func(line int, _ map[string]any) error {
		seen = append(seen, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 3 {
		t.Errorf("visited lines = %v, want [1 3]", seen)
	}

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := JSONLines(ctx, p, func(int, map[string]any) error { return nil })
		if err != context.Canceled {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if err := JSONLines(context.Background(), filepath.Join(dir, "nope"), func(int, map[string]any) error { return nil }); err == nil {
			t.Error("want error for missing file")
		}
	})
}

func TestRewriteJSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "in.jsonl")
	// A blank line is dropped; a non-JSON line is preserved verbatim.
	content := "{\"keep\":1}\n\nplain text\n{\"keep\":2}\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, changed, err := RewriteJSONL(p, func(o map[string]any) bool {
		if o["keep"] == float64(2) {
			o["keep"] = float64(20)
			return true
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Errorf("changed = %d, want 1", changed)
	}
	s := string(out)
	if !strings.Contains(s, "plain text") {
		t.Error("non-JSON line was not preserved")
	}
	if strings.Contains(s, "\n\n") {
		t.Error("blank line was not dropped")
	}
	if !strings.Contains(s, "\"keep\":20") {
		t.Errorf("mutation not applied: %s", s)
	}
}
