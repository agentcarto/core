package conversation

import (
	"github.com/agentcarto/core/domain"
	"strings"
)

const SubstantialMinSize = 6

// This package is agent-agnostic: what counts as a genuine prompt or command
// is decided by each plugin at parse time (its own tag vocabulary stays there)
// and arrives here as the normalized Event.Prompt/Command fields.

// NodePromptText returns the genuine user prompt the node carries, normalized
// by the plugin at parse time, or "" when it has none.
func NodePromptText(n domain.ConvNode) string {
	for _, e := range n.Events {
		if e.Kind == domain.EventUser && e.Prompt != "" {
			return e.Prompt
		}
	}
	return ""
}

// NodeCommandName returns the normalized label of the user-issued command the
// node carries ("/verify", "! ls -la"), or "" when it has none.
func NodeCommandName(n domain.ConvNode) string {
	for _, e := range n.Events {
		if e.Kind == domain.EventUser && e.Command != "" {
			return e.Command
		}
	}
	return ""
}
func NodeHasUser(n domain.ConvNode) bool {
	for _, e := range n.Events {
		if e.Kind == domain.EventUser {
			return true
		}
	}
	return false
}
func NodeCompact(n domain.ConvNode) bool {
	for _, e := range n.Events {
		if e.RawType == domain.RawCompactSummary {
			return true
		}
	}
	return false
}
func NodeTurnID(n domain.ConvNode) string {
	for _, e := range n.Events {
		if e.TurnID != "" {
			return e.TurnID
		}
	}
	return ""
}
func TurnsOfPath(c domain.Conversation, path []string) [][]string {
	var turns [][]string
	seenBoundary := false
	lastBoundaryTurnID := ""
	for _, id := range path {
		n := c.Nodes[id]
		boundary := NodePromptText(n) != "" || NodeCommandName(n) != "" || NodeCompact(n)
		turnID := NodeTurnID(n)
		sameBoundaryTurn := turnID != "" && turnID == lastBoundaryTurnID
		if boundary && seenBoundary && len(turns) > 0 && !sameBoundaryTurn {
			turns = append(turns, nil)
		}
		if boundary {
			seenBoundary = true
			if turnID != "" {
				lastBoundaryTurnID = turnID
			}
		}
		if len(turns) == 0 {
			turns = append(turns, nil)
		}
		turns[len(turns)-1] = append(turns[len(turns)-1], id)
	}
	return turns
}

// TurnIsCompact reports whether the turn contains a /compact boundary
// (a compact_summary event).
func TurnIsCompact(c domain.Conversation, ids []string) bool {
	for _, id := range ids {
		if NodeCompact(c.Nodes[id]) {
			return true
		}
	}
	return false
}

// NodeHasRealContent reports whether the node holds real content beyond a
// summary: a genuine user prompt, a command other than /clear, an assistant
// message, a tool call/result, or reasoning. Because NodeCommandName excludes
// /clear, a node containing only /clear is also false here.
func NodeHasRealContent(n domain.ConvNode) bool {
	if NodePromptText(n) != "" || NodeCommandName(n) != "" {
		return true
	}
	for _, e := range n.Events {
		if e.RawType == domain.RawCompactSummary {
			continue
		}
		switch e.Kind {
		case domain.EventAssistant, domain.EventToolCall, domain.EventToolResult, domain.EventReasoning:
			if e.Text != "" || e.ToolName != "" {
				return true
			}
		}
	}
	return false
}

// TurnHasRealContent reports whether the turn holds real content beyond a
// summary (a genuine prompt, an assistant message, a tool call/result, or
// reasoning).
func TurnHasRealContent(c domain.Conversation, ids []string) bool {
	for _, id := range ids {
		if NodeHasRealContent(c.Nodes[id]) {
			return true
		}
	}
	return false
}

// NodesHaveRealContent reports whether any node in the slice holds real content.
// It is used to exclude empty sessions from the listing: those with only a clear
// command, or with no utterances at all.
func NodesHaveRealContent(nodes []domain.ConvNode) bool {
	for _, n := range nodes {
		if NodeHasRealContent(n) {
			return true
		}
	}
	return false
}

