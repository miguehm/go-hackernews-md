# hnmd

`hnmd` is a Go backend that periodically fetches the top Hacker News stories, extracts and converts their content to Markdown, and serves a file-based API for the KindleHome KOReader plugin.

The API is split into a small metadata index and on-demand Markdown downloads:

- `GET /v1/top` returns metadata only (never article bodies).
- `GET|HEAD /v1/articles/{itemId}.md` returns the article as a Markdown file.
- The server does not render or serve HTML; the client converts Markdown to HTML on-device.

Scraping runs in the background. API requests never trigger scraping; they only read the latest in-memory snapshot.

The scraper only fetches `http`/`https` URLs, follows at most 5 redirects, ignores proxy environment variables and refuses to connect to non-public addresses (loopback, private, link-local, CGNAT, metadata and other reserved ranges). The check runs on the resolved IP at dial time, so it also covers redirects and DNS rebinding. When an article cannot be captured, `content_error` holds a stable code (`fetch_failed`, `fetch_timeout`, `fetch_blocked`, `unsupported_scheme`, `too_many_redirects`, `unsupported_content_type`, `body_too_large`, `upstream_status_<code>`, `extract_failed`, `convert_failed`, `too_large`, `hn_unavailable`, `item_unavailable`); the underlying error is only written to the server log.

## Features

- Background refresh loop with initial warm-up on startup
- Top 20 Hacker News stories per cycle, with bounded concurrency
- Metadata index plus per-item Markdown files
- Last-known-good article retention across refresh cycles
- Header-only Markdown for deleted/dead/empty items
- In-memory, thread-safe snapshot store
- Optional bearer API key authentication
- Index capped at 128 KiB, articles capped at 512 KiB
- Todo module: JSON + Markdown views, flat `#tags`, optional due dates (`due_soon`/`overdue` filters), deterministic ordering
- Persistent, atomic JSON store for tasks with `.bak` fallback
- Conditional reads (`If-None-Match` → `304`) and gzip for `/v1/top`, articles, `/v1/todos` and `/v1/todos/tags`
- Offset pagination for `/v1/top`, `/v1/todos` and `/v1/todos/tags`
- Optimistic concurrency on task writes via mandatory `If-Match`
- Idempotent task creation with a client-provided `id`
- Configurable IANA timezone for due-date filtering and rendering
- `/v1/status` endpoint for readiness and snapshot metadata

## Architecture

1. `cmd/server/main.go` loads configuration, starts the refresher, and serves HTTP.
2. `internal/service` fetches top story IDs and items from the Hacker News API and processes them concurrently.
3. `internal/scraper` fetches and extracts article HTML; `internal/markdown` converts HTML to Markdown and renders the final article header.
4. `internal/store` keeps one immutable snapshot containing the metadata index and article bodies.
5. `internal/api` serves `/v1/status`, `/v1/top`, `/v1/articles/{itemId}.md`, the todo endpoints under `/v1/todos*`, the deprecated `/top` alias, and `/health` behavior from `main`.
6. `internal/todo` parses `#tags`, persists tasks in a JSON file and renders the Markdown views.

| Package | Responsibility |
| --- | --- |
| `cmd/server` | Entrypoint, env config, HTTP server, graceful shutdown |
| `internal/api` | HTTP handlers, auth middleware, JSON errors |
| `internal/service` | Refresh cycle, concurrency, status mapping, retention, size limits |
| `internal/store` | Index/article models and thread-safe snapshot access |
| `internal/todo` | Task model, `#tag` parsing, JSON persistence, Markdown rendering |
| `internal/hn` | Hacker News Firebase API client |
| `internal/scraper` | HTML fetch (timeouts/limits/content-type, SSRF guard) and readability extraction |
| `internal/markdown` | HTML-to-Markdown conversion, article rendering, slug/filename helpers |

## Requirements

- Go 1.27.3 (see `go.mod`)

## Quickstart

```bash
go mod tidy
go run ./cmd/server
```

By default the server listens on `http://localhost:8982`.

