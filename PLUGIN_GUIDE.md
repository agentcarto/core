# AgentCarto Plugin Development Guide

An AgentCarto plugin teaches the host about one AI coding agent: where its
sessions live on disk, how to parse them into the shared domain model, and
(optionally) how to resume, fork, or relocate them. Each plugin lives in its
own repository, builds one binary (`agentcarto-plugin-<type>`), and runs as a
subprocess that the host talks to over
[hashicorp/go-plugin](https://github.com/hashicorp/go-plugin) (net/rpc + gob).
A plugin crash or missing binary degrades gracefully and never takes down the
host.

The four existing plugins are the best reference: `plugin-claude` (the most
complete: branch trees, forks, all actions), `plugin-codex`, `plugin-grok`, and
`plugin-copilot` (the smallest: scan + conversation only, two factories in one
module).

## Skeleton

A plugin module depends on `github.com/agentcarto/core` (plus small direct
dependencies of its own, such as `gopkg.in/yaml.v3` for options decoding) and
never on the host or on other plugins. It exposes a `Factory`:

```go
package myagent

import (
    "github.com/agentcarto/core/common"
    "github.com/agentcarto/core/domain"
    "github.com/agentcarto/core/plugin"
    "gopkg.in/yaml.v3"
)

type Factory struct{}

func (Factory) Descriptor() plugin.Descriptor {
    return plugin.Descriptor{
        Type:          "myagent",
        DisplayName:   "My Agent",
        ParserVersion: "1",
        Capabilities:  domain.Capabilities{Scan: true, Conversation: true},
    }
}

func (Factory) New(id string, options *yaml.Node) (any, error) {
    o := Options{} // your own struct for config.yaml's options: block
    if err := common.DecodeOptions(options, &o); err != nil {
        return nil, err // DecodeOptions rejects unknown option keys
    }
    return &Plugin{id: id, o: o}, nil
}
```

The binary's `main` is one line:

```go
// cmd/agentcarto-plugin-myagent/main.go
package main

import (
    "github.com/agentcarto/core/plugin"
    myagent "github.com/agentcarto/plugin-myagent"
)

func main() { plugin.Serve(myagent.Factory{}) }
```

During development, clone your repo next to the others and add it to the
workspace `go.work`; the module itself carries
`replace github.com/agentcarto/core => ../agentcarto-core`.

## The contract: capability interfaces

The value returned by `Factory.New` implements a subset of small interfaces
(`core/plugin/plugin.go`) and advertises them via `Descriptor.Capabilities`.
The registry rejects a plugin that advertises a capability without implementing
the matching interface.

| Capability | Interface | What it does |
|---|---|---|
| `Scan` | `Scanner` | list sessions from disk (incremental) |
| `Conversation` | `ConversationLoader` | parse one session into a `domain.Conversation` |
| `Active` | `ActiveMatcher` | mark sessions whose agent process is currently running |
| `Resume` | `Resumer` | build the CLI command that resumes a session |
| `Rewind` | `Rewinder` | plan a fork at a conversation node |
| `Relocate` | `Relocator` | plan moving sessions to another directory |
| — | `ExecutableProvider` | the agent CLI's executable name (used by `Active`/handoff) |

Start with `Scan` + `Conversation`; everything else is optional and the host
shows a read-only "not supported" reason where a capability is missing.

## Scanning (incremental by design)

`Scan(ctx, plugin.ScanInput)` receives the **entire previous snapshot** by
value: `Warm` (previous sessions) and `Dead` (a negative cache of paths that
parsed to nothing). There are no per-path host callbacks — the plugin decides
reuse/skip/re-parse itself with `core/scan.Cache`:

```go
cache := scan.New(in.Warm, in.Dead, Factory{}.Descriptor().ParserVersion)
for _, path := range candidatePaths {
    if s, ok := cache.Reuse(path); ok { out = append(out, s); continue } // unchanged, previous result
    if cache.Skip(path) { continue }                                     // unchanged, known-dead
    s, ok := buildSession(ctx, path)                                     // parse
    if !ok { cache.Dead(path); continue }                                // remember as dead
    cache.Stamp(&s)                                                      // fingerprint + ParserVersion
    out = append(out, s)
}
return plugin.ScanOutput{Sessions: out, Dead: cache.DeadOut()}, nil
```

The fingerprint is a hash of `path:size:mtime`, reproducible on both sides of
the process boundary. `Reuse` refuses warm entries stamped with a different
`ParserVersion`, which is how a parser change invalidates old results.

Session fields worth knowing:

- `Title` — use `common.Title(events, fallback)`; it picks the first *user*
  event with a non-empty `Prompt` (see the normalization contract below).
- `CWD` — set `"(unknown)"` when unresolvable. If your agent *never* records a
  working directory, also set `InferCWD: true`; the host then borrows the CWD
  of a temporally-near session of any agent (a cross-plugin heuristic that can
  only run host-side).
- `ParentSessionID` / `ForkAt` — fork lineage. `ForkAt` is the parent node ID
  the fork attaches to; plugins that know it let the host graft cheaply and
  decide `EmptyFork` during scan. Plugins that don't record a fork point leave
  `ForkAt` empty and the host detects empty forks by prefix comparison.
- `EmptyFork`, `Unresumable` — gate listing and the resume action; see the
  comments in `core/domain/model.go`.
- Exclude sessions with no real content (e.g. only a clear command):
  `conversation.NodesHaveRealContent(nodes)`.

## Conversations

`LoadConversation` returns a `*domain.Conversation`: a tree of `ConvNode`s
(`Nodes`, `Children`, `Roots`, `ActiveLeaf`, `ForkRoots`), each node holding
`[]domain.Event`. Agents without rewind/fork branches can build a linear chain
with `common.Linear(events)`; `plugin-claude` shows a real tree, and
`plugin-grok` branches at rewind markers.

`Event.Kind` is the normalized vocabulary (`user`, `queued`, `assistant`,
`reasoning`, `tool_call`, `tool_result`, `file_change`, `stream`,
`turn_complete`, `system`, `meta`, `task`). `RawType` is a free-form string for
your own bookkeeping, with one cross-plugin value: `domain.RawCompactSummary`
marks the summary a context compaction leaves behind. Set `TurnID` when your
agent's log carries turn identifiers — events sharing a `TurnID` are never
split into separate turns.

## The normalization contract (the important part)

**Core and the host never inspect `Event.Text` with agent-specific format
knowledge.** Everything the host derives — turn boundaries, headlines, titles,
tool-call labels, file diffs — comes from normalized fields you fill at parse
time. Your plugin is the only place that knows your agent's wrapper tags,
preambles, and payload formats.

| Field | You set it when | The host uses it for |
|---|---|---|
| `Event.Prompt` | a user event is a *genuine* prompt: cleaned, whitespace-folded text. Leave empty for system-injected pseudo-prompts (reminders, preambles, notifications, wrappers) and compact summaries. | turn boundaries, turn headlines, session titles, USER-vs-system rendering |
| `Event.Command` | a user event is a command invocation: a normalized label (`"/verify"`, `"! ls -la"`). Commands that must not open a turn (e.g. a clear) are your policy: leave it empty. | turn boundaries, headlines |
| `Event.ToolArg` | a tool call (or task event) has a salient one-line argument: a shell command as `"$ make check"`, a file path, a search pattern. | the label next to `ToolName` |
| `Event.ToolDetail` | the expanded body should differ from raw `Text` (e.g. a cleaned shell command, output with agent metadata stripped). | the expanded block body (falls back to `Text`) |
| `Event.Changes` | the event edits files: one `domain.FileChange{Path, Op, Added, Removed, Diff}` per file. `Diff` holds apply_patch-style hunk lines (`@@`, `+`, `-`, context) **without** the `*** ... File:` header (the host derives it from `Path`/`Op`). Empty `Diff` = counts only. | the consolidated per-turn file-diff section and edit stats; `Changes`-bearing events are hidden from the timeline |
| `EventTask` (kind) | your agent logs background-task/subagent completion notices. `ToolArg` = label (`"<id> [status]"`), `ToolDetail` = body. | the TASK block and the per-turn task count |

Rules of thumb:

- A turn opens where a node has a non-empty `Prompt` or `Command`, or a
  `RawCompactSummary` event. If your classification is wrong, whole
  conversations collapse into one turn — test it.
- When your agent logs both the *request* for an edit (a `tool_call`) and its
  *applied result* (a `file_change`) in the same turn, set `Changes` on both;
  the host generically prefers the applied one, so nothing is counted twice.
- Reconstruct diffs however your data allows: `plugin-claude` diffs
  old/new strings into hunks (`unifiedHunks`), `plugin-codex` splits the
  apply_patch documents its agent already writes.
- Keep `Text` as the raw payload; normalized fields are *additions*, and raw
  text remains the fallback for display and search.
- **Any change to parsing or classification requires a `ParserVersion` bump.**
  It invalidates both the plugin-side scan reuse and the host's derived caches
  (search index artifacts are keyed by it). Shipping a parse change without a
  bump is the classic way to get stale, inconsistent UI.

## Actions (resume / fork / relocate)

`ResumeCommand` returns a `domain.Command` (executable + args + cwd); the host
`exec`-replaces itself with it. `PlanFork`/`PlanRelocate` return a
`domain.MutationPlan` — declarative writes/moves plus `AllowedRoots`. The host
validates the plan (`core/transaction`) and refuses any path outside your
declared roots, then applies it atomically. Forks must always create a new
session and never modify the original.

## Config and binary resolution

Users enable plugins in `config.yaml`:

```yaml
plugins:
  - id: myagent
    type: myagent          # matches Descriptor.Type
    enabled: true
    color: magenta         # list color; the host has no per-agent color knowledge
    # command: /path/to/agentcarto-plugin-myagent   # optional explicit path
    options: {}            # decoded into Factory.New's *yaml.Node
```

The host resolves the binary via `plugins[].command`, then
`agentcarto-plugin-<type>` next to the host binary, then `PATH`.

## Testing and release

- Build/test everything from the host directory: `cd agentcarto && make check`
  (builds the host plus all sibling modules; the host's integration test
  launches real plugin binaries from `bin/` and skips when one is missing, so
  `make test` — not bare `go test` — is the meaningful host run).
- Unit-test your classifier and normalization directly (see the
  `classify_test.go` / `tool_test.go` files in the existing plugins); they are
  the contract the host relies on.
- Releases are per-repository: each plugin repo publishes
  `agentcarto-plugin-<repo>_<os>_<arch>.tar.gz` (`.zip` on Windows) on its own
  GitHub Release, and the installer fetches the latest. A repo hosting several
  factories bundles all its binaries in one archive (`plugin-copilot` ships
  both `-vc` and `-jb`). Two version knobs matter:
  - `Descriptor.ParserVersion` — bump on any parse/classification change
    (cache invalidation).
  - `plugin.Handshake.ProtocolVersion` (in core) — bumped when the meaning of
    the transferred domain types changes; a mismatched host/plugin pair fails
    the handshake with a clear error instead of degrading silently.
