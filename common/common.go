package common

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/agentcarto/core/domain"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func DecodeOptions(n *yaml.Node, dst any) error {
	if n == nil || n.Kind == 0 {
		return nil
	}
	b, e := yaml.Marshal(n)
	if e != nil {
		return e
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	return d.Decode(dst)
}
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(p, "~/"))
	}
	return p
}
func JSONLines(ctx context.Context, path string, fn func(int, map[string]any) error) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64<<10), 16<<20)
	line := 0
	for s.Scan() {
		line++
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var v map[string]any
		if json.Unmarshal(s.Bytes(), &v) != nil {
			continue
		}
		if e := fn(line, v); e != nil {
			return e
		}
	}
	return s.Err()
}
func String(v any) string      { s, _ := v.(string); return s }
func Map(v any) map[string]any { m, _ := v.(map[string]any); return m }
func Slice(v any) []any        { a, _ := v.([]any); return a }
func Text(v any) string {
	switch x := v.(type) {
	case nil:
		// A missing field (nil) becomes an empty string. This avoids
		// json.Marshal(nil) returning "null" and leaking into places such as
		// the tail of a THINK line.
		return ""
	case string:
		return x
	case []any:
		var b []string
		for _, p := range x {
			m := Map(p)
			if s := String(m["text"]); s != "" {
				b = append(b, s)
			}
		}
		return strings.Join(b, "\n")
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
func Time(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func FileTime(path string) time.Time {
	st, e := os.Stat(path)
	if e != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// MaxMTime returns the modification time of path: for a file, its own mtime;
// for a directory, the maximum mtime among all files beneath it. It is used to
// derive the update time of directory-based sessions (grok/copilot).
func MaxMTime(path string) time.Time {
	st, e := os.Stat(path)
	if e != nil {
		return time.Time{}
	}
	if !st.IsDir() {
		return st.ModTime()
	}
	best := time.Time{}
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, e := d.Info(); e == nil && fi.ModTime().After(best) {
			best = fi.ModTime()
		}
		return nil
	})
	if best.IsZero() {
		best = st.ModTime()
	}
	return best
}
func Fingerprint(path string) string {
	st, e := os.Stat(path)
	if e != nil {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", filepath.Clean(path), st.Size(), st.ModTime().UnixNano())
}
func IDFromPath(path string) string {
	b := filepath.Base(path)
	b = strings.TrimSuffix(b, filepath.Ext(b))
	if i := strings.LastIndex(b, "-"); i >= 0 && len(b)-i > 16 {
		return b[i+1:]
	}
	return b
}
func WalkFiles(root string, accept func(string) bool) ([]string, error) {
	var out []string
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			if p == root && os.IsNotExist(e) {
				return nil
			}
			return nil
		}
		if !d.IsDir() && accept(p) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, e
}
func Linear(events []domain.Event) domain.Conversation {
	nodes := make([]domain.ConvNode, 0, len(events))
	parent := ""
	for i, e := range events {
		id := fmt.Sprintf("event-%08d", i)
		nodes = append(nodes, domain.ConvNode{ID: id, Parent: parent, Timestamp: e.Timestamp, Events: []domain.Event{e}})
		parent = id
	}
	return domain.NewConversation(nodes)
}
func LastMeaningful(events []domain.Event) domain.EventKind {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != domain.EventMeta && events[i].Kind != domain.EventSystem {
			return events[i].Kind
		}
	}
	return ""
}

// CleanTitle collapses the whitespace in a title candidate and truncates it to
// 200 characters.
func CleanTitle(t string) string {
	if t == "" {
		return ""
	}
	t = strings.Join(strings.Fields(t), " ")
	if len([]rune(t)) > 200 {
		t = string([]rune(t)[:200])
	}
	return t
}

// noiseTitlePrefixes lists system-injected preambles that begin text which is
// unsuitable as a title.
var noiseTitlePrefixes = []string{
	"<command-name>", "<command-message>", "<environment_context>",
	"<user_info>", "<local-command", "<system-reminder>", "<bash-input>",
	"<bash-stdout>", "caveat:", "<context-",
	"# agents.md instructions", "agents.md instructions",
}

func isNoiseTitle(t string) bool {
	s := strings.ToLower(strings.TrimSpace(t))
	for _, p := range noiseTitlePrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

var userQueryRE = regexp.MustCompile(`(?s)<user_query>(.*?)</user_query>`)

// TitleCandidate extracts, from a user message, the real content that can serve
// as a listing title. If the text is wrapped in <user_query>, its inner content
// is returned; if it starts with a noise preamble, an empty string is returned.
func TitleCandidate(t string) string {
	if t == "" {
		return ""
	}
	s := strings.TrimSpace(t)
	if m := userQueryRE.FindStringSubmatch(s); m != nil && strings.TrimSpace(m[1]) != "" {
		return strings.TrimSpace(m[1])
	}
	if isNoiseTitle(s) {
		return ""
	}
	return s
}

// Title uses the first non-noise user message as the listing title, falling
// back to def when there is none.
func Title(events []domain.Event, def string) string {
	for _, e := range events {
		if e.Kind == domain.EventUser {
			if cand := TitleCandidate(e.Text); cand != "" {
				return CleanTitle(cand)
			}
		}
	}
	return def
}
func ReadAll(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 64<<20))
}

func RewriteJSONL(path string, mutate func(map[string]any) bool) ([]byte, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var out bytes.Buffer
	changed := 0
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var o map[string]any
		if json.Unmarshal(line, &o) != nil {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		if mutate(o) {
			changed++
		}
		enc, _ := json.Marshal(o)
		out.Write(enc)
		out.WriteByte('\n')
	}
	return out.Bytes(), changed, nil
}
func RewriteJSON(path string, mutate func(map[string]any) bool) ([]byte, bool, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, false, e
	}
	var o map[string]any
	if e = json.Unmarshal(b, &o); e != nil {
		return nil, false, e
	}
	changed := mutate(o)
	if !changed {
		return b, false, nil
	}
	b, e = json.MarshalIndent(o, "", "  ")
	if e == nil {
		b = append(b, '\n')
	}
	return b, true, e
}
func NewID() string { return uuid.NewString() }
func ProcessMatches(s domain.Session, ps []domain.Process) bool {
	for _, p := range ps {
		for _, f := range p.OpenFiles {
			if f == s.SourceRef.Source || strings.HasPrefix(f, s.SourceRef.Source+string(filepath.Separator)) {
				return true
			}
		}
		for _, a := range p.Args {
			if a == s.SessionID {
				return true
			}
		}
	}
	return false
}
func ActiveStatus(kind domain.EventKind, userRunning bool) domain.Status {
	switch kind {
	case domain.EventTurnComplete:
		return domain.StatusReady
	case domain.EventReasoning, domain.EventToolCall, domain.EventToolResult, domain.EventStream:
		return domain.StatusRunning
	case domain.EventUser:
		if userRunning {
			return domain.StatusRunning
		}
	}
	return domain.StatusOther
}
