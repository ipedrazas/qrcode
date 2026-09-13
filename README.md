# qrsvc

A small, stateless HTTP service that turns a URL into a **static** QR code, served as SVG. It also serves a small web page at `/` for making codes by hand.

The URL is encoded directly into the QR matrix. There is no shortener, no redirect endpoint, no database, no analytics and no persistence of any kind. The service never fetches, resolves or follows the URL; it works with all outbound network egress blocked.

## API

### `GET /qr`

| Param    | Required | Default   | Notes |
|----------|----------|-----------|-------|
| `url`    | yes      | —         | Absolute `http` or `https` URL to encode. At most `MAX_URL_LEN` bytes (default 2048). |
| `ec`     | no       | `M`       | Error correction level: `L`, `M`, `Q` or `H` (case-insensitive). |
| `margin` | no       | `4`       | Quiet zone in modules, `4`–`16`. 4 is the minimum the QR spec allows. |
| `fg`     | no       | `#000000` | Dark module colour, exactly `#rrggbb` (case-insensitive). |
| `bg`     | no       | `#ffffff` | Background colour, `#rrggbb`, or `none` for transparent. |

Remember to URL-encode parameter values. In particular, `#` must be sent as `%23`, because otherwise it starts the fragment and never reaches the server.

There is deliberately **no** `size` or `scale` parameter. SVG is vector: size it with `width`/`height` on the `<img>`, or with CSS.

Unknown parameters are rejected rather than ignored, so a typo like `?url=…&margn=8` fails loudly instead of quietly producing a code with the default margin. Repeating a parameter is also rejected.

**Success:** `200`, `Content-Type: image/svg+xml; charset=utf-8`.

The document has:
- `viewBox="0 0 N N"`, where `N = symbol size + 2 × margin`, in module units. There are no `width`/`height` attributes.
- One `<rect>` for the background, omitted when `bg=none`.
- One `<path shape-rendering="crispEdges">` holding all dark modules as horizontal runs.

There is no XML declaration, DOCTYPE, title, comment or ID. The URL appears only inside the encoded matrix.

**Errors:** a JSON body and never an SVG.

```json
{"code":"unsupported_scheme","param":"url","message":"url must be absolute and use the http or https scheme"}
```

| Status | `code` | When |
|---|---|---|
| 400 | `missing_parameter` | `url` is absent |
| 400 | `unknown_parameter` | a parameter other than `url`, `ec`, `margin`, `fg`, `bg` |
| 400 | `duplicate_parameter` | a parameter appears more than once |
| 400 | `invalid_query` | the query string cannot be parsed (bad `%` escape, `;` separator) |
| 400 | `invalid_url` | empty, not UTF-8, contains whitespace, unparseable, or has no host |
| 400 | `unsupported_scheme` | anything but `http`/`https`, including `javascript:`, `data:` and scheme-relative `//host` |
| 400 | `url_too_long` | longer than `MAX_URL_LEN` bytes |
| 400 | `capacity_exceeded` | the URL does not fit in a version 40 QR code at the requested `ec`; the message suggests the lower levels that would fit |
| 400 | `invalid_ec`, `invalid_margin`, `invalid_color` | bad value for that parameter (`param` says which) |
| 404 | `not_found` | unknown or non-canonical path (e.g. `//qr`, `/a/../qr`); the service never redirects |
| 405 | `method_not_allowed` | anything but `GET`/`HEAD` |
| 429 | `rate_limited` | per-client rate limit exceeded; see `Retry-After` |
| 500 | `internal_error` | a bug; details are logged, never returned |

`code` values are stable. Messages are for humans and may change. Error messages never echo the submitted URL.

Every URL is encoded in byte mode, so the version 40 capacity is exact:

| `ec` | Max bytes |
|---|---|
| L | 2953 |
| M | 2331 |
| Q | 1663 |
| H | 1273 |

With the default 2048-byte limit, only `Q` and `H` can hit `capacity_exceeded`.

### `GET /` (web UI)

A single page for making a code by hand. You type a URL (a bare `example.com/x` gets `https://`), optionally set the EC level, margin and colours, and download the SVG. The page calls `GET /qr` like any other client, so it offers nothing the API doesn't, and errors show the API's own message. Without JavaScript the form still works as a plain `GET /qr`.

It is served from files embedded in the binary, at exact paths only: `/`, `/app.js`, `/style.css`, `/favicon.svg` and `/favicon.ico`. There is no file server, so `/index.html` and anything else is a `404`. These files are revalidated on every load (`Cache-Control: no-cache` with a content-hash `ETag`), so a new build shows up immediately.

