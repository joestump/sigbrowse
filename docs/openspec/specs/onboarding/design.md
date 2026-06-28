# SPEC-0007 Design: Onboarding (doctor, export, sync)

- **Capability:** onboarding
- **Related ADRs:** [ADR-0015](../../../adr/0015-onboarding-doctor-export-sync.md), [ADR-0010](../../../adr/0010-security-privacy-posture.md), [ADR-0014](../../../adr/0014-image-transcoding-external-converter.md)

## Architecture

All three commands live in `internal/cli` and lean on existing packages — they
add orchestration, not new core logic.

```
doctor  → store.OpenReadOnly + archivepath.Resolve + imageconv.Detect + exec.LookPath   (read-only)
export  → exec.Command(signal-export …) ; exec.Command(imessage-exporter -f txt -c clone …)
sync    → export → ingest.Run + imessage.Run → imageconv.Run → embed.Run → facts.Run
```

## Key design decisions

### Read-only inspection (`doctor`)
`store.OpenReadOnly(path)` opens an existing DB with `mode=ro` and runs no
migrations, so `doctor` reports the true on-disk schema version (drift is visible,
not auto-migrated away) and never creates a file. `checkDataDir` stats the data
dir + DB first and skips DB-dependent checks when absent. (Shipped.)

### Exporter orchestration (`export`)
A small `runner` abstraction wraps command execution so the orchestration is
unit-testable without the real tools:

```
type runner func(ctx, name string, args ...string) error   // exec.CommandContext in prod, a fake in tests
```

Tool resolution: prefer the `--signal-export-bin` / `--imessage-exporter-bin`
flag (or config), else `exec.LookPath(name)`; a required-but-missing tool returns
a clear error (tool name + install hint) unless `--skip-on-error`. iMessage is
always invoked with copy mode so attachments are bundled (the fix for the
reference-only/absolute-path failure mode). Signal/iMessage are independent
sources; with `--skip-on-error`, one failing source logs and the other proceeds.
msgbrowse passes no secrets and reads no Keychain — the invoked tool handles its
own credentials/permissions (ADR-0015).

### Pipeline (`sync`)
`sync` composes the existing run functions in order, each guarded by a per-stage
skip flag and `--skip-on-error`. embed/facts need the LLM; `sync` checks
reachability (or catches their errors) and warns-and-continues rather than failing
the whole pipeline, so a fully-local run with no LLM still produces a browsable
archive. No stage logic is duplicated — `sync` calls `ingest.Run`,
`imessage.Run`, `imageconv.Run`, `embed.Run`, `facts.Run`.

### Security posture
Orchestration is the deliberate widening recorded in ADR-0015: spawning the
user's local exporters (which read the Signal key / need Full Disk Access) at the
user's explicit request, storing no secrets, with no network egress beyond the
single configured LLM endpoint. `doctor` itself is side-effect-free.

## Testing
- `doctor`: classify/verdict/archive-root/host-port units (shipped); read-only
  open + no-create (shipped).
- `export`: inject a fake `runner` to assert the exact tool + flags per source
  (esp. iMessage copy mode), missing-tool error, `--bin` override, and
  `--skip-on-error` continue-past-failure.
- `sync`: inject fakes/stubs to assert stage order, per-stage skip flags, and
  warn-and-continue when the LLM stages fail.

## Sequencing
`doctor` (done) → `export` → `sync` (depends on `export` for its first stage).
