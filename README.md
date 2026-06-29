# core

Plugin SDK for [AgentCarto](https://github.com/agentcarto/agentcarto). Each agent
plugin depends only on this module.

- `domain` — shared data model (Session, Conversation, Command, …)
- `plugin` — plugin interfaces and the go-plugin RPC bridge (Scanner, Factory, Descriptor, `Serve`, `Launch`)
- `scan` — differential-scan helpers (fingerprint, reuse/skip)
- `conversation` — builds the conversation tree (rewind/fork branches)
- `transaction` — atomic file mutations
- `common` — shared plugin helpers