The browser-tab icon exists twice: `favicon.svg` for current browsers and `favicon.ico` (16 and 32 px) for the rest. Both come from one geometry in `internal/httpapi/gen_icons.go`; regenerate them with `task icons`.

### `GET /healthz`

Liveness probe. Returns `200` with `ok`. It is exempt from rate limiting.

### Examples

```sh
# Defaults: EC level M, 4-module quiet zone, black on white.
curl -G 'http://localhost:8080/qr' --data-urlencode 'url=https://example.com/' -o example.svg

# High error correction, wide margin, custom colours.
curl -G 'http://localhost:8080/qr' \
  --data-urlencode 'url=https://example.com/menu?table=12' \
  --data-urlencode 'ec=H' --data-urlencode 'margin=8' \
  --data-urlencode 'fg=#1a2b3c' --data-urlencode 'bg=#fafaf0' -o menu.svg

# Transparent background.
curl 'http://localhost:8080/qr?url=https%3A%2F%2Fexample.com%2F&bg=none'

# Errors are JSON.
curl -i 'http://localhost:8080/qr?url=javascript:alert(1)'
```

```html
<img src="https://qr.example.net/qr?url=https%3A%2F%2Fexample.com%2F" width="256" height="256" alt="QR code for example.com">
```

## Caching

A response is a pure function of its canonicalized parameters, so it is content-addressed:

- Parameters are canonicalized before anything else uses them. That covers upper-case `ec`, lower-case hex colours and applied defaults. `?url=X&ec=m`, `?ec=M&url=X` and `?url=X` therefore all describe the same image.
- `ETag` is a strong tag: a SHA-256 over the canonical parameter set plus an output-format version.
- `If-None-Match` is honoured with `304 Not Modified`, and the service skips encoding entirely in that case.
- `Cache-Control: public, max-age=31536000, immutable`.

Error responses carry `Cache-Control: no-store` and no `ETag`.

If a change to the renderer or an encoder upgrade alters the output bytes, the golden tests fail. Regenerate them with `task golden` and bump `qr.FormatVersion`, so every ETag changes with the bytes.

## Configuration

Environment variables only. All are optional.

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` | Listen port. |
| `RATE_LIMIT_RPS` | `10` | Sustained requests per second per client. `0` disables rate limiting. |
| `RATE_LIMIT_BURST` | `20` | Bucket size per client. |
| `MAX_URL_LEN` | `2048` | Maximum `url` length in bytes, `1`–`2953`. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Logs are JSON on stdout. |
| `LOG_URLS` | `false` | `true` adds the full URL to each successful `/qr` log line as `url`. See the threat model before enabling. |

Invalid values stop the service at startup with a message naming every bad variable.

HTTP server limits are fixed in code:
- `ReadHeaderTimeout` 5s
- `ReadTimeout` 10s
- `WriteTimeout` 10s
- `IdleTimeout` 60s
- `MaxHeaderBytes` 16 KiB

On `SIGINT`/`SIGTERM` the server stops accepting connections and gives in-flight requests up to 15 seconds to finish.

## Running

```sh
task run                 # go run ./cmd/qrsvc
task docker              # build the image
task docker:run          # run it with --read-only --cap-drop=ALL --security-opt=no-new-privileges
```

The image is a static `CGO_ENABLED=0` binary on `gcr.io/distroless/static-debian13:nonroot`: no shell, no package manager, UID 65532. The service writes nothing to disk, so always run it with a read-only root filesystem. In Kubernetes:

```yaml
securityContext:
  runAsNonRoot: true
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]
```

Tagging `v*` builds and pushes a multi-arch (`linux/amd64`, `linux/arm64`) image to `ghcr.io/<owner>/<repo>`.

### Seeing which URLs were processed

With `LOG_URLS=true` (e.g. `docker run -e LOG_URLS=true …`), each successful `/qr` request is logged with its URL. To list them, most requested first:

```sh
docker logs <container> 2>/dev/null \
  | jq -rR 'fromjson? | select(.msg=="request" and .url) | .url' \
  | sort | uniq -c | sort -rn
```

`-R` with `fromjson?` skips any line that isn't JSON. `2>/dev/null` drops stderr, since the JSON logs go to stdout. Some variations:

```sh
# Only codes actually generated, leaving out 304 cache hits
docker logs <container> 2>/dev/null \
  | jq -rR 'fromjson? | select(.msg=="request" and .url and .status==200) | .url' \
  | sort | uniq -c | sort -rn

# Only the last 24 hours: add --since
docker logs --since 24h <container> 2>/dev/null | jq -rR 'fromjson? | select(.url) | .url'