```bash
curl -H "Authorization: Bearer <key>" http://localhost:8982/v1/top
curl -H "Authorization: Bearer <key>" http://localhost:8982/v1/articles/42123456.md

curl -H "Authorization: Bearer <key>" http://localhost:8982/v1/todos.md
curl -X POST -H "Authorization: Bearer <key>" -H "Content-Type: application/json" \
  -d '{"text":"Comprar leche #casa #urgente"}' \
  http://localhost:8982/v1/todos/tasks
```

## Configuration

Environment variables read at startup:

| Variable | Default | Format / Notes | Description |
| --- | --- | --- | --- |
| `PORT` | `8982` | string | HTTP listen port |
| `REFRESH_INTERVAL` | `30m` | Go duration (`time.ParseDuration`, e.g. `10m`, `1h`) | Background refresh interval. Must be positive; invalid values cause startup failure. Also drives `refresh_interval_seconds`. |
| `SCRAPE_TIMEOUT` | `10s` | Go duration (positive) | Timeout per target site fetch. Invalid values cause startup failure. |
| `HN_TIMEOUT` | `10s` | Go duration (positive) | Timeout for Hacker News API requests. Invalid values cause startup failure. |
| `API_KEYS` | _(empty)_ | Comma-separated string(s) | When set, enables API key auth. Protected endpoints require `Authorization: Bearer <key>`. Multiple keys are accepted for rotation. |
| `PROTECT_HEALTH` | `false` | `true/false`, `1/0`, `yes/no`, `on/off` | When `true` and auth is enabled, `GET /health` also requires a valid API key. |
| `DATA_DIR` | `./data` | path | Directory for persistent data. Docker defaults to `/data`. |
| `TODO_FILE` | `<DATA_DIR>/todos.json` | path | JSON file used by the todo store. A `.bak` copy is kept. |
| `TODO_TIMEZONE` | `UTC` | IANA timezone | Default timezone for todo `due_soon`/`overdue`, counts and rendering. Can be overridden per request with `tz`. Invalid values fall back to UTC with a warning. |
| `TODO_MAX_TASKS` | `1000` | integer | Maximum number of stored tasks. |
| `TODO_ALLOW_INSECURE_WRITES` | `false` | `true/false`, `1/0`, `yes/no`, `on/off` | Allow todo writes when `API_KEYS` is unset. Off by default. |
| `PUBLIC_BASE_URL` | _(empty)_ | URL | Optional external base URL used for absolute `hn_url` values in `/v1/todos/top`. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` | Minimum log level. Invalid values cause startup failure. |
| `LOG_FORMAT` | `json` | `json`, `text` | Log output format on stderr. Invalid values cause startup failure. |

### Logging

Logs are structured (`log/slog`) and written to stderr, as JSON by default. Every request produces one `request` line with `request_id`, `method`, `path`, `route` (the matched pattern), `status`, `bytes` and `duration_ms`. Server errors are logged at `ERROR`, warm-up `503`s at `WARN` and `/health` probes at `DEBUG`. A panic in a handler is turned into a `500 internal_error` JSON response and logged with its stack trace.

Every response carries `X-Request-ID`. A valid incoming `X-Request-ID` (`[A-Za-z0-9._-]{1,64}`) is reused; otherwise the server generates one.
| `CORS_ORIGINS` | `web,http://localhost:5173,https://localhost,capacitor://localhost` | Comma-separated origins | Browser allowlist. Overrides the defaults when set; `*` is ignored. `OPTIONS` preflight is answered with `204` before auth. |

> **Security:** when authentication is enabled, run behind HTTPS so the bearer key is not exposed in transit. Keys are compared in constant time. If `API_KEYS` is unset, auth is disabled and a startup warning is logged.

Values fixed in code:

| Setting | Value | Notes |
| --- | --- | --- |
| Top stories | `20` | Number of HN items per snapshot |
| Max concurrency | `6` | Bounded concurrent item processing |
| Max HTML body size | `5MB` | Per-page fetch limit |
| Max index size | `128 KiB` | Response is trimmed by rank if necessary |
| Max article size | `512 KiB` | Larger articles are reported as `error` / `too_large` |
| Todo JSON index | `128 KiB` | Trimmed by dropping tasks from the tail |
| Todo Markdown | `512 KiB` | Truncated with a visible note |
| Todo write body | `64 KiB` | `413 payload_too_large` above this |
| Tags per task | `10` | Flat, lowercase, max 32 chars each |

