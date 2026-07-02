package domain

import (
	"sort"
	"time"
)

type EventKind string

const (
	EventUser         EventKind = "user"
	EventQueued       EventKind = "queued"
	EventAssistant    EventKind = "assistant"
	EventReasoning    EventKind = "reasoning"
	EventToolCall     EventKind = "tool_call"
	EventToolResult   EventKind = "tool_result"
	EventFileChange   EventKind = "file_change"
	EventStream       EventKind = "stream"
	EventTurnComplete EventKind = "turn_complete"
	EventSystem       EventKind = "system"
	EventMeta         EventKind = "meta"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusReady   Status = "ready"
	StatusOther   Status = "other"
)

type SessionKey struct{ PluginID, SessionID string }
type SessionRef struct{ Source string }
type Session struct {
	PluginID        string     `json:"plugin_id"`
	AgentType       string     `json:"agent_type"`
	SessionID       string     `json:"session_id"`
	CWD             string     `json:"cwd"`
	StartedAt       time.Time  `json:"started_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Title           string     `json:"title"`
	SourceRef       SessionRef `json:"source_ref"`
	Status          Status     `json:"status,omitempty"`
	LastKind        EventKind  `json:"last_kind,omitempty"`
	PermissionWait  bool       `json:"permission_wait,omitempty"`
	ParentSessionID string     `json:"parent_session_id,omitempty"`
	ForkAt          string     `json:"fork_at,omitempty"`
	// EmptyFork marks a full-copy fork that was created in agentcarto but never
	// continued: its connection point ForkAt is at the tip with no child below
	// it, so it is identical to the parent's prefix and carries no unique
	// information. Such sessions are excluded from the listing.
	EmptyFork bool `json:"empty_fork,omitempty"`
	// Unresumable marks a session that cannot be resumed on its own through the
	// agent's CLI. Claude's native subagent forks (under subagents/) are like
	// this: their SessionID is a synthetic, filename-based ID used only to make
	// the entry unique, while the real sessionId is the same as the parent's.
	// Running `--resume <SessionID>` would fail because no such session exists
	// (the fork branch has no resumable head). The resume action is not offered.
	Unresumable   bool   `json:"unresumable,omitempty"`
	Model         string `json:"model,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	ParserVersion string `json:"parser_version,omitempty"`
}

func (s Session) Key() SessionKey { return SessionKey{s.PluginID, s.SessionID} }

// RawCompactSummary is the normalized RawType every plugin assigns to the
// summary event a context compaction leaves behind. Core treats such an event
// as a turn boundary that must not become a headline or title.
const RawCompactSummary = "compact_summary"

type Event struct {
	Kind      EventKind `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	RawType   string    `json:"raw_type,omitempty"`
	TurnID    string    `json:"turn_id,omitempty"`
	// Prompt and Command are normalized by the plugin at parse time; they are
	// how agent-specific vocabulary (wrapper tags, preambles, command syntax)
	// stays inside the plugin. Core derives turn boundaries, headlines and
	// titles from these fields alone, never from Text.
	//
	// Prompt is the cleaned, whitespace-folded text of a genuine user prompt.
	// It is empty for system-injected pseudo-prompts (reminders, preambles,
	// notifications), command invocations, and compact summaries.
	Prompt string `json:"prompt,omitempty"`
	// Command is the normalized label of a user-issued command ("/verify",
	// "! ls -la"). Commands that must not open a turn (e.g. Claude's /clear)
	// are the plugin's own policy: it leaves Command empty for them.
	Command string `json:"command,omitempty"`
}
type ConvNode struct {
	ID        string    `json:"id"`
	Parent    string    `json:"parent,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Events    []Event   `json:"events"`
}
type Conversation struct {
	Nodes      map[string]ConvNode `json:"nodes"`
	Children   map[string][]string `json:"children"`
	Roots      []string            `json:"roots"`
	ActiveLeaf string              `json:"active_leaf,omitempty"`
	ForkRoots  []string            `json:"fork_roots,omitempty"`
}