# Watch URLs as they arrive
docker logs -f <container> 2>/dev/null | jq -rR 'fromjson? | select(.url) | .url'
```

Without `LOG_URLS` you can still count requests and distinct URLs by grouping on `.url_sha256` instead of `.url`; you just can't see what the URLs were. `task docker:run` starts the container with `--rm`, so its logs go when it stops. To keep a history, run without `--rm` or ship the logs to a collector.

## Threat model

### Why static-only, and why there is no redirect endpoint

Sooner or later someone will ask for "dynamic" QR codes: a code that points at our short link and redirects wherever it is currently configured to go. That feature is not a small addition to this service. It is a different service with a different risk profile, and this one deliberately isn't it:

- **An open redirect on our domain.** A redirect endpoint lends our origin's reputation to every destination it forwards to. Phishing filters, users and corporate allow-lists trust links on our domain, and attackers will register codes that bounce through it.
- **Mutable destinations.** A printed code that redirects can be re-pointed after it has been reviewed, printed and stuck on a wall. Whoever controls the mapping controls every printed copy. A static code says exactly what it says, forever, and anyone can decode it and see the destination before visiting.
- **State means a database, authentication and abuse handling.** Codes need owners, owners need accounts, accounts get compromised, and reported malicious codes need takedown. Each of those is a system to build, secure and operate.
- **Scan analytics are tracking.** Recording who scanned what, when and from where is personal data collection, with the retention, consent and breach obligations that come with it.

A static generator has none of these problems. It holds no data, cannot be re-pointed, and cannot be used to launder a destination through our domain: the destination is in the image, in plain sight. If dynamic codes are ever needed, build them as a separate service with its own threat model rather than bolting a redirect onto this one.

### Why the URL is never fetched

The URL is a string to encode, nothing more. Fetching it — to validate that it resolves, to generate a preview, or to follow redirects to a "canonical" form — would turn the service into an SSRF primitive. Anyone could make our servers send requests to internal addresses (`http://169.254.169.254/`, `http://localhost:…`, private ranges), probe for open ports, or hit third parties from our IPs. Resolving the hostname alone already leaks request data to DNS and permits DNS rebinding tricks. So the service makes no outbound network calls at all, and should run with egress blocked. The tests enforce this:

- `TestNoOutboundConnections` requests codes for URLs that point at a live local listener and at hostnames that need DNS. It traps `http.DefaultTransport` and `net.DefaultResolver`, and asserts that the listener saw no connection and no trap fired.
- `TestNoNetworkClientCode` scans every non-test source file for HTTP-client, dialer and resolver identifiers.
- `TestNoNetworkDependencies` asserts that `internal/qr`'s transitive imports, including the encoder library's, contain no `net` package at all.

### Other properties

- **No XSS through the SVG.** SVG served from our origin is an active document, so interpolating user text into it would be stored XSS. The renderer writes only integers and colours it has formatted itself; the URL affects which modules are dark and nothing else. Colours are parsed into bytes and re-emitted, never copied from the query. Every response also carries `X-Content-Type-Options: nosniff` and, except for the UI page, `Content-Security-Policy: default-src 'none'; sandbox`. The fuzz tests check every successful response against a regular expression that admits only that fixed structure.
- **The UI page gets only the CSP it needs.** `/` is the one response that runs script. Its policy allows only its own script and stylesheet, `fetch` to its own origin, and images from itself or `blob:` (the generated code is shown from a blob of exactly the bytes `/qr` returned). It allows nothing inline and nothing from other origins, and `frame-ancestors 'none'` stops the page being framed. The script writes user and server text only through `textContent`, never as HTML. It keeps the URL out of the page's own address, so it stays out of browser history, and every UI response sends `Referrer-Policy: no-referrer`. Tests check that the page has no inline script, style or event handler that its CSP would silently block.
- **Only `http`/`https`.** Other schemes, including `javascript:`, `data:`, `file:`, `intent:` and scheme-relative input, are rejected. What a scanner does with a `javascript:` URL varies by app, and none of the answers is good.
- **URLs are not logged by default.** They are user data and often carry tokens (password resets, magic links, signed URLs). Logs record a 12-hex-digit SHA-256 prefix (`url_sha256`) and the byte length (`url_len`), which is enough to count requests and correlate reports without recording the URL. Error messages never echo the URL.

  Setting `LOG_URLS=true` also logs the full URL of every request that passes validation; rejected input is never logged. Only enable it if you are prepared to treat your logs as user data, with access control, retention limits and redaction to match, because any token in a submitted URL ends up in them.