## API

The canonical contract is [`openapi.yaml`](./openapi.yaml) (OpenAPI 3.0.3). The production base URL is `https://hnmd.miguehm.com/v1`.

### CORS

Browser clients are limited to the origins in `CORS_ORIGINS`. For an allowed
origin the server returns `Access-Control-Allow-Origin` and exposes `ETag`,
`Retry-After`, `Location` and `X-Request-ID`. Preflight requests are answered with `204` before
authentication:

```bash
curl -i -X OPTIONS http://localhost:8982/v1/todos/tasks \
  -H 'Origin: http://localhost:5173' \
  -H 'Access-Control-Request-Method: PATCH' \
  -H 'Access-Control-Request-Headers: authorization,content-type,if-match'
# 204 No Content
# Access-Control-Allow-Methods: GET, HEAD, POST, PATCH, DELETE, OPTIONS
# Access-Control-Allow-Headers: Authorization, Content-Type, If-Match, If-None-Match, X-Request-ID
# Access-Control-Expose-Headers: ETag, Retry-After, Location, X-Request-ID
# Vary: Origin
```

All CORS responses include `Vary: Origin`. A disallowed origin gets no
`Access-Control-Allow-Origin`, so the browser blocks the response.

### `GET /v1/top`

Returns the metadata index. This endpoint never triggers scraping.

Query parameters:

- `limit` (integer, 1-30, default 20): maximum number of items. Invalid values are clamped: missing or non-numeric means 20, values below 1 become 1, values above 30 become 30.
- `offset` (integer, >=0, default 0): first item index. Invalid or negative values become 0.
- `If-None-Match` (header, optional): revalidate with the previous `ETag`; a match returns `304 Not Modified` with no body.

Response fields:

- `schema_version`: currently `1`
- `generated_at`: RFC3339 timestamp
- `next_refresh_at`: RFC3339 timestamp or `null`
- `refresh_interval_seconds`: integer or `null`
- `offset`, `limit`: effective page values
- `returned_count`: number of items in `items`
- `truncated`: `true` when the tail or page was cut by `limit` or by the 128 KiB cap
- `has_more`: whether another page is available
- `next_offset`: next `offset` to request when `has_more` is `true`, otherwise `null`
- `items`: array of metadata items

Because the server fetches 20 stories, `returned_count` is at most 20 even though `limit` accepts up to 30.

Responses use weak `ETag` (`W/"..."`), `Last-Modified`, `Cache-Control: private, no-cache` and `Vary: Accept-Encoding`. Gzip is applied when the client sends `Accept-Encoding: gzip`.

Example:

```bash
curl -H "Authorization: Bearer <key>" \
  "https://hnmd.miguehm.com/v1/top?limit=20"
```

```json
{
  "schema_version": 1,
  "generated_at": "2026-11-16T12:00:00Z",
  "next_refresh_at": "2026-11-16T12:30:00Z",
  "refresh_interval_seconds": 1800,
  "offset": 0,
  "limit": 20,
  "returned_count": 1,
  "truncated": false,
  "has_more": false,
  "next_offset": null,
  "items": [
    {
      "id": "42123456",
      "rank": 1,
      "type": "story",
      "title": "A title about something",
      "hn_url": "https://news.ycombinator.com/item?id=42123456",
      "url": "https://example.com/post",
      "by": "alice",
      "score": 231,
      "descendants": 87,
      "published_at": "2026-11-16T10:12:00Z",
      "domain": "example.com",
      "content_status": "ok",
      "content_error": null,
      "content_url": "/v1/articles/42123456.md",
      "content_type": "text/markdown; charset=utf-8",
      "suggested_filename": "42123456-a-title-about-something.md",
      "content_length": 18432,
      "updated_at": "2026-11-16T11:58:00Z"
    }
  ]
}
```

### `GET|HEAD /v1/articles/{itemId}.md`

Returns the article as a Markdown file. `HEAD` returns the same status and headers without a body.

