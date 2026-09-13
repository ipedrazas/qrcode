# Build prompt: static QR code service (Go 1.26)

Build a stateless HTTP service in Go 1.26 that takes a URL and returns a QR code as SVG.

## Scope

Static QR codes only. The destination URL is encoded directly into the QR matrix. There is no shortener, no redirect endpoint, no database, no analytics, no persistence of any kind.

The service must never dereference, fetch, resolve, or follow the input URL. It is a string to be encoded, nothing more. The service should function correctly with all outbound network egress blocked.

## API

### `GET /qr`

| Param | Required | Default | Notes |
|---|---|---|---|
| `url` | yes | — | The string to encode |
| `ec` | no | `M` | Error correction: `L`, `M`, `Q`, `H` |
| `margin` | no | `4` | Quiet zone in modules. Min 4 (spec minimum), max 16 |
| `fg` | no | `#000000` | Strict `^#[0-9a-fA-F]{6}$` |
| `bg` | no | `#ffffff` | Same pattern, or the literal `none` for transparent |

There is deliberately **no** `size` or `scale` parameter. SVG is vector; sizing is the consumer's concern via `width`/`height` attributes or CSS. The `viewBox` is expressed in module units.

Success: `200`, `Content-Type: image/svg+xml; charset=utf-8`.

Errors: `400` with a JSON body carrying a stable machine-readable error code plus a human-readable message. Never return an SVG for an error.

Reject unknown query parameters with a `400` rather than silently ignoring them — a typo'd param should fail loudly, not produce a code with unexpected defaults.

### `GET /healthz`

Liveness. Returns `200` with a trivial body.

## SVG output

- `viewBox="0 0 N N"` where `N = matrixSize + (2 * margin)`.
- No `width`/`height` attributes on the root element, so it scales cleanly wherever it is embedded.
- All dark modules rendered as a **single `<path>`** built from horizontal runs (`M x y h w v 1 h -w z`). Do not emit one `<rect>` per module — a version 40 code is 177×177 and that approach produces tens of thousands of elements.
- `shape-rendering="crispEdges"` on the path.
- Background as a single `<rect>` covering the full viewBox, omitted entirely when `bg=none`.
- `xmlns` declared. No XML declaration, no DOCTYPE.
- **Deterministic**: identical inputs must produce byte-identical output. No map iteration order leaking into the markup, no timestamps, no generated IDs.
- The input URL must appear **only** inside the encoded matrix. Never interpolate it into the markup — no `<title>`, no `<desc>`, no comments, no `data-` attributes. This is the difference between a safe generator and an XSS vector when the SVG is served from our own origin.

## Encoding

Use a maintained pure-Go QR encoder that exposes the raw module matrix. Check that whatever you pick is actively maintained before committing to it — several of the most-starred Go QR libraries are archived or unmaintained. State your choice and the reasoning in the README.

Render the SVG ourselves rather than using a library's built-in SVG writer, so we control quiet zone handling, path construction, and determinism. Put the encoder behind a small internal interface so it can be swapped without touching the HTTP layer.

## Validation

- `url` must parse with `net/url` and have scheme `http` or `https`. Reject everything else, including `javascript:`, `data:`, and scheme-relative input.
- Reject if the URL exceeds 2048 bytes.
- Reject if the payload would exceed QR version 40 capacity at the requested EC level. The error message should say so explicitly and suggest a lower EC level or a shorter URL.
- Canonicalize parameters (case-normalize `ec` and hex colors, apply defaults) before anything downstream uses them.

## Security

- No outbound network calls, ever. If there is a clean way to assert this in a test, do it.
- Response headers on every response: `X-Content-Type-Options: nosniff` and `Content-Security-Policy: default-src 'none'; sandbox`.
- Per-IP rate limiting, configurable, returning `429` with `Retry-After`.
- Set `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, and `MaxHeaderBytes` on `http.Server`. Do not rely on defaults.
- Do not log raw URLs — they are user data and may carry tokens in query strings. Log a short SHA-256 prefix and the byte length instead.
- Container runs as non-root with a read-only root filesystem and no shell.

## Caching

The response is a pure function of the canonicalized parameters, so make it content-addressable:

- Strong `ETag` derived from a hash of the canonical parameter set.
- Handle `If-None-Match` and return `304` when it matches.
- `Cache-Control: public, max-age=31536000, immutable`.

Canonicalizing before hashing means `?url=X&ec=m` and `?ec=M&url=X` share a cache entry.

## Configuration

Environment variables only, no config file: `PORT`, `RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`, `MAX_URL_LEN`, `LOG_LEVEL`. Sensible defaults for all of them so the service runs with no configuration at all.

## Structure

```
cmd/qrsvc/main.go
internal/qr/       encoding + SVG rendering
internal/httpapi/  handlers, middleware, validation
```

Standard library `net/http` with `ServeMux` routing patterns. No web framework. `log/slog` with JSON output. Graceful shutdown on `SIGINT`/`SIGTERM` with a bounded drain period.

## Testing

- **Round-trip correctness.** Render the module matrix to an `image.Gray` in the test, decode it with a QR decoder, and assert the decoded payload equals the input exactly. Run this across all four EC levels and a spread of URL lengths including boundary cases near version transitions. This is the test that actually matters — golden files alone cannot catch an encoder producing plausible-looking but wrong output.
- **Golden file tests** for SVG rendering, with a Task target to regenerate them.
- **Determinism test**: same input rendered many times must produce identical bytes.
- **Quiet zone test**: assert the rendered output really has `margin` clear modules on all four sides.
- **Fuzz test** on the `url` parameter. It must never panic, regardless of input.
- **Table tests** for validation rules, parameter canonicalization, and ETag stability.

## Tooling

- `Taskfile.yml` with `test`, `lint`, `build`, `docker`, `run` targets.
- `golangci-lint` with a checked-in config.
- GitHub Actions: test + lint on PR; release workflow building a multi-arch (amd64, arm64) image pushed to `ghcr`.
- Multi-stage `Dockerfile`, `CGO_ENABLED=0`, static binary on `distroless/static` or `scratch`.

## Documentation

README covering the API, example requests, and an explicit statement of the threat model: why this service is static-only, why there is no redirect endpoint, and why it never fetches the URL. Someone will eventually ask for dynamic codes, and the README should be the answer.

## Out of scope — do not build

Dynamic codes, URL shortening, redirects, scan analytics, logo overlays, PNG or any raster output, a database, authentication, a web UI.

If you think something outside this list is needed, stop and ask rather than adding it.