- **Rate limiting.** A token bucket per client, keyed by IPv4 address or IPv6 `/64`, answers `429` with `Retry-After`. Idle buckets are forgotten, so memory is bounded by the number of recently active clients. The key is the TCP peer address; `X-Forwarded-For` is ignored because clients can forge it. **Behind a reverse proxy or load balancer, every request appears to come from the proxy.** Either rate limit at the proxy and set `RATE_LIMIT_RPS=0` here, or raise the limits accordingly.
- **Out of scope:** the service does not judge destinations. It will encode any syntactically valid `http(s)` URL, including a malicious one, exactly as a pen and paper would. Deciding which URLs your organisation prints is a policy question for whoever calls this service.

## Encoder choice

Encoding uses [`github.com/piglig/go-qr`](https://github.com/piglig/go-qr), a pure-Go port of Project Nayuki's reference QR Code generator. As of September 2026:

- **Maintained.** v1.1.0 was released in May 2026, the last push was in August 2026, and it has no open issues.
- **Exposes the raw matrix.** `Size()` and `Module(x, y)`, which is all we need to render the SVG ourselves.
- **Exact control.** Byte-mode segments, the requested EC level pinned (`boostEcl=false`), and automatic version and mask selection. Its data-too-long error is a sentinel we can map to `capacity_exceeded`.
- **Zero runtime dependencies** (testify is test-only), and no `net` imports anywhere in its graph.
- **Traceable algorithm.** It follows Nayuki's widely reviewed reference implementation.

Alternatives considered:

| Library | Why not |
|---|---|
| `github.com/skip2/go-qrcode` | The most-starred Go QR library, but its latest module version dates from June 2020, and 35 issues are open. |
| `rsc.io/qr` | Last version 2018. |
| `github.com/boombuler/barcode` | Maintained, but slower-moving (last push July 2025). It is a general barcode library whose QR matrix comes back as an `image.Image`. |
| `github.com/yeqown/go-qrcode/v2` | Actively maintained and a reasonable fallback. The matrix is reached through a writer or iterator interface, and it pulls in `golang.org/x/text` and a Reed–Solomon module. |

The encoder sits behind a three-line interface in `internal/qr`:

```go
type Encoder interface {
    Encode(payload []byte, ec ECLevel) (*Matrix, error)
}
```

To swap libraries, add an implementation next to `encoder_goqr.go` and change `qr.NewEncoder`. The HTTP layer never sees the library. The round-trip, version-boundary and golden tests tell you whether the new encoder is correct and whether its output bytes changed; if they did, bump `qr.FormatVersion`.

## Testing

```sh
task test    # go test -race ./...
task fuzz    # fuzz the url parameter (FUZZTIME=30s)
task golden  # regenerate SVG golden files
task lint    # golangci-lint (config in .golangci.yml)
```

- **Round trip** (`internal/qr/roundtrip_test.go`). This is the test that matters. It covers all four EC levels and URL lengths from 1 byte up to each level's version 40 capacity, including both sides of several version transitions, plus Unicode and awkward URLs. Each case is encoded and rendered to SVG. The SVG is parsed back into a grid, which must match the matrix exactly, then rasterized to an `image.Gray` and decoded with [ZXing (gozxing)](https://github.com/makiuchi-d/gozxing). ZXing is an independent, test-only decoder. The decoded text and EC level must equal the input. gozxing runs in `PURE_BARCODE` mode, because its finder-pattern detector occasionally mislocates patterns on perfectly sharp synthetic images. Full format, error-correction and data decoding still run.
- **Version boundaries.** Payloads exactly at, and one byte over, the ISO/IEC 18004 byte-mode capacity for versions 1, 2, 5, 10, 20, 39 and 40 must produce the expected symbol size, or `ErrCapacityExceeded` past version 40.
- **Golden files** in `internal/qr/testdata/golden` pin the exact bytes.
- **Determinism.** Many concurrent renders with fresh encoders must be byte-identical.
- **Quiet zone.** For margins 0–16, nothing is dark outside the symbol, and the finder corners sit exactly at the margin.
- **Fuzzing.** `FuzzURLParam` and `FuzzQuery` require every input to yield either a structurally exact SVG or a well-formed 400, never a panic.
- **Table tests** cover validation, canonicalization, ETag stability (including a pinned value), conditional requests, headers on every response, rate limiting, log redaction and configuration.
- **UI.** Every embedded file is served with the right type and revalidates to `304`. Every file the page links to resolves, and every element `app.js` looks up exists in the page. The page has nothing its CSP would block, and both favicons are well-formed.

## Layout

```
cmd/qrsvc/          main, configuration, graceful shutdown
internal/qr/        Encoder interface + go-qr adapter, SVG renderer
internal/httpapi/   routes, validation/canonicalization, caching, middleware, rate limiting
internal/httpapi/ui/  web UI (HTML, JS, CSS, favicons), embedded into the binary
```