- `Content-Type: text/markdown; charset=utf-8`
- `Content-Length`: exact byte count, at most 512 KiB
- `Content-Disposition: inline; filename="<id>-<slug>.md"`
- `ETag`: weak validator (`W/"..."`); send it as `If-None-Match` to get `304 Not Modified`
- `Last-Modified`: informational; `Cache-Control: private, no-cache`
- `Vary: Accept-Encoding`; gzip is applied when the client accepts it
- Body: visible Markdown header followed by the article body, or header-only for empty/deleted items

The article header format is:

```markdown
# A title about something

> alice · 231 points · 87 comments · 2026-11-16
> HN: https://news.ycombinator.com/item?id=42123456
> URL: https://example.com/post

Body in Markdown...
```

The `URL:` line is omitted when the item has no external URL. There is no YAML frontmatter.

### `GET /v1/status`

Returns readiness and snapshot metadata. Protected by the same bearer key as the rest of `/v1` when `API_KEYS` is set. The response uses `Cache-Control: no-store`.

```bash
curl -H "Authorization: Bearer <key>" https://hnmd.miguehm.com/v1/status
```

```json
{
  "schema_version": 1,
  "version": "1.5.0",
  "auth_enabled": true,
  "ready": true,
  "snapshot_generated_at": "2026-11-22T14:00:00Z",
  "next_refresh_at": "2026-11-22T14:30:00Z",
  "refresh_interval_seconds": 1800,
  "todo_count": 10,
  "server_time": "2026-11-22T14:10:00Z"
}
```

`ready` is `false` until the first Hacker News snapshot is available. The `snapshot_*` fields are omitted when no snapshot exists yet.

When the latest refresh failed, the response also includes `last_refresh_error` (`hn_unavailable` or `refresh_timeout`) and `last_refresh_error_at`. They are omitted after the next successful refresh, and they also appear during warm-up, so `ready: false` plus `last_refresh_error` means the first refresh is failing.

### `GET /top` (deprecated)

Deprecated alias for `/v1/top`. It returns the same new index shape and includes `Deprecation: true`. The alias is documented here but is not part of the canonical OpenAPI contract.

### `GET /health`

Simple operational health check, outside the KindleHome OpenAPI contract.

- `200 OK` (`text/plain`): `ok`

Public by default. Set `PROTECT_HEALTH=true` to require the same bearer API key when auth is enabled.

### Todos

The todo module lives under `/v1/todos*`. All endpoints accept the same bearer key as the rest of `/v1`.

The canonical contract for schemas, error codes and header semantics is [`openapi.yaml`](./openapi.yaml): see the `/todos*` paths and the `TodoTask`, `TodoIndexResponse`, `TodoTagsResponse`, `CreateTodoRequest`, `UpdateTodoRequest` and `ErrorResponse` components. It also contains worked examples of the generated Markdown and of each todo error response.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/todos?tag=&status=&due_days=` | JSON index with counts and tag counters |
| `GET\|HEAD` | `/v1/todos.md?tag=&status=&due_days=` | Full Markdown list |
| `GET` | `/v1/todos/tags?due_days=` | Tag counters and untagged count |
| `GET\|HEAD` | `/v1/todos/tags/{tag}.md?status=&due_days=` | Flat Markdown for one tag |
| `GET` | `/v1/todos/top?tags=true` | KindleHome-compatible index with `type: "todo"` items |
| `POST` | `/v1/todos/tasks` | Create task |
| `GET` | `/v1/todos/tasks/{id}` | Get task |
| `PATCH` | `/v1/todos/tasks/{id}` | Update title/tags/details/due date/state |
| `DELETE` | `/v1/todos/tasks/{id}` | Delete task (requires `If-Match`, returns `204` even if already missing) |

`GET /v1/todos` and `GET /v1/todos/tags` accept `offset` (default 0) and `limit` (default 100, max 500) and return `offset`, `limit`, `returned_count`, `truncated`, `has_more` and `next_offset`. The `tags` and `counts` fields are global counters; only `items`/`tags` are paginated.

These two JSON indexes are cacheable with `Cache-Control: private, no-cache`. They carry a stable weak `ETag` and `Last-Modified`; send the ETag back as `If-None-Match` to get `304 Not Modified` with no body. `generated_at` equals the store's last modification time, so it no longer changes on every request. Task responses, task Markdown and error responses keep `Cache-Control: no-store`.

```bash
ETAG=$(curl -sD - -o /dev/null http://localhost:8982/v1/todos -H "Authorization: Bearer <key>" \
  | awk -F': ' 'tolower($1)=="etag"{gsub(/\r/,"",$2); print $2}')
