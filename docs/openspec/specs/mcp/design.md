# SPEC-0003 Design: MCP Server

- **Capability:** mcp
- **Related ADRs:** [ADR-0004](../../../adr/0004-mcp-sdk-and-rag.md)

## Architecture

```
internal/cli/mcp.go ──▶ mcp.NewServer(store, llmClient, opts)
                          │  registerTools()  (modelcontextprotocol/go-sdk)
                          ├── list_conversations ─┐
                          ├── get_conversation    │
                          ├── search_messages ────┤──▶ internal/store (read-only)
                          ├── semantic_search     │     + llm.Client.Embed (query only)
                          ├── get_context         │
                          ├── list_media          │
                          └── list_links ─────────┘
        transports: StdioTransport (default) | StreamableHTTPHandler (--http)
```

## Key design decisions

### Official Go MCP SDK (ADR-0004)

The server uses `github.com/modelcontextprotocol/go-sdk`, the canonical
spec-tracking implementation, over third-party alternatives. Its typed
`AddTool[In, Out]` infers JSON Schema from Go structs and validates
inputs/outputs, keeping tool definitions declarative and self-documenting, and it
provides both stdio and streamable-HTTP transports out of the box. The cost is a
Go 1.25 toolchain floor, accepted for a single-maintainer project.

### Thin adapter over the store

Every tool calls the same store methods the web UI uses
(`SearchMessages`, `SemanticSearch`, `ConversationTranscript`, `GetContext`,
`ListAttachments`, `ListLinks`, `GetConversationByID`). The MCP layer adds only
shaping: it maps store rows into citation-faithful result structs
(`messageHit`, `mediaInfo`, `linkInfo`) and resolves a conversation name to an id
(erroring on an unknown name). This guarantees keyword/semantic/media behavior
cannot diverge between the model's view and the human's view.

### Citation-faithful result shapes

A shared `messageHit` carries `message_id`, `hash`, `conversation`, `source`,
`sender`, `timestamp`, optional `score`, and the `text` (snippet for keyword hits,
body for semantic hits). Three converters (`hitFromSearch`, `hitFromScored`,
`hitFromView`) produce it from the store's three result types, all normalizing the
owner sender to `Me`. The coordinates let the consuming model cite precisely and a
human jump to the message in the web UI (`/c/{id}/at/{message_id}`). The server
never returns a passage without coordinates.

### Hybrid fusion and graceful degradation

`search_messages` runs the always-available keyword half and a best-effort vector
half (only when an `llm.Client` and embed model are configured and the query embeds
successfully), then fuses with RRF (`fuseResults`, SPEC-0002). A failed vector half
is logged and the tool returns keyword-only results — the keyword half is the
offline floor. `semantic_search` is pure-vector and returns an explicit error when
no embedding model is configured (rather than silently empty results).

### Read-only and minimal egress (ADR-0004)

No tool mutates the store or archive. The only network call any tool makes is
`embedQuery` → `llm.Client.Embed` to embed the search query, via the same local-by-
default endpoint as the rest of msgbrowse. A future sqlite-vec backend (ADR-0002)
changes only `store.SemanticSearch`; the tools are unaffected.

### Transports and stderr logging

`RunStdio` is the default; `RunHTTP` wraps the server in a
`StreamableHTTPHandler` bound to `127.0.0.1:8788` by default with a read-header
timeout and graceful shutdown on context cancel. Under stdio, stdout is the
JSON-RPC channel, so the CLI wires logging to slog's stderr writer; the package
doc and CLI help both call this out.

### Journal tools deferred (ADR-0004)

`get_journal_day` and `on_this_day` are intentionally absent until the journal
generation that produces their data exists (Slice 6), so no tool is backed by data
that does not yet exist.

## Trade-offs

- Conversation filtering is by name (resolved to id), so an unknown name errors
  rather than silently returning nothing — clearer for the calling model.
- The HTTP transport has no authentication; it binds loopback by default and
  exposing it elsewhere is the operator's explicit choice (mirrors the web UI).
