package common

import (
	"context"
	"encoding/json"
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

func TestTitle(t *testing.T) {
	// The first user event carrying a plugin-normalized Prompt wins; pseudo
	// prompts (empty Prompt) and non-user events are skipped.
	events := []domain.Event{
		{Kind: domain.EventSystem, Text: "boot"},
		{Kind: domain.EventUser, Text: "<command-name>/init</command-name>"},
		{Kind: domain.EventUser, Text: "  build   the app  ", Prompt: "build the app"},
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
	// Use OS-native separators: ProcessMatches joins with filepath.Separator, so
	// paths must look the way they do on the running platform.
	src := filepath.FromSlash("/a/b")
	child := filepath.Join(src, "c.json")
	sibling := filepath.FromSlash("/a/bc")

	s := domain.Session{SessionID: "sess-123"}
	s.SourceRef.Source = src
	cases := []struct {
		name string
		ps   []domain.Process
		want bool
	}{
		{"exact open file", []domain.Process{{OpenFiles: []string{src}}}, true},
		{"child open file", []domain.Process{{OpenFiles: []string{child}}}, true},
		{"sibling prefix is not a match", []domain.Process{{OpenFiles: []string{sibling}}}, false},
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
		if o["keep"] == json.Number("2") {
			o["keep"] = 20
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

// Untouched lines must survive byte-for-byte: no float64 rounding of large
// integers, no HTML escaping, no key reordering. These logs belong to the
// user's agent, so any silent rewrite is data corruption.
func TestRewriteJSONLPreservesUntouchedLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "in.jsonl")
	untouched := `{"z":1,"a":9007199254740993,"html":"<tag> & co","nested":{"big":18446744073709551615}}`
	changedLine := `{"cwd":"/old","n":9007199254740993,"s":"<x>"}`
	if err := os.WriteFile(p, []byte(untouched+"\n"+changedLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, changed, err := RewriteJSONL(p, func(o map[string]any) bool {
		if o["cwd"] == "/old" {
			o["cwd"] = "/new"
			return true
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed = %d, want 1", changed)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if lines[0] != untouched {
		t.Errorf("untouched line rewritten:\n got %s\nwant %s", lines[0], untouched)
	}
	// The mutated line keeps big integers and angle brackets intact.
	if !strings.Contains(lines[1], "9007199254740993") {
		t.Errorf("big integer corrupted in mutated line: %s", lines[1])
	}
	if !strings.Contains(lines[1], "<x>") {
		t.Errorf("HTML-escaped mutated line: %s", lines[1])
	}
	if !strings.Contains(lines[1], `"cwd":"/new"`) {
		t.Errorf("mutation not applied: %s", lines[1])
	}
}

func TestRewriteJSONPreservesNumbersAndAngleBrackets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "in.json")
	if err := os.WriteFile(p, []byte(`{"info":{"cwd":"/old"},"big":9007199254740993,"s":"<x>"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// No change: original bytes come back untouched.
	out, changed, err := RewriteJSON(p, func(o map[string]any) bool { return false })
	if err != nil || changed {
		t.Fatalf("unexpected: changed=%v err=%v", changed, err)
	}
	if !strings.Contains(string(out), `"big":9007199254740993`) {
		t.Errorf("no-op rewrite corrupted file: %s", out)
	}
	out, changed, err = RewriteJSON(p, func(o map[string]any) bool {
		Map(o["info"])["cwd"] = "/new"
		return true
	})
	if err != nil || !changed {
		t.Fatalf("unexpected: changed=%v err=%v", changed, err)
	}
	if !strings.Contains(string(out), "9007199254740993") || !strings.Contains(string(out), "<x>") {
		t.Errorf("rewrite corrupted numbers or escaping: %s", out)
	}
}

// A line larger than any fixed scanner buffer must not abort the walk: the
// old bufio.Scanner-based implementation stopped at the first >16MB line and
// silently dropped the rest of the session.
func TestJSONLinesHandlesOversizedLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "in.jsonl")
	big := `{"pad":"` + strings.Repeat("x", 17<<20) + `"}`
	content := `{"n":1}` + "\n" + big + "\n" + `{"n":2}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := 0
	err := JSONLines(context.Background(), p, func(_ int, o map[string]any) error {
		if _, ok := o["n"]; ok {
			got++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("lines after the oversized one were dropped: got %d JSON lines with n, want 2", got)
	}
}
