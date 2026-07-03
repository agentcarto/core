package common

import (
	"encoding/json"
	"strings"

	"github.com/agentcarto/core/domain"
)

// This file holds building blocks for the parse-time normalization contract
// (Event.Prompt/ToolArg/Changes). Whether and how to apply them stays each
// plugin's decision; nothing here encodes a specific agent's vocabulary.

// IsBareSlashCommand reports whether s is a short single-line slash command
// ("/compact") rather than real prompt text. Plugins use it to keep such
// input out of Event.Prompt.
func IsBareSlashCommand(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "/") && !strings.Contains(s, "\n") && len([]rune(s)) <= 40
}

// toolArgKeys are common JSON field names of a tool call's salient argument,
// in preference order.
var toolArgKeys = []string{"description", "file_path", "notebook_path", "path", "command", "pattern", "query", "url", "prompt"}

// ToolArgFromJSON extracts the one-line display argument for a tool call from
// a JSON invocation payload, or "" when text is not a JSON object or has no
// salient string field.
func ToolArgFromJSON(text string) string {
	var m map[string]any
	if json.Unmarshal([]byte(text), &m) != nil {
		return ""
	}
	for _, k := range toolArgKeys {
		if v, _ := m[k].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// SplitPatch converts an apply_patch document (the normalized diff format)
// into per-file changes: the "*** ... File:" headers become Path/Op (dropped
// from Diff, the host re-renders them) and the +/- lines are counted per file.
func SplitPatch(patch string) []domain.FileChange {
	var out []domain.FileChange
	var bodies [][]string
	ci := -1
	for _, line := range strings.Split(patch, "\n") {
		if line == "*** Begin Patch" || line == "*** End Patch" {
			continue
		}
		op, path := "", ""
		for prefix, o := range map[string]string{"*** Add File: ": "add", "*** Update File: ": "update", "*** Delete File: ": "delete"} {
			if strings.HasPrefix(line, prefix) {
				op, path = o, strings.TrimSpace(strings.TrimPrefix(line, prefix))
				break
			}
		}
		if path != "" {
			out = append(out, domain.FileChange{Path: path, Op: op})
			bodies = append(bodies, nil)
			ci = len(out) - 1
			continue
		}
		if ci < 0 {
			continue
		}
		bodies[ci] = append(bodies[ci], line)
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			out[ci].Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			out[ci].Removed++
		}
	}
	for i := range out {
		out[i].Diff = strings.Join(bodies[i], "\n")
	}
	return out
}
