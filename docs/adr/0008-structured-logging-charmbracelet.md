# ADR-0008: Structured logging via charmbracelet/log as an slog.Handler

- **Status:** Accepted
- **Date:** 2026-06-27

## Context

msgbrowse runs as several long- and short-lived processes — `serve`, `mcp`,
`signal-import`, `imessage-import`, `embed`, `watch`, `journal` — that all need
leveled, readable logging. Two constraints shape the choice:

- **`mcp` speaks JSON-RPC over stdio.** Anything written to **stdout** corrupts
  that stream, so logs must go to **stderr** exclusively.
- **The code already logs through `log/slog`.** Handlers across the codebase use
  the standard `*slog.Logger`; we want pretty, colorized, leveled output without
  touching every call site.

## Decision

**Install `charmbracelet/log` as the `slog.Handler` behind the default
`*slog.Logger`, writing to stderr, and key actionable error hints on sentinel
errors.**

1. **One handler swap, everywhere.** `configureLogger` (`internal/cli/root.go`)
   does `slog.SetDefault(slog.New(newLogHandler(level)))`. Because
   `charmbracelet/log` implements `slog.Handler`, every existing `slog` call
   across import/serve/mcp gets colorized, leveled, timestamped output with **no
   call-site changes**.
2. **stderr only.** `newLogHandler` constructs the handler with
   `charmlog.NewWithOptions(os.Stderr, …)` — keeping stdout clean for the `mcp`
   subcommand's JSON-RPC stream.
3. **Configured twice, by design.** `Execute` installs a pretty `info` logger up
   front so even config-load failures (which happen before per-command config
   resolution) render nicely; `resolveConfig` re-installs it at the resolved
   `log_level` once config is known.
4. **Level mapping.** `newLogHandler` maps the `log_level` string
   (`debug`/`info`/`warn`/`error`) to `charmlog` levels; the level is validated in
   `config.Validate`.
5. **Actionable error hints on sentinels.** `renderError` / `errorHint`
   (`root.go`) match failures with `errors.Is` against sentinel errors
   (`ingest.ErrExportDirNotFound`, `imessage.ErrArchiveNotFound`) and log a
   `hint` attribute telling the user the concrete next step (e.g. point
   `archive_root` at the folder that *contains* `export/`). `main` only sets the
   exit code so the error isn't printed twice.

## Why these choices

- **slog.Handler adapter over a logging facade:** the standard library's `slog`
  is already the call-site API; making `charmbracelet/log` the *handler* gives
  styled output for free and keeps the door open to swap the handler (e.g. JSON)
  without re-touching code.
- **stderr-only is non-negotiable for MCP:** the stdio JSON-RPC transport owns
  stdout; a single stray stdout log line would break a client. stderr is the only
  safe sink for a process that may be an MCP server.
- **Sentinel-keyed hints over string matching:** `errors.Is` stays correct as
  error messages are reworded, and ties the hint to the actual failure mode users
  hit most (mis-pointed archive roots).

## Consequences

### Positive

- Consistent, colorized, leveled logs across every subcommand from one place.
- `mcp` over stdio stays correct — logs never pollute the JSON-RPC stream.
- The two most common setup mistakes self-explain via `hint` attributes.

### Negative

- The logger is configured twice (early default, then at resolved level); the
  early-`info` window means very-early `debug` lines won't appear until config
  resolves. This is intentional but worth knowing.
- Output is human-oriented (pretty/colorized) rather than machine-parseable JSON;
  a future deployment wanting JSON logs would add a handler option.

### Operational

- Logs go to stderr; container/process supervisors should capture stderr.
- New user-facing failure modes should get a sentinel error + an `errorHint`
  case rather than an inline message, to keep hints discoverable.

## Alternatives considered

- **Plain `slog.NewTextHandler`/`JSONHandler`.** Rejected: functional but drab;
  `charmbracelet/log` gives leveled color for local, single-user use at the cost
  of one dependency, and still satisfies the `slog.Handler` contract.
- **A logging facade (zap/zerolog) at call sites.** Rejected: the code already
  speaks `slog`; introducing a second logging API everywhere is churn for no
  gain.
- **Logging to stdout (or splitting by level).** Rejected: unsafe for the `mcp`
  stdio transport; stderr-only is simpler and correct.

## References

- `internal/cli/root.go` (`configureLogger`, `newLogHandler`, `renderError`, `errorHint`)
- `cmd/msgbrowse/main.go` (exit-only, no double print)
- [ADR-0004: MCP SDK & RAG](0004-mcp-sdk-and-rag.md) (the stdio JSON-RPC stream this protects)