curl -i http://localhost:8982/v1/todos -H "Authorization: Bearer <key>" -H "If-None-Match: $ETAG"
# 304 Not Modified
```

All todo date filters and rendering accept `tz` (IANA name, e.g. `Europe/Madrid`). It overrides `TODO_TIMEZONE` for that request; invalid values return `400 invalid_request` with `field: tz`.

Task responses carry `ETag: "<revision>"`. `PATCH` and `DELETE` require `If-Match` with that revision (strong or weak) or `*`; a missing header returns `428 precondition_required` and a stale revision returns `412 revision_conflict` with `current_revision`.

`POST /v1/todos/tasks` accepts an optional client-generated `id`. If it already exists, the server returns `200` with the stored task instead of `201`, making retries safe.

`status` is `open` (default), `done`, `all`, `due_soon` or `overdue`. `tag` matches any of a task's tags. `due_days` (1–30, default 3) sets the "due soon" horizon; out-of-range values return `400` with `field: due_days`. Both `due_soon` and `overdue` apply only to open tasks: `overdue` means the deadline is before today (UTC), and `due_soon` means today through today + `due_days`, inclusive. Dates are compared as `YYYY-MM-DD` strings so no timezone conversion is needed for the task itself.

Create a task by typing hashtags in `text`:

```bash
curl -X POST -H "Authorization: Bearer <key>" -H "Content-Type: application/json" \
  -d '{"text":"Comprar leche #casa #urgente","due_date":"2026-11-30"}' \
  http://localhost:8982/v1/todos/tasks
```

`due_date` is optional and must be a valid `YYYY-MM-DD` date. It is returned as `due_date` in task JSON (omitted when absent) and rendered as `(vence 2026-11-30)` in the Markdown. Clear it with `{"due_date":null}` or `{"due_date":""}` on PATCH. Invalid dates return `400 invalid_request` with `field: due_date`.

The server extracts `#casa` and `#urgente`, normalizes them to lowercase and removes them from the stored title. Multiple tags are supported; the first one is the primary tag used for grouping in the full Markdown view. A filter by a non-primary tag still finds the task.

An optional `details` note can be supplied on create or patch (up to 1000 bytes, line breaks preserved). It is returned as `details` in task JSON and rendered as indented lines under the task in both Markdown views. Existing tasks without the field simply report no details:

```bash
curl -X PATCH -H "Authorization: Bearer <key>" -H "If-Match: \"<revision>\"" -H "Content-Type: application/json" \
  -d '{"details":"Llamar a la tienda antes de las 18:00"}' \
  http://localhost:8982/v1/todos/tasks/<id>
```

On PATCH, `text` updates the title only: it never clears the existing tags (stored titles have their hashtags stripped). Hashtags in a patched `text` are merged with the current tags, lowercased and deduplicated. Only an explicit `tags` field replaces the list — and `"tags":[]` clears all tags:

```bash
curl -X PATCH -H "Authorization: Bearer <key>" -H "If-Match: \"<revision>\"" -H "Content-Type: application/json" \
  -d '{"text":"Comprar leche entera #urgente"}' \
  http://localhost:8982/v1/todos/tasks/<id>   # keeps #casa, adds #urgente

curl -X PATCH -H "Authorization: Bearer <key>" -H "If-Match: \"<revision>\"" -H "Content-Type: application/json" \
  -d '{"tags":[]}' http://localhost:8982/v1/todos/tasks/<id>   # clears all tags
```

Mark it done:

```bash
curl -X PATCH -H "Authorization: Bearer <key>" -H "If-Match: \"<revision>\"" -H "Content-Type: application/json" \
  -d '{"done":true}' http://localhost:8982/v1/todos/tasks/<id>
```