func NewConversation(nodes []ConvNode) Conversation {
	c := Conversation{Nodes: map[string]ConvNode{}, Children: map[string][]string{}}
	order := map[string]int{}
	for _, n := range nodes {
		order[n.ID] = len(order)
		c.Nodes[n.ID] = n
		if n.Parent == "" {
			c.Roots = append(c.Roots, n.ID)
		} else {
			c.Children[n.Parent] = append(c.Children[n.Parent], n.ID)
		}
	}
	for parent := range c.Children {
		sort.SliceStable(c.Children[parent], func(i, j int) bool {
			a, b := c.Nodes[c.Children[parent][i]], c.Nodes[c.Children[parent][j]]
			if !a.Timestamp.Equal(b.Timestamp) {
				if a.Timestamp.IsZero() {
					return false
				}
				if b.Timestamp.IsZero() {
					return true
				}
				return a.Timestamp.Before(b.Timestamp)
			}
			return order[a.ID] < order[b.ID]
		})
	}
	// The active leaf is the tip of the current branch: among the leaf nodes
	// (those with no children), the one with the latest timestamp. Ties are
	// broken by input order, with the later-seen node winning.
	for id, n := range c.Nodes {
		if len(c.Children[id]) > 0 {
			continue
		}
		if c.ActiveLeaf == "" || leafBeats(n, order[id], c.Nodes[c.ActiveLeaf], order[c.ActiveLeaf]) {
			c.ActiveLeaf = id
		}
	}
	return c
}

// leafBeats reports whether candidate leaf cand (at input index ci) should
// replace the current best active-leaf cur (at input index ri). A later
// timestamp wins; equal timestamps are broken by the later input order.
func leafBeats(cand ConvNode, ci int, cur ConvNode, ri int) bool {
	if !cand.Timestamp.IsZero() && (cur.Timestamp.IsZero() || cand.Timestamp.After(cur.Timestamp)) {
		return true
	}
	return cand.Timestamp.Equal(cur.Timestamp) && ci > ri
}

func (c Conversation) ActivePath() []string {
	if c.ActiveLeaf == "" {
		return nil
	}
	var out []string
	// Guard against cyclic parent links (a corrupt transcript can make a node's
	// parent chain loop back on itself). Without seen, the walk would append to
	// out forever and grow memory without bound until the process is OOM-killed.
	seen := map[string]bool{}
	for id := c.ActiveLeaf; id != "" && !seen[id]; {
		seen[id] = true
		out = append(out, id)
		n, ok := c.Nodes[id]
		if !ok {
			break
		}
		id = n.Parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

type Capabilities struct{ Scan, Conversation, Active, Resume, Rewind, Relocate bool }
type Command struct {
	Executable       string
	Args             []string
	WorkingDirectory string
}
type FileWrite struct {
	Path string
	Data []byte
	Mode uint32
}
type PathMove struct{ From, To string }
type MutationPlan struct {
	PluginID, Description string
	AllowedRoots          []string
	Writes                []FileWrite
	Moves                 []PathMove
}
type MutationResult struct{ Completed, Pending, RolledBack, Warnings []string }
type ForkTarget struct {
	NodeID    string
	KeepTurns int
}
type Process struct {
	PID             int32
	PPID            int32
	Executable      string
	Args, OpenFiles []string
	CWD             string
}
type ActionAvailability struct {
	Enabled bool
	Reason  string
}
type Snapshot struct {
	Sessions []Session
	Scanning bool
	Errors   []PluginError
	Version  uint64
	// Dead maps path -> fingerprint for files/directories that were parsed but
	// produced no session. It is carried across scans as a negative cache so
	// that, if such a path is unchanged on the next scan, re-parsing is skipped.
	Dead map[string]string
}
type PluginError struct {
	PluginID, Reason string
	Err              error
}
