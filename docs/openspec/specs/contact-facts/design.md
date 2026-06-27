# SPEC-0005 Design: Contact facts

- **Capability:** contact-facts
- **Related ADRs:** [ADR-0011](../../../adr/0011-contact-facts-extraction.md), [ADR-0003](../../../adr/0003-dual-source-archive.md), [ADR-0010](../../../adr/0010-security-privacy-posture.md)

## Architecture

Fact extraction mirrors the embed pipeline: a dedicated command drives an
orchestrator that reads messages from the store, calls the LLM once per batch,
and writes results back. Extraction logic lives in `internal/facts`; persistence
and the incrementality cursor live in `internal/store`.

```
internal/cli/facts.go ──▶ facts.Run(store, llm.Client, Options)
                             │
internal/facts/facts.go ─────┼─ FactConversations (honors exclude list)
                             ├─ per conversation (bounded worker pool):
                             │    GetFactState → ResolveCursor (hash → keyset)
                             │    loop: GetMessages(after cursor)
                             │          buildPrompt → llm.Chat → parseFacts
                             │          PutFact (dedup) ; SetFactState (advance)
                             └─ Summary
internal/web/conversation ─▶ store.ContactFactsByConversation (resolve provenance id)
```

## Key design decisions

### Incrementality cursor keyed on a hash

`fact_state(conversation_id, last_message_hash, model, facts_added, updated_at)`
stores the last message handed to the extractor as a **hash**, not a rowid.
`ReplaceConversationMessages` reassigns rowids on every re-ingest but hashes are
stable, so at run time `ResolveCursor` maps the stored hash back to a `(ts_unix,
id)` keyset position and `GetMessages` continues after it. A vanished hash (the
message was removed by a re-ingest) resolves to not-found and the conversation
restarts from the top — harmless because `PutFact` is idempotent. Recording the
`model` lets a model change re-scan from the start.

### Idempotency and merged contacts

`contact_facts` is keyed to `contacts(id)` with `UNIQUE(contact_id, fact_hash)`
where `fact_hash = sha256(lower(trim(fact)))`. `PutFact` uses `ON CONFLICT DO
NOTHING` and reports whether a row was inserted (for counting). Because facts
attach to the contact, two conversations merged onto one contact
([ADR-0003](../../../adr/0003-dual-source-archive.md)) contribute to a single deduplicated set, and
`ContactFactsByConversation` resolves the contact via a subquery
(`WHERE f.contact_id = (SELECT contact_id FROM conversations WHERE id = ?)`) to
avoid fan-out. There is intentionally no foreign key from `source_message_hash`
to `messages`, for the same reason embeddings omit it.

### Structured extraction and defensive parsing

`buildPrompt` renders a numbered transcript of the batch's real messages (owner
labeled "You", others labeled with the contact name). The system prompt demands a
JSON array of `{fact, category, evidence}`. `parseFacts`:

- extracts the outermost `[ … ]` (tolerating fences/prose) via `extractJSONArray`;
- drops blank facts;
- lowercases the category and coerces anything off the allowlist to `other`;
- maps the 1-based `evidence` onto the included slice, clamping out-of-range or
  missing indices to the last message so provenance is never lost.

This keeps a sloppy model response from failing a whole batch.

### Concurrency, failure, and resumption

`Run` fans conversations out to a worker pool (`--concurrency`). The cursor is
persisted after every batch via `SetFactState`, so a per-batch `llm.Chat` failure
cancels the run (first error wins) but the next run resumes from the last
persisted cursor — the same "abort and resume" contract as `embed`. A defensive
per-conversation batch cap backstops any future no-progress seam.

### Privacy boundary

`FactConversations` filters `journal.exclude_conversations` by name while
listing candidates, before any message body is read, so an excluded thread's
content never reaches the orchestrator or the LLM ([ADR-0010](../../../adr/0010-security-privacy-posture.md)).
The sole egress is `llm.Chat` to `llm.base_url`.

## Testing

- `internal/store/facts_test.go`: dedup + provenance resolution, unresolved
  provenance after re-ingest, exclude/contactless/empty filtering, cursor
  round-trip + hash resolution, merged-contact visibility, reset.
- `internal/facts/parse_test.go`: fenced/bounded parse, category coercion,
  evidence clamping, empty/garbage handling, prompt rendering, array extraction.
- `internal/facts/run_test.go`: end-to-end run with a fake `llm.Client` —
  extraction, exclude honored, incremental no-op re-run, `--conversation` scope,
  `--reset` rebuild.
- `internal/web/facts_web_test.go`: conversation page renders the panel + jump
  link, and omits the panel when empty.
