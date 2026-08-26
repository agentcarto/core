package conversation

import (
	"github.com/agentcarto/core/domain"
	"strings"
	"testing"
	"time"
)

func ev(k domain.EventKind, s string) domain.Event { return domain.Event{Kind: k, Text: s} }

// prompt is a user event the plugin classified as a genuine prompt.
func prompt(s string) domain.Event {
	return domain.Event{Kind: domain.EventUser, Text: s, Prompt: s}
}

// pseudo is a user event the plugin classified as system-injected (no Prompt).
func pseudo(s string) domain.Event { return domain.Event{Kind: domain.EventUser, Text: s} }

// cmd is a user event the plugin classified as a command invocation.
func cmd(text, label string) domain.Event {
	return domain.Event{Kind: domain.EventUser, Text: text, Command: label}
}

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
	c := domain.NewConversation([]domain.ConvNode{{ID: "a", Events: []domain.Event{pseudo("<command-name>/clear</command-name>")}}, {ID: "b", Parent: "a", Events: []domain.Event{prompt("Q1")}}, {ID: "c", Parent: "b", Events: []domain.Event{ev(domain.EventAssistant, "A1")}}, {ID: "d", Parent: "c", Events: []domain.Event{prompt("Q2")}}})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || TurnHeadline(c, turns[0]) != "Q1" {
		t.Fatalf("%v", turns)
	}
}
func TestCommandTurnBoundary(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{prompt("Q1")}},
		{ID: "b", Parent: "a", Events: []domain.Event{ev(domain.EventAssistant, "A1")}},
		{ID: "c", Parent: "b", Events: []domain.Event{cmd("<command-name>/verify</command-name>", "/verify")}},
		{ID: "d", Parent: "c", Events: []domain.Event{ev(domain.EventAssistant, "A2")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || strings.Join(turns[1], ",") != "c,d" {
		t.Fatalf("turns=%v", turns)
	}
	if h := TurnHeadline(c, turns[1]); h != "/verify" {
		t.Fatalf("headline=%q", h)
	}
}
func TestBashCommandTurnBoundary(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{prompt("Q1")}},
		{ID: "b", Parent: "a", Events: []domain.Event{ev(domain.EventAssistant, "A1")}},
		{ID: "c", Parent: "b", Events: []domain.Event{cmd("<bash-input>ls -la</bash-input>", "! ls -la")}},
		{ID: "d", Parent: "c", Events: []domain.Event{pseudo("<bash-stdout>total 0</bash-stdout>")}},
		{ID: "e", Parent: "d", Events: []domain.Event{prompt("Q2")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 3 || strings.Join(turns[1], ",") != "c,d" {
		t.Fatalf("turns=%v", turns)
	}
	if h := TurnHeadline(c, turns[1]); h != "! ls -la" {
		t.Fatalf("headline=%q", h)
	}
}
func TestPseudoUserEventNotTurnBoundary(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q1")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{ev(domain.EventAssistant, "A1")}},
		{ID: "c", Parent: "b", Timestamp: time.Unix(3, 0), Events: []domain.Event{prompt("Q2")}},
		{ID: "d", Parent: "c", Timestamp: time.Unix(4, 0), Events: []domain.Event{ev(domain.EventAssistant, "A2")}},
		{ID: "e", Parent: "d", Timestamp: time.Unix(5, 0), Events: []domain.Event{pseudo("<task-notification>\n<task-id>x</task-id>")}},
		{ID: "f", Parent: "e", Timestamp: time.Unix(6, 0), Events: []domain.Event{ev(domain.EventAssistant, "A3")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || strings.Join(turns[0], ",") != "a,b" || strings.Join(turns[1], ",") != "c,d,e,f" || TurnHeadline(c, turns[1]) != "Q2" {
		t.Fatalf("turns=%v headline=%q", turns, TurnHeadline(c, turns[1]))
	}
}
func TestSameCodexTurnIDDoesNotSplitTurn(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{{Kind: domain.EventUser, Text: "Q1", Prompt: "Q1", TurnID: "turn-1"}}},
		{ID: "b", Parent: "a", Events: []domain.Event{{Kind: domain.EventAssistant, Text: "working", TurnID: "turn-1"}}},
		{ID: "c", Parent: "b", Events: []domain.Event{{Kind: domain.EventUser, Text: "follow-up while running", Prompt: "follow-up while running", TurnID: "turn-1"}}},
		{ID: "d", Parent: "c", Events: []domain.Event{{Kind: domain.EventAssistant, Text: "still working", TurnID: "turn-1"}}},
		{ID: "e", Parent: "d", Events: []domain.Event{{Kind: domain.EventUser, Text: "Q2", Prompt: "Q2", TurnID: "turn-2"}}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || strings.Join(turns[0], ",") != "a,b,c,d" || strings.Join(turns[1], ",") != "e" {
		t.Fatalf("turns=%v", turns)
	}
}
func TestBranchClassifyPrototype(t *testing.T) {
	nodes := []domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q")}},
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
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "alt", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{prompt("rephrased question")}},
	})
	if !IsSubstantial(c, "alt") {
		t.Fatal("user prompt branch should be substantial")
	}
}

// A command is something the user said too. /clear is the exception
// NodeCommandName carves out — it only wipes the screen — so a branch holding
// nothing else is not one to offer.
func TestSubstantialByUserCommand(t *testing.T) {
	branch := func(e domain.Event) domain.Conversation {
		return domain.NewConversation([]domain.ConvNode{
			{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q")}},
			{ID: "b", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
			{ID: "alt", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{e}},
		})
	}
	if !IsSubstantial(branch(cmd("<command-name>/verify</command-name>", "/verify")), "alt") {
		t.Fatal("a branch holding a command should be substantial")
	}
	if IsSubstantial(branch(pseudo("<command-name>/clear</command-name>")), "alt") {
		t.Fatal("a /clear-only branch should not be substantial")
	}
}

// A manual /compact leaves the raw "/compact" input behind as a childless leaf
// while the compacted conversation resumes from the same parent. The leaf holds
// a user event the plugin classified as system-injected, so there is nothing in
// the branch to read and it must not be offered as one.
func TestBranchWithNothingReadableIsNotSubstantial(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "raw", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{pseudo("/compact")}},
	})
	if IsSubstantial(c, "raw") {
		t.Fatal("a branch holding only a system-injected user event should not be substantial")
	}
	active := map[string]bool{"a": true, "b": true}
	trivial, subs := TurnBranches(c, TurnsOfPath(c, c.ActivePath())[0], active)
	if trivial != 1 || len(subs) != 0 {
		t.Fatalf("the branch should be counted as trivial, not offered: trivial=%d subs=%v", trivial, subs)
	}
}

