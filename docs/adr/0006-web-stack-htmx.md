# ADR-0006: Web stack — net/http (Go 1.22 routing) + html/template + HTMX, no SPA

- **Status:** Accepted
- **Date:** 2026-06-27
- **Builds on:** [ADR-0001](0001-sqlite-driver-mattn-cgo.md) (single SQLite store the UI reads)

## Context

msgbrowse needs a UI to browse conversations, run live keyword search, scroll a
transcript, and view a media gallery — over a single-user, local-only archive.
The data already lives in SQLite (FTS5 search, source-tagged rows). The question
is what to put in front of it.

The constraints push hard away from a JavaScript SPA:

- **Strict CSP, no CDN.** The security posture ([ADR-0010](0010-security-privacy-posture.md))
  sets `Content-Security-Policy: default-src 'none'; script-src 'self'` — every
  script must be same-origin and self-hosted. A framework loaded from a CDN, or a
  bundler emitting `eval`/inline scripts, is incompatible with that header.
- **No Node at runtime.** The runtime is a single Go binary; we do not want an
  npm toolchain in the container or a `node_modules` in the request path.
- **Untrusted content.** Message bodies are attacker-controlled (a crafted
  archive). Auto-escaping must be the default, not opt-in.
- **Scale is tiny.** One user, one machine. There is no interactivity that
  warrants client-side state management.

## Decision

**Server-render everything with the standard library, sprinkle HTMX for partial
updates, and ship no SPA and no runtime npm.**

1. **Routing: `net/http` with Go 1.22 method+pattern mux.** `internal/web/server.go`
   registers method-qualified patterns (`GET /c/{id}`, `GET /c/{id}/messages`,
   `GET /c/{id}/at/{mid}`, `GET /search/results`, `GET /media/{id}/{path...}`),
   reading path values with `r.PathValue`. No third-party router.
2. **Rendering: `html/template`.** Templates are `go:embed`-ed
   (`//go:embed templates/*.html`) and parsed once at startup. All message
   content flows through the template engine's contextual auto-escaping;
   `renderBody` (`render.go`) additionally HTML-escapes every text run before
   emitting `template.HTML`, drops image/media markdown, and linkifies bare URLs
   with `rel="noopener noreferrer nofollow"`. Search snippets are escaped *before*
   highlight `<mark>` sentinels are substituted (`highlightSnippet`).
3. **Interactivity: HTMX, progressive-enhancement only.** Forms and links work
   without JavaScript; HTMX upgrades them. Live search is a plain
   `<form action="/search" method="get">` that HTMX turns into partial swaps
   (`hx-get="/search/results" hx-target="#search-results"
   hx-trigger="input changed delay:300ms, change"`). Infinite scroll on the
   transcript uses `hx-trigger="revealed"` on a sentinel that swaps in the next
   page (`message_list` partial in `templates/partials.html`).
4. **Partials are just named templates.** The same `message_list` / `search_results`
   blocks render both the full page and the HTMX fragment, so there is one source
   of truth per view.
5. **HTMX is vendored and pinned by SHA-256.** `static/htmx.min.js` is committed
   (`script-src 'self'` means it is the *only* script that can ever run), with
   `static/HTMX-VERSION.md` recording version `2.0.4`, the byte size, the upstream
   URL, and the SHA-256 (`e209dda5…fb447`) plus an update procedure that requires
   re-verifying the hash against a second source.

## Why these choices

- **HTMX over a JS SPA:** the only dynamic surfaces are debounced search and
  reveal-triggered pagination — both are a few `hx-*` attributes against
  endpoints that return HTML the server already knows how to render. An SPA would
  add a build step, a client state layer, a JSON API, and CSP friction, for zero
  capability we lack.
- **`html/template` auto-escaping over manual sanitization:** message bodies are
  untrusted; contextual auto-escaping is the safe default and means a missed
  escape is the exception that stands out (each `template.HTML` site in
  `render.go` is deliberate and escapes its own inputs).
- **stdlib router over chi/gorilla:** Go 1.22 patterns cover method routing,
  wildcards, and `{path...}` rest-matching — the whole route table fits in
  `routes()` with no dependency.
- **Vendoring + SHA pin over a CDN `<script>`:** the strict CSP forbids a CDN
  anyway, and pinning by hash makes a supply-chain swap of htmx a visible,
  reviewable change rather than a silent upstream fetch.

## Consequences

### Positive

- One Go binary serves the whole UI; no runtime Node, no bundler, no JSON API to
  keep in sync with the templates.
- The strict CSP holds because everything is same-origin and self-hosted (one
  vendored script, one stylesheet, inline SVG icons).
- Auto-escaping makes XSS the hard-to-hit path: untrusted bodies are escaped by
  default and re-escaped in `renderBody`.
- Search and pagination degrade gracefully — the form and links still work with
  JavaScript disabled.

### Negative

- Rich client interactions (drag, offline, optimistic UI) would be awkward; if
  the UI ever needs them, this is the wrong substrate.
- HTMX must be manually re-vendored and re-hashed on upgrade — there is no
  package manager doing it for us (the `HTMX-VERSION.md` procedure exists for
  exactly this).
- Partial endpoints (`/search/results`, `/c/{id}/messages`) return HTML
  fragments, so they are coupled to the templates rather than being a reusable
  JSON contract.

### Operational

- Templates and static assets are embedded, so the binary is self-contained — no
  asset paths to mount at runtime.
- The `revealed` infinite-scroll trigger is the most regression-prone surface;
  the htmx update checklist calls it out for manual exercise after a bump.

## Alternatives considered

- **A JS SPA (React/Svelte) over a JSON API.** Rejected: needs npm at build time,
  fights the `script-src 'self'`/no-CDN CSP, and adds a client state layer for a
  single-user app whose only dynamism is search + scroll.
- **A third-party Go router (chi, gorilla/mux).** Rejected: Go 1.22 patterns
  already do method routing and `{path...}` matching; a dependency buys nothing.
- **`text/template` + manual escaping.** Rejected: gives up contextual
  auto-escaping on untrusted message content — the wrong default for this threat
  model.

## References

- `internal/web/server.go` (routes, embed, CSP middleware)
- `internal/web/render.go` (`renderBody`, `highlightSnippet`, escaping)
- `internal/web/templates/` (`partials.html` `message_list`, `search.html`)
- `internal/web/static/HTMX-VERSION.md` (pin + SHA-256)
- [ADR-0010: Security & privacy posture](0010-security-privacy-posture.md)
- [htmx 2.0.4](https://htmx.org)