Ordering is deterministic: open tasks with a deadline come first, sorted by date ascending (overdue first), followed by open tasks without a deadline grouped by primary tag alphabetically, untagged last, oldest first within each group; done tasks appear under `## Hechas` sorted by completion time. There is no manual reordering.

The Markdown is mutable, so clients must re-download it on refresh instead of applying the article "download only if missing" rule. Responses use `Cache-Control: no-store`.

Writes require `API_KEYS`. If auth is disabled, write endpoints return `403` unless `TODO_ALLOW_INSECURE_WRITES=true`.

#### Behavior

- **Persistence:** tasks live in `TODO_FILE` with `schema_version: 1`. Writes are atomic and a `.bak` copy is kept; if a save fails, the stored tasks are left unchanged. Tasks survive restarts, and only one server instance should write the file.
- **Validation:** tags are canonicalized to lowercase and deduplicated; `#café` stays plain text. Text, title, tag and details limits are listed in [Configuration](#configuration). `due_date` must be `YYYY-MM-DD`; invalid values return `400 invalid_request` with `field: due_date`. Tasks saved by older versions load without `details` or `due_date`; the store schema stays at version 1.
- **State:** completing a task sets `done_at`; reopening it clears `done_at`. Every change updates `updated_at` and increments `revision`; the `ETag` on task responses reflects that revision and must be sent as `If-Match` on the next write.
- **Counts:** `counts` and `tags` in `GET /v1/todos` are global and ignore the `tag`/`status` filters; only `items` is filtered. They also include `due_soon` and `overdue`, computed with `due_days` (default 3) and today in the effective timezone (`tz` or `TODO_TIMEZONE`, default UTC).
- **Truncation/pagination:** JSON indexes are paginated and trimmed from the tail when they exceed the size limits in [Configuration](#configuration); `truncated: true`, `has_more` and `next_offset` describe the result. The Markdown view is shortened and includes a visible `> Lista truncada.` note.

## Content status

`content_status` is the source of truth for whether the client should download an article:

| Status | Meaning | `.md` endpoint |
| --- | --- | --- |
| `ok` | Markdown is available and within limits | `200` |
| `no_content` | No item metadata available to render | `404` |
| `error` | HN/fetch/extract/convert failure, or article over 512 KiB | `404` |
| `warming_up` | Reserved for future use; not emitted per item in the current design | `404` |

If the service has no ready snapshot yet, both endpoints return `503` with `Retry-After`. A missing or unavailable item returns `404`; the client keeps any local copy it already has.

## Error responses

```json
{
  "error": "unauthorized",
  "message": "Invalid or missing API key"
}
```

```json
{
  "error": "not_found",
  "message": "No markdown available for this item",
  "item_id": "42123456"
}
```

```json
{
  "error": "warming_up",
  "message": "Server is warming up, try again later",
  "retry_after_seconds": 15
}
```

While warming up, `Retry-After` is a fixed 15 seconds: the first refresh normally completes within seconds, so clients should not wait a full refresh interval.

`ErrorResponse.error` is one of `unauthorized`, `forbidden`, `warming_up`, `invalid_request`, `not_found`, `method_not_allowed`, `payload_too_large`, `precondition_required`, `revision_conflict`, or `internal_error`.

Conditional write failures:

```json
{
  "error": "precondition_required",
  "message": "If-Match header is required for task writes",
  "field": "if_match"
}
```

```json
{
  "error": "revision_conflict",
  "message": "task revision does not match",
  "current_revision": 3
}
```

A successful conditional read on `/v1/top`, articles, `/v1/todos` or `/v1/todos/tags` returns `304 Not Modified` with no body, the same weak `ETag` and `Last-Modified`.

## Behavior and limitations

- **No persistence (HN):** snapshots live in memory and are lost on restart. Todo tasks are persisted separately (see [Todos](#todos)).
- **Warm-up:** until the first ready snapshot exists, `/v1/top` and `/v1/articles/{id}.md` return `503` with `Retry-After`; `/v1/status` returns `200` with `ready: false`.
- **Partial failures:** individual item failures do not fail the whole snapshot.
- **Retention:** while an item remains in the top 20, its last successful article body is reused if a refresh fails. The article header is re-rendered with current metadata.
- **Full refresh failures:** if fetching the top story list fails, the last valid snapshot is kept and the error is retained internally for logs.
- **Header-only articles:** deleted/dead items, Ask HN posts without text, and empty extractions are still served as `ok` with a visible header.
- **Limits:** the index is kept under 128 KiB by trimming the lowest-ranked items; articles over 512 KiB are not stored.
- **Third-party dependencies:** relies on the Hacker News Firebase API and external websites; availability, rate limiting, blocking, and content quality can affect results.
- **Extraction quality:** readability/Markdown conversion quality varies by source page.
- **Authentication:** optional bearer auth when `API_KEYS` is set. Use HTTPS in production.
- **Conditional reads/gzip:** `/v1/top`, articles, `/v1/todos` and `/v1/todos/tags` support weak `ETag` revalidation and gzip with `Vary: Accept-Encoding`.
- **CORS:** browser origins are restricted to `CORS_ORIGINS`; `OPTIONS` preflight returns `204` before auth and responses include `Vary: Origin`.
- **Pagination/status:** list endpoints support `offset`/`limit`; `/v1/status` reports readiness without triggering scraping.
- **Todo module:** persistence, validation, truncation, refresh and write permissions are described in [Todos](#todos).

## Development

```text
cmd/server/          # application entrypoint
internal/api/        # HTTP handlers, auth, errors
internal/hn/         # Hacker News API client
internal/markdown/   # HTML-to-Markdown, rendering, slug helpers
internal/scraper/    # page fetch and content extraction
internal/service/    # refresh orchestration and item processing
internal/store/      # index/article models and in-memory store
internal/todo/       # task model, tags, JSON store, Markdown rendering
```

Key dependencies (see `go.mod` / `go.sum`): `github.com/go-shiori/go-readability`, `github.com/JohannesKaufmann/html-to-markdown/v2`, `golang.org/x/sync`.

Verification:

```bash
gofmt -w .
go vet ./...
go test ./...
go build ./...
```

## 1.6.0 changes

- `/v1/todos` and `/v1/todos/tags` use a stable weak `ETag`, support `If-None-Match` → `304`, and return `Last-Modified`.
- `generated_at` in those two indexes is now the store's last modification time instead of the request time.
- Those indexes use `Cache-Control: private, no-cache`; mutable task/Markdown responses keep `no-store`.
- Added CORS with a configurable origin allowlist (`CORS_ORIGINS`) and unauthenticated `OPTIONS` preflight returning `204`.

See [`CHANGELOG.md`](./CHANGELOG.md) for migration details.

## 1.5.0 changes

- `PATCH` and `DELETE /v1/todos/tasks/{id}` now require `If-Match`; clients must send the task `ETag` (or `*`).
- `POST /v1/todos/tasks` accepts an optional `id` and returns `200` on a retry instead of a duplicate `201`.
- `/v1/top`, `/v1/todos` and `/v1/todos/tags` are paginated with `offset`/`limit` and expose `has_more`/`next_offset`.
- `/v1/top` and articles support `If-None-Match` with weak ETags and gzip responses.
- Todo date filters/rendering use `tz` (or `TODO_TIMEZONE`, default UTC).
- Added `/v1/status` for readiness and snapshot metadata.

See [`CHANGELOG.md`](./CHANGELOG.md) for migration details.

## Breaking changes from the old snapshot API

- `/top` is now a deprecated alias for `/v1/top`; the canonical path is `/v1/top`.
- The index no longer embeds `markdown`; article bodies are downloaded from `/v1/articles/{itemId}.md`.
- `id` is a string, not an integer.
- Snapshot fields `count`, `refresh_interval`, `last_refresh_error`, `time`, `status`, `error`, and `scraped_at` are gone; use `returned_count`, `refresh_interval_seconds`, `content_status`, `content_error`, and `updated_at`.
- The warm-up error uses `error: "warming_up"` instead of `status: "warming_up"`.

## License

MIT. See [`LICENSE`](./LICENSE).