// A summary-only branch is likewise nothing anyone said: a compact summary is a
// boundary of its own kind and never carries a prompt.
func TestSummaryOnlyBranchIsNotSubstantial(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "sum", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{
			{Kind: domain.EventUser, Text: "(summary)", RawType: domain.RawCompactSummary},
		}},
	})
	if IsSubstantial(c, "sum") {
		t.Fatal("a branch holding only a compact summary should not be substantial")
	}
}

// Size still stands on its own: a branch large enough to be a conversation is
// substantial even when no single node of it reads as content.
func TestBigBranchIsSubstantialWithoutReadableContent(t *testing.T) {
	nodes := []domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q")}},
		{ID: "b", Parent: "a", Timestamp: time.Unix(99, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
	}
	parent := "a"
	for i := range SubstantialMinSize {
		id := "alt" + string(rune('0'+i))
		nodes = append(nodes, domain.ConvNode{
			ID: id, Parent: parent, Timestamp: time.Unix(int64(2+i), 0),
			Events: []domain.Event{pseudo("<system-reminder>")},
		})
		parent = id
	}
	c := domain.NewConversation(nodes)
	if !IsSubstantial(c, "alt0") {
		t.Fatalf("a branch of %d nodes should be substantial on size alone", SubstantialMinSize)
	}
}

func TestBranchLeadFallsBackToAssistantOrTool(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0), Events: []domain.Event{prompt("Q")}},
		{ID: "active", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "alt", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{ev(domain.EventAssistant, "old work")}},
	})
	if got := BranchLead(c, "alt"); got != "old work" {
		t.Fatalf("lead=%q", got)
	}
	c = domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{prompt("Q")}},
		{ID: "active", Parent: "a", Timestamp: time.Unix(9, 0), Events: []domain.Event{ev(domain.EventAssistant, "active")}},
		{ID: "alt", Parent: "a", Timestamp: time.Unix(2, 0), Events: []domain.Event{{Kind: domain.EventToolCall, ToolName: "Edit"}}},
	})
	if got := BranchLead(c, "alt"); got != "◆ Edit" {
		t.Fatalf("lead=%q", got)
	}
}
func TestCompactMentionDoesNotSplitTurn(t *testing.T) {
	c := domain.NewConversation([]domain.ConvNode{
		{ID: "a", Events: []domain.Event{prompt("Q1")}},
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
		{ID: "a", Events: []domain.Event{prompt("Q1")}},
		{ID: "b", Parent: "a", Events: []domain.Event{{Kind: domain.EventUser, Text: "summary", RawType: domain.RawCompactSummary}}},
		{ID: "c", Parent: "b", Events: []domain.Event{ev(domain.EventAssistant, "post compact")}},
	})
	turns := TurnsOfPath(c, c.ActivePath())
	if len(turns) != 2 || TurnHeadline(c, turns[1]) != "post compact" {
		t.Fatalf("%v headline=%q", turns, TurnHeadline(c, turns[1]))
	}
}
