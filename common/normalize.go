package common

import (
	"encoding/json"
	"strings"

	"github.com/agentcarto/core/domain"
)

// This file holds building blocks for the parse-time normalization contract
// (Event.Prompt/ToolArg/Changes, and the Text of a question or an answer).
// Whether and how to apply them stays each plugin's decision; nothing here
// encodes a specific agent's vocabulary.

// IsBareSlashCommand reports whether s is a short single-line slash command
// ("/compact") rather than real prompt text. Plugins use it to keep such
// input out of Event.Prompt.
func IsBareSlashCommand(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "/") && !strings.Contains(s, "\n") && len([]rune(s)) <= 40
}

// toolArgKeys are common JSON field names of a tool call's salient argument,
// in preference order. Both spellings of each name are listed: agents disagree
// on case (Claude sends file_path, Copilot sends filePath), and a payload whose
// key is missing here renders as a bare tool name — no path, no command, and
// nothing for a search to match.
var toolArgKeys = []string{
	"description",
	"file_path", "filePath",
	"notebook_path", "notebookPath",
	"path", "relativePath",
	"command",
	"pattern",
	"query",
	"url",
	"prompt",
}

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

// Question is one question an agent asked the user through an ask tool, in the
// shape every agent's ask payload reduces to.
type Question struct {
	// Header is the short label the agent gave the question. It may be empty.
	Header string
	Text   string
	// Multi reports that the user may pick more than one option.
	Multi   bool
	Options []Option
}

// Option is one choice offered with a Question. Preview is the longer body some
// agents attach to a choice (a mock-up, a code sample); it may span lines.
type Option struct{ Label, Description, Preview string }

// Label returns what names the question in an answer: its header, or the
// question text when it has none.
func (q Question) Label() string {
	if h := strings.TrimSpace(q.Header); h != "" {
		return h
	}
	return strings.TrimSpace(q.Text)
}

// Answer is the user's response to one Question.
type Answer struct {
	// Header names the question answered (Question.Label).
	Header string
	// Values are the options picked, or the text the user typed instead. None,
	// or only blank ones, means the question was left unanswered.
	Values []string
	// Note is what the user added alongside the choice.
	Note string
}

// QuestionText renders questions as the Text of an EventQuestion: per question
// a "header: text" line, then one "- label: description" line per option with
// the option's preview indented beneath it. Questions are separated by a blank
// line. A question or an option with nothing to name it is left out.
func QuestionText(qs []Question) string {
	blocks := make([]string, 0, len(qs))
	for _, q := range qs {
		var lines []string
		if head := joinLabel(q.Header, q.Text); head != "" {
			if q.Multi {
				head += " (multi-select)"
			}
			lines = append(lines, head)
		}
		for _, o := range q.Options {
			item := joinLabel(o.Label, o.Description)
			if item == "" {
				continue
			}
			lines = append(lines, "- "+item)
			if p := strings.Trim(o.Preview, "\n"); strings.TrimSpace(p) != "" {
				lines = append(lines, indent(p, "    "))
			}
		}
		if len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// AnswerText renders answers as the Text of an EventAnswer: one
// "header: value, value" line per question, "(no answer)" standing in for an
// unanswered one, with the user's note on an indented line beneath.
func AnswerText(as []Answer) string {
	lines := make([]string, 0, len(as))
	for _, a := range as {
		var picked []string
		for _, v := range a.Values {
			if v = strings.TrimSpace(v); v != "" {
				picked = append(picked, v)
			}
		}
		value := "(no answer)"
		if len(picked) > 0 {
			value = strings.Join(picked, ", ")
		}
		lines = append(lines, joinLabel(a.Header, value))
		if n := strings.TrimSpace(a.Note); n != "" {
			lines = append(lines, indent("note: "+n, "  "))
		}
	}
	return strings.Join(lines, "\n")
}

// joinLabel returns "label: body", or whichever of the two is not blank.
func joinLabel(label, body string) string {
	label, body = strings.TrimSpace(label), strings.TrimSpace(body)
	if label == "" || body == "" {
		return label + body
	}
	return label + ": " + body
}

// indent prefixes every line of s.
func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
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
