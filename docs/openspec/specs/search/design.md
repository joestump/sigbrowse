# SPEC-0002 Design: Search

- **Capability:** search
- **Related ADRs:** [ADR-0002](../../../adr/0002-vector-backend.md), [ADR-0004](../../../adr/0004-mcp-sdk-and-rag.md)

## Architecture

Search primitives live in the store; the web UI and MCP layer are thin consumers,
so keyword/semantic/hybrid behavior cannot drift between them (ADR-0004).

```
                       ┌── internal/store/search.go  (FTS5 + buildFTSQuery)
internal/store ────────┤
                       └── internal/store/vector.go  (embeddings, SemanticSearch, cosine)
internal/embed ──▶ llm.Client.Embed ──▶ store.PutEmbedding   (incremental)
internal/web/search.go ──▶ store.SearchMessages + GetContext (keyword + jump-to-context)
internal/mcp/tools.go  ──▶ store.SearchMessages + store.SemanticSearch ──▶ RRF fuse
```

## Key design decisions

### FTS5 keyword search and injection safety

`messages_fts` is an FTS5 external-content table over `messages.body`
(`tokenize='unicode61 remove_diacritics 2'`), kept in sync by AFTER
INSERT/DELETE/UPDATE triggers (SPEC-0001 schema). `buildFTSQuery` is the single
chokepoint that turns user input into a MATCH expression: every token becomes a
quoted prefix term `"token"*` with embedded `"` doubled, ANDed together. Quoting
neutralizes every FTS5 operator and punctuation character, so the query can never
be a syntax error and user input can never restructure the query — the safety
property is structural, not a denylist. Filters are appended as parameterized
`AND` clauses, never string-interpolated.

### Snippet highlighting: control-character sentinels, escape-then-mark

The store stays presentation-free: `snippet()` wraps matches in `\x02`/`\x03`
control bytes (which never occur in real bodies). The web layer's
`highlightSnippet` performs a strict ordering — strip stray C0 controls except the
sentinels, HTML-escape, then swap sentinels for `<mark>` tags. Doing the strip
before escape defends against a crafted body that smuggles a literal sentinel byte
(which would otherwise survive escaping and emit an unbalanced tag). This is not an
XSS vector (`<mark>` has no attribute/script context) but keeps markup well-formed.
Message bodies are untrusted everywhere and always escaped (shared with SPEC-0004's
`renderBody`).

### Vector backend: Go brute-force cosine over float32 blobs (ADR-0002)

Per ADR-0002, embeddings are stored as little-endian float32 BLOBs in a normal
table keyed by `(message_hash, model)` (SPEC-0001 v3 schema), and `SemanticSearch`
loads the filtered candidate set and scores cosine similarity in Go. At
personal-archive scale brute force is fast enough and keeps everything in one
SQLite file with no extension dependency; a `sqlite-vec` backend can later replace
the method body behind the same signature. Keying by stable hash (not rowid) means
re-ingest does not orphan unchanged embeddings, and keying by model lets two models
coexist without forcing a full re-embed on a model switch. Dimension mismatches
(same model name, changed output dim) are skipped rather than scored.

### Incremental embedding as a separate egress step

`internal/embed` deliberately runs as its own command so a plain import never makes
LLM calls. It selects messages with no embedding for the model (non-system,
non-empty body), batches them through `llm.Client.Embed`, and upserts vectors. The
model string is trimmed once and reused for both the selection query and storage
(the PK includes the model, so a stray space would make stored vectors never
satisfy the "needs embedding" query); a bounded retry backstop aborts with a clear
diagnostic if a batch records no progress.

### Hybrid retrieval lives in MCP, fused by RRF (ADR-0004)

The web UI's `/search` is keyword-only (it degrades cleanly without JS and never
requires the LLM). The MCP `search_messages` tool is the hybrid surface: it runs
both halves and fuses with reciprocal-rank fusion (`sum of 1/(60+rank)`). RRF is
chosen because bm25 and cosine scores live on incomparable scales; rank fusion is
scale-free and robust. The vector half is best-effort — if embedding the query or
the vector search fails, the tool logs and returns keyword-only results, so the
offline-always keyword half is the floor. Ties in the fused order break by message
id so identical queries are reproducible (the fusion map's iteration order is
otherwise random).

### Jump-to-context ownership check

`GetContext` derives the conversation from the message itself, so rendering
`/c/{id}/at/{mid}` without verifying ownership would let a crafted or mistyped link
show conversation `id`'s chrome while displaying a different conversation's
transcript — an identity-confusion / information-disclosure bug. The web handler
calls `MessageConversationID(mid)` and 404s unless it equals `id`. The MCP
`get_context` tool similarly derives the owning conversation from the message id
for correct provenance.

## Trade-offs

- Brute-force cosine is O(candidates) per query; acceptable at this scale and
  revisited (sqlite-vec) only if latency is felt (ADR-0002).
- Quoting every token makes all keyword search prefix-AND with no phrase or
  boolean operators exposed to users — a deliberate simplicity/safety trade.
- Hybrid fusion is MCP-only; the web search stays keyword-only to remain
  zero-dependency and offline.
