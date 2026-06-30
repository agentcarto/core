package conversation

import (
	"github.com/agentcarto/core/domain"
	"strings"
	"testing"
	"time"
)

func ev(k domain.EventKind, s string) domain.Event { return domain.Event{Kind: k, Text: s} }

// A children graph with a cycle (X<->Y) must not make Subtree's DFS loop
// forever; before the fix it grew the stack and result without bound.
func TestSubtreeCycleTerminates(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "X", Parent: "Y"},
		{ID: "Y", Parent: "X"},
	})
	done := make(chan []string, 1)
	go func() { done <- Subtree(c, "X") }()
	select {
	case got := <-done:
		if len(got) > len(c.Nodes) {
			t.Fatalf("Subtree visited a node twice: %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subtree did not terminate on a cyclic children graph")
	}
}
func TestTurns(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{{ID: "a", Events: []domain.Event{ev(domain.EventUser, "/clear")}}, {ID: "b", Parent: "a", Events: []domain.Event{ev(domain.EventUser, "Q1")}}, {ID: "c", Parent: "b", Events: []domain.Event{ev(domain.EventAssistant, "A1")}}, {ID: "d", Parent: "c", Events: []domain.Event{ev(domain.EventUser, "Q2")}}})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || TurnHeadline(c, turns[0]) != "Q1" {
		t.Fatalf("%v", turns)
	}
}
func TestCommandBoundary(t *testing.T) {
	n := domain.ConvNode{Events: []domain.Event{ev(domain.EventUser, "<command-name>/verify</command-name>")}}
	if NodeCommandName(n) != "/verify" {
		t.Fatal(NodeCommandName(n))
	}
}
func TestTaskNotificationNotTurnBoundary(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{ev(domain.EventUser, "Q1")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{ev(domain.EventAssistant, "A1")}},
		{ID: "c", Parent: "b", Timestamp: time.Unix(3, 0), Events: []domain.Event{ev(domain.EventUser, "Q2")}},
		{ID: "d", Parent: "c", Timestamp: time.Unix(4, 0), Events: []domain.Event{ev(domain.EventAssistant, "A2")}},
		{ID: "e", Parent: "d", Timestamp: time.Unix(5, 0), Events: []domain.Event{ev(domain.EventUser, "<task-notification>\n<task-id>x</task-id>")}},
		{ID: "f", Parent: "e", Timestamp: time.Unix(6, 0), Events: []domain.Event{ev(domain.EventAssistant, "A3")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || strings.Join(turns[0], ",") != "a,b" || strings.Join(turns[1], ",") != "c,d,e,f" || TurnHeadline(c, turns[1]) != "Q2" {
		t.Fatalf("turns=%v headline=%q", turns, TurnHeadline(c, turns[1]))
	}
}
func TestSameCodexTurnIDDoesNotSplitTurn(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{{Kind: domain.EventUser, Text: "Q1", TurnID: "turn-1"}}},
		{ID: "b", Parent: "a", Events: []domain.Event{{Kind: domain.EventAssistant, Text: "working", TurnID: "turn-1"}}},
		{ID: "c", Parent: "b", Events: []domain.Event{{Kind: domain.EventUser, Text: "follow-up while running", TurnID: "turn-1"}}},
		{ID: "d", Parent: "c", Events: []domain.Event{{Kind: domain.EventAssistant, Text: "still working", TurnID: "turn-1"}}},
		{ID: "e", Parent: "d", Events: []domain.Event{{Kind: domain.EventUser, Text: "Q2", TurnID: "turn-2"}}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || strings.Join(turns[0], ",") != "a,b,c,d" || strings.Join(turns[1], ",") != "e" {
		t.Fatalf("turns=%v", turns)
	}
}
func TestClearCommandExcludedFromBoundary(t *testing.T) {
	clear := domain.ConvNode{Events: []domain.Event{ev(domain.EventUser, "<command-message>clear</command-message>\n<command-name>/clear</command-name>")}}
	if NodeCommandName(clear) != "" {
		t.Fatalf("clear should be excluded, got %q", NodeCommandName(clear))
	}
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{ev(domain.EventUser, "Q1")}},
		{ID: "b", Parent: "a", Events: []domain.Event{ev(domain.EventAssistant, "A1")}},
		{ID: "c", Parent: "b", Events: clear.Events},
		{ID: "d", Parent: "c", Events: []domain.Event{ev(domain.EventAssistant, "A2")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 1 || strings.Join(turns[0], ",") != "a,b,c,d" {
		t.Fatalf("turns=%v", turns)
	}
}
func TestBranchClassifyPrototype(t *testing.T) {
	nodes := []domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{ev(domain.EventUser, "Q")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active answer")}},
		{ID: "r1", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Bash"}}},
		{ID: "r2", Parent: "a", Timestamp: time.Unix(3, 0), Events: []domain.Event{ev(domain.EventAssistant, "old work")}},
		{ID: "r2_0", Parent: "r2", Timestamp: time.Unix(4, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Edit"}}},
		{ID: "r2_1", Parent: "r2_0", Timestamp: time.Unix(5, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Edit"}}},
		{ID: "r2_2", Parent: "r2_1", Timestamp: time.Unix(6, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Edit"}}},
		{ID: "r2_3", Parent: "r2_2", Timestamp: time.Unix(7, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Edit"}}},
		{ID: "r2_4", Parent: "r2_3", Timestamp: time.Unix(8, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Edit"}}},
	}
	c := domain.NewConversation(nodes)
	if c.ActiveLeaf != "b" || strings.Join(c.ActivePath(), ",") != "a,b" {
		t.Fatalf("active leaf/path=%q %v", c.ActiveLeaf, c.ActivePath())
	}
	active := map[string]bool{"a": true, "b": true}
	turns := TurnsOfPath(c, c.ActivePath())
	trivial, subs := TurnBranches(c, turns[0], active)
	if trivial != 1 || len(subs) != 1 || subs[0] != "r2" {
		t.Fatalf("trivial=%d subs=%v", trivial, subs)
	}
}
func TestSubstantialByUserPrompt(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{ev(domain.EventUser, "Q")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "alt", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{ev(domain.EventUser, "rephrased question")}},
	})
	if !IsSubstantial(c, "alt") {
		t.Fatal("user prompt branch should be substantial")
	}
}

func TestBranchLeadFallsBackToAssistantOrTool(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{ev(domain.EventUser, "Q")}},
		{ID: "active", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "alt", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{ev(domain.EventAssistant, "old work")}},
	})
	if got := BranchLead(c, "alt"); got != "old work" {
		t.Fatalf("lead=%q", got)
	}
	c = domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{ev(domain.EventUser, "Q")}},
		{ID: "active", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "alt", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Edit"}}},
	})
	if got := BranchLead(c, "alt"); got != "◆ Edit" {
		t.Fatalf("lead=%q", got)
	}
}
func TestPseudoPromptMatchesPrototypePrefixes(t *testing.T) {
	for _, text := range []string{"/compact please", "<environment_context>ctx", "<user_instructions>x", "<agent_skills>x", "<bash-stdout>x", "<local-command-caveat>x"} {
		if !IsPseudoPrompt(text) {
			t.Fatalf("%q should be pseudo", text)
		}
	}
	if IsPseudoPrompt("Please mention /compact in docs") {
		t.Fatal("inline /compact mention should not be pseudo")
	}
}
func TestCompactMentionDoesNotSplitTurn(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{ev(domain.EventUser, "Q1")}},
		{ID: "b", Parent: "a", Events: []domain.Event{ev(domain.EventAssistant, "Please mention /compact in docs")}},
		{ID: "c", Parent: "b", Events: []domain.Event{ev(domain.EventAssistant, "A2")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 1 || TurnHeadline(c, turns[0]) != "Q1" {
		t.Fatalf("%v", turns)
	}
}
func TestCompactSummaryIsBoundaryButNotHeadline(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{ev(domain.EventUser, "Q1")}},
		{ID: "b", Parent: "a", Events: []domain.Event{{Kind: domain.EventUser, Text: "summary", RawType: "compact_summary"}}},
		{ID: "c", Parent: "b", Events: []domain.Event{ev(domain.EventAssistant, "post compact")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || TurnHeadline(c, turns[1]) != "post compact" {
		t.Fatalf("%v headline=%q", turns, TurnHeadline(c, turns[1]))
	}
}