func TurnHeadline(c domain.Conversation, ids []string) string {
	for _, id := range ids {
		n := c.Nodes[id]
		if x := NodePromptText(n); x != "" {
			return x
		}
		if x := NodeCommandName(n); x != "" {
			return x
		}
	}
	for _, id := range ids {
		for _, e := range c.Nodes[id].Events {
			if e.Kind == domain.EventAssistant {
				if s := strings.TrimSpace(e.Text); s != "" {
					return strings.Join(strings.Fields(s), " ")
				}
			}
		}
	}
	for _, id := range ids {
		for _, e := range c.Nodes[id].Events {
			if e.Kind == domain.EventToolCall {
				if e.ToolName != "" {
					return "◆ " + e.ToolName
				}
				return "◆ tool"
			}
			if e.Kind == domain.EventReasoning {
				return "◇ (thinking)"
			}
			if e.Kind == domain.EventToolResult {
				return "└ (tool result)"
			}
		}
	}
	return "(no content)"
}

func DeepestPath(c domain.Conversation, root string) []string {
	var path []string
	seen := map[string]bool{}
	for cur := root; cur != "" && !seen[cur]; {
		seen[cur] = true
		path = append(path, cur)
		kids := c.Children[cur]
		if len(kids) == 0 {
			break
		}
		best := kids[0]
		for _, k := range kids[1:] {
			a, b := c.Nodes[k], c.Nodes[best]
			if b.Timestamp.IsZero() || (!a.Timestamp.IsZero() && a.Timestamp.After(b.Timestamp)) {
				best = k
			}
		}
		cur = best
	}
	return path
}

func BranchLead(c domain.Conversation, root string) string {
	path := DeepestPath(c, root)
	for _, id := range path {
		n := c.Nodes[id]
		if x := NodePromptText(n); x != "" {
			return "▶ " + x
		}
		if x := NodeCommandName(n); x != "" {
			return "▶ " + x
		}
	}
	return TurnHeadline(c, path)
}

// BranchKind returns the kind of an alternate line of conversation: "fork" for a
// fork that lives in a separate file, or "rewind" for a rewind within the same
// session (including an independent root that was restarted by rewinding to the
// beginning).
//
// Earlier, when a fork was opened on its own, the parent's original continuation
// was classified as "main" (origin). That category has since been dropped:
// conversation rendering was canonicalized to start from the root ancestor, so
// regardless of how a session is opened it forms the same tree (parent -> ... ->
// current). The parent's main line therefore always ends up on the active-path
// (foreground) side and needs no label, making "main" unnecessary.
func BranchKind(c domain.Conversation, root string) string {
	for _, id := range c.ForkRoots {
		if id == root {
			return "fork"
		}
	}
	return "rewind"
}

func Subtree(c domain.Conversation, root string) []string {
	out := []string{}
	// Guard against cycles in the children graph: a corrupt transcript can link
	// nodes so the DFS revisits them, growing both stack and out without bound.
	seen := map[string]bool{}
	stack := []string{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		stack = append(stack, c.Children[id]...)
	}
	return out
}
func IsSubstantial(c domain.Conversation, root string) bool {
	xs := Subtree(c, root)
	if len(xs) >= SubstantialMinSize {
		return true
	}
	for _, id := range xs {
		if NodeHasUser(c.Nodes[id]) {
			return true
		}
	}
	return false
}

// BranchAltCount returns how many further alternate lines (sub-branches) appear
// when descending into this branch. It walks the branch's deepest path and
// counts the substantial branches at each turn.
func BranchAltCount(c domain.Conversation, root string) int {
	dp := DeepestPath(c, root)
	active := map[string]bool{}
	for _, id := range dp {
		active[id] = true
	}
	n := 0
	for _, ids := range TurnsOfPath(c, dp) {
		_, subs := TurnBranches(c, ids, active)
		n += len(subs)
	}
	return n
}

func TurnBranches(c domain.Conversation, turn []string, active map[string]bool) (int, []string) {
	trivial := 0
	var substantial []string
	for _, id := range turn {
		for _, child := range c.Children[id] {
			if active[child] {
				continue
			}
			if IsSubstantial(c, child) {
				substantial = append(substantial, child)
			} else {
				trivial++
			}
		}
	}
	return trivial, substantial
}
