package common

import (
	"testing"

	"github.com/agentcarto/core/domain"
)

func TestIsBareSlashCommand(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/compact", true},
		{" /clear ", true},
		{"/compact but keep every design note from this session", false},
		{"/multi\nline", false},
		{"not a command", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsBareSlashCommand(c.in); got != c.want {
			t.Errorf("IsBareSlashCommand(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToolArgFromJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"pattern":"needle","path":"/repo"}`, "/repo"},
		{`{"description":"  list files  "}`, "list files"},
		{`{"count":3}`, ""},
		{"not json", ""},
	}
	for _, c := range cases {
		if got := ToolArgFromJSON(c.in); got != c.want {
			t.Errorf("ToolArgFromJSON(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitPatch(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n*** Add File: b.txt\n+one\n+two\n*** End Patch"
	cs := SplitPatch(patch)
	if len(cs) != 2 {
		t.Fatalf("changes=%+v", cs)
	}
	if cs[0].Path != "a.go" || cs[0].Op != "update" || cs[0].Added != 1 || cs[0].Removed != 1 {
		t.Fatalf("cs[0]=%+v", cs[0])
	}
	if cs[1].Path != "b.txt" || cs[1].Op != "add" || cs[1].Added != 2 {
		t.Fatalf("cs[1]=%+v", cs[1])
	}
}

func TestLastMeaningfulSkipsTaskNotices(t *testing.T) {
	events := []domain.Event{
		{Kind: domain.EventAssistant},
		{Kind: domain.EventTask},
		{Kind: domain.EventMeta},
	}
	if got := LastMeaningful(events); got != domain.EventAssistant {
		t.Fatalf("LastMeaningful=%q, want assistant", got)
	}
}

// Agents disagree on the case of the same field. Copilot sends filePath where
// Claude sends file_path, and a tool call whose argument is not recognized
// renders as a bare name: no path on screen, nothing for a search to match.
func TestToolArgFromJSONAcceptsBothSpellings(t *testing.T) {
	cases := map[string]string{
		`{"file_path":"a.go"}`:        "a.go",
		`{"filePath":"b.go"}`:         "b.go",
		`{"notebookPath":"n.ipynb"}`:  "n.ipynb",
		`{"relativePath":"sub/c.go"}`: "sub/c.go",
		`{"toolName":"read_file"}`:    "", // not an argument
		`not json`:                    "",
	}
	for in, want := range cases {
		if got := ToolArgFromJSON(in); got != want {
			t.Errorf("ToolArgFromJSON(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestQuestionText(t *testing.T) {
	cases := []struct {
		name string
		in   []Question
		want string
	}{
		{
			"header, text and options",
			[]Question{{Header: "Scope", Text: "Which chapters?", Options: []Option{
				{Label: "All (Recommended)", Description: "Rewrite every chapter."},
				{Label: "First only"},
			}}},
			"Scope: Which chapters?\n- All (Recommended): Rewrite every chapter.\n- First only",
		},
		{
			"multi-select is marked on the question line",
			[]Question{{Header: "Kinds", Text: "Which checks?", Multi: true, Options: []Option{{Label: "proofread"}}}},
			"Kinds: Which checks? (multi-select)\n- proofread",
		},
		{
			"a preview follows its option, indented, keeping its own layout",
			[]Question{{Header: "Layout", Text: "Which one?", Options: []Option{
				{Label: "Wide", Description: "Two columns.", Preview: "\n+---+---+\n| a | b |\n\n  note\n"},
				{Label: "Narrow"},
			}}},
			"Layout: Which one?\n- Wide: Two columns.\n    +---+---+\n    | a | b |\n    \n      note\n- Narrow",
		},
		{
			"no header leaves the question text alone",
			[]Question{{Text: "  Anything else?  "}},
			"Anything else?",
		},
		{
			"questions are separated by a blank line",
			[]Question{
				{Header: "A", Text: "First?", Options: []Option{{Label: "yes"}}},
				{Header: "B", Text: "Second?"},
			},
			"A: First?\n- yes\n\nB: Second?",
		},
		{
			"a question or an option with nothing to show leaves no empty line behind",
			[]Question{
				{Multi: true},
				{Header: "A", Text: "First?", Options: []Option{{}, {Label: "yes"}, {Preview: "orphan"}}},
			},
			"A: First?\n- yes",
		},
		{"none", nil, ""},
	}
	for _, c := range cases {
		if got := QuestionText(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestAnswerText(t *testing.T) {
	cases := []struct {
		name string
		in   []Answer
		want string
	}{
		{
			"one line per question, several picks joined",
			[]Answer{
				{Header: "Scope", Values: []string{"All"}},
				{Header: "Kinds", Values: []string{"proofread", "verify"}},
			},
			"Scope: All\nKinds: proofread, verify",
		},
		{
			"a note sits under its answer, indented on every line",
			[]Answer{
				{Header: "Scope", Values: []string{"None of the above"}, Note: "skip the timeline\nand the index"},
				{Header: "Kinds", Values: []string{"verify"}},
			},
			"Scope: None of the above\n  note: skip the timeline\n  and the index\nKinds: verify",
		},
		{
			"an unanswered question says so",
			[]Answer{{Header: "Scope"}, {Header: "Kinds", Values: []string{""}}},
			"Scope: (no answer)\nKinds: (no answer)",
		},
		{
			"blank picks are dropped, not joined",
			[]Answer{{Header: "Kinds", Values: []string{"", " proofread ", " ", "verify"}}},
			"Kinds: proofread, verify",
		},
		{
			"no header leaves the value alone",
			[]Answer{{Values: []string{"typed reply"}}},
			"typed reply",
		},
		{"none", nil, ""},
	}
	for _, c := range cases {
		if got := AnswerText(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// An answer names its question by the header, and by the question itself when
// the agent gave it none, so the answer line still reads on its own.
func TestQuestionLabelFallsBackToText(t *testing.T) {
	if got := (Question{Header: " Scope ", Text: "Which chapters?"}).Label(); got != "Scope" {
		t.Errorf("Label = %q, want the header", got)
	}
	if got := (Question{Text: " Which chapters? "}).Label(); got != "Which chapters?" {
		t.Errorf("Label = %q, want the question text", got)
	}
}
