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
