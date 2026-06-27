# ADR-0011: AI contact facts — incremental, cited extraction

- **Status:** Accepted
- **Date:** 2026-06-27
- **Relates to:** [ADR-0002](0002-vector-backend.md) (brute-force, no extra services), [ADR-0003](0003-dual-source-archive.md) (contacts model), [ADR-0004](0004-mcp-sdk-and-rag.md) (LLM client + citation-faithfulness), [ADR-0010](0010-security-privacy-posture.md) (single egress, exclude list)

## Context

The conversation view should answer "who is this person?" at a glance — not just
the transcript. We want short, durable facts about a contact ("Has a dog named
Biscuit", "Works as a nurse in Denver") gathered by the LLM as it reads their
messages, shown when you open the conversation.

Constraints from the rest of the system:

- **One egress, opt-in cost.** LLM calls go only to `llm.base_url` and must be a
  deliberate step, never a side effect of import or serving ([ADR-0010](0010-security-privacy-posture.md)).
- **Re-ingest churn.** `ReplaceConversationMessages` deletes and re-inserts a
  conversation's rows on every import (new rowids, stable hashes), exactly as it
  does for embeddings ([ADR-0002](0002-vector-backend.md)).
- **Merged identities.** A person's Signal and iMessage threads may be linked to
  one contact ([ADR-0003](0003-dual-source-archive.md)); facts must accumulate per *contact*, not per
  conversation.
- **Privacy.** `journal.exclude_conversations` flags threads that must never be
  sent to any LLM.

## Decision

A dedicated `msgbrowse facts` command extracts **atomic, categorized, cited**
facts, modeled directly on the `embed` command's incremental design.

1. **Structured output with provenance.** For each batch of a contact's
   messages, the model returns a JSON array of `{fact, category, evidence}`
   where `evidence` is the 1-based index of the supporting message. Each stored
   fact carries `(source, source_message_hash, source_ts)` so the UI links it
   back to the exact message (jump-to-context). Category is constrained to an
   allowlist (`personal, work, relationships, preferences, health, location,
   plans, other`); anything else is coerced to `other`. An out-of-range or
   missing `evidence` clamps to the last message in the batch so a fact never
   loses its provenance. The parser tolerates code fences / prose around the
   array.

2. **Facts are keyed to contacts, deduplicated by normalized text.**
   `contact_facts(contact_id, …, fact_hash, …)` with `UNIQUE(contact_id,
   fact_hash)` where `fact_hash = sha256(lower(trim(fact)))`. `PutFact` does
   `INSERT … ON CONFLICT DO NOTHING`, so reprocessing — or extracting from two
   conversations merged onto one contact — never duplicates a fact. No foreign
   key from `source_message_hash` to `messages` (a CASCADE would wipe facts on
   every re-ingest, same reasoning as embeddings); a fact whose message later
   vanishes renders without a jump link.

3. **Incremental via a hash cursor.** `fact_state(conversation_id,
   last_message_hash, model, …)` records the last message handed to the
   extractor and the model that produced its facts. At run time the hash is
   resolved back to a `(ts_unix, id)` keyset position (surviving re-ingest); a
   missing hash restarts from the top (safe, because PutFact is idempotent). A
   different stored model re-scans the contact from the start so a model change
   re-derives everything.

4. **Bounded concurrency, resumable on failure.** Conversations are processed by
   a small worker pool (`--concurrency`, default 4). The cursor is persisted
   after every batch, so a mid-run LLM failure aborts the run but the next run
   resumes where it stopped — exactly like `embed`.

5. **Honors the exclude list and the single egress.** `FactConversations`
   filters `journal.exclude_conversations` by name *before* any content is read,
   so excluded threads never reach the orchestrator, let alone the LLM. The only
   network call is `llm.Chat` to `llm.base_url`.

## Consequences

- Facts are cheap to re-run (incremental) and safe to re-run (idempotent), with
  no new services or schema beyond two tables (migration v4).
- Provenance is first-class: every fact is one click from its evidence, keeping
  the feature honest and auditable — wrong facts are traceable to the message
  that misled the model.
- The model allowlist + clamping mean a sloppy LLM response degrades gracefully
  (coerced category, clamped citation) instead of failing the batch.
- Quality depends on the chat model; facts are explicitly labeled AI-generated
  and "may be incomplete or wrong" in the UI. A prompt or model change is
  applied by re-running (model change auto-rescans; `--reset` wipes and rebuilds).
- Deferred: surfacing facts via MCP, editing/curating facts, and confidence
  scoring. The schema leaves room (facts are rows, not a blob) to add these.
