# hnmd

`hnmd` is a Go backend that periodically fetches the top Hacker News stories, extracts and converts their content to Markdown, and serves a file-based API for the KindleHome KOReader plugin.

The API is split into a small metadata index and on-demand Markdown downloads:

- `GET /v1/top` returns metadata only (never article bodies).
- `GET|HEAD /v1/articles/{itemId}.md` returns the article as a Markdown file.
- The server does not render or serve HTML; the client converts Markdown to HTML on-device.

Scraping runs in the background. API requests never trigger scraping; they only read the latest in-memory snapshot.

## Features

- Background refresh loop with initial warm-up on startup
- Top 20 Hacker News stories per cycle, with bounded concurrency
- Metadata index plus per-item Markdown files
- Last-known-good article retention across refresh cycles
- Header-only Markdown for deleted/dead/empty items
- In-memory, thread-safe snapshot store
- Optional bearer API key authentication
- Index capped at 128 KiB, articles capped at 512 KiB
- Todo module: JSON + Markdown views, flat `#tags`, deterministic ordering
- Persistent, atomic JSON store for tasks with `.bak` fallback

## Architecture

1. `cmd/server/main.go` loads configuration, starts the refresher, and serves HTTP.
2. `internal/service` fetches top story IDs and items from the Hacker News API and processes them concurrently.
3. `internal/scraper` fetches and extracts article HTML; `internal/markdown` converts HTML to Markdown and renders the final article header.
4. `internal/store` keeps one immutable snapshot containing the metadata index and article bodies.
5. `internal/api` serves `/v1/top`, `/v1/articles/{itemId}.md`, the todo endpoints under `/v1/todos*`, the deprecated `/top` alias, and `/health` behavior from `main`.
6. `internal/todo` parses `#tags`, persists tasks in a JSON file and renders the Markdown views.

| Package | Responsibility |
| --- | --- |
| `cmd/server` | Entrypoint, env config, HTTP server, graceful shutdown |
| `internal/api` | HTTP handlers, auth middleware, JSON errors |
| `internal/service` | Refresh cycle, concurrency, status mapping, retention, size limits |
| `internal/store` | Index/article models and thread-safe snapshot access |
| `internal/todo` | Task model, `#tag` parsing, JSON persistence, Markdown rendering |
| `internal/hn` | Hacker News Firebase API client |
| `internal/scraper` | HTML fetch (timeouts/limits/content-type) and readability extraction |
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
| `REFRESH_INTERVAL` | `30m` | Go duration (`time.ParseDuration`, e.g. `10m`, `1h`) | Background refresh interval. Invalid values cause startup failure. Also drives `Retry-After` and `refresh_interval_seconds`. |
| `SCRAPE_TIMEOUT` | `10s` | Go duration | Timeout per target site fetch |
| `HN_TIMEOUT` | `10s` | Go duration | Timeout for Hacker News API requests |
| `API_KEYS` | _(empty)_ | Comma-separated string(s) | When set, enables API key auth. Protected endpoints require `Authorization: Bearer <key>`. Multiple keys are accepted for rotation. |
| `PROTECT_HEALTH` | `false` | `true/false`, `1/0`, `yes/no`, `on/off` | When `true` and auth is enabled, `GET /health` also requires a valid API key. |
| `DATA_DIR` | `./data` | path | Directory for persistent data. Docker defaults to `/data`. |
| `TODO_FILE` | `<DATA_DIR>/todos.json` | path | JSON file used by the todo store. A `.bak` copy is kept. |
| `TODO_MAX_TASKS` | `1000` | integer | Maximum number of stored tasks. |
| `TODO_ALLOW_INSECURE_WRITES` | `false` | `true/false`, `1/0`, `yes/no`, `on/off` | Allow todo writes when `API_KEYS` is unset. Off by default. |
| `PUBLIC_BASE_URL` | _(empty)_ | URL | Optional external base URL used for absolute `hn_url` values in `/v1/todos/top`. |

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

### `GET /v1/top`

Returns the metadata index. This endpoint never triggers scraping.

Query parameters:

- `limit` (integer, 1-30, default 20): maximum number of items. Invalid values are clamped: missing or non-numeric means 20, values below 1 become 1, values above 30 become 30.

Response fields:

- `schema_version`: currently `1`
- `generated_at`: RFC3339 timestamp
- `next_refresh_at`: RFC3339 timestamp or `null`
- `refresh_interval_seconds`: integer or `null`
- `returned_count`: number of items in `items`
- `truncated`: `true` when the tail was cut by `limit` or by the 128 KiB cap
- `items`: array of metadata items

Because the server fetches 20 stories, `returned_count` is at most 20 even though `limit` accepts up to 30.

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
  "returned_count": 1,
  "truncated": false,
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
- `ETag` and `Last-Modified`: informational only; conditional requests are not supported
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
| `GET` | `/v1/todos?tag=&status=` | JSON index with counts and tag counters |
| `GET\|HEAD` | `/v1/todos.md?tag=&status=` | Full Markdown list |
| `GET` | `/v1/todos/tags` | Tag counters and untagged count |
| `GET\|HEAD` | `/v1/todos/tags/{tag}.md?status=` | Flat Markdown for one tag |
| `GET` | `/v1/todos/top?tags=true` | KindleHome-compatible index with `type: "todo"` items |
| `POST` | `/v1/todos/tasks` | Create task |
| `GET` | `/v1/todos/tasks/{id}` | Get task |
| `PATCH` | `/v1/todos/tasks/{id}` | Update title/tags/details/state |
| `DELETE` | `/v1/todos/tasks/{id}` | Delete task (idempotent `204`) |

`status` is `open` (default), `done` or `all`. `tag` matches any of a task's tags.

Create a task by typing hashtags in `text`:

```bash
curl -X POST -H "Authorization: Bearer <key>" -H "Content-Type: application/json" \
  -d '{"text":"Comprar leche #casa #urgente"}' \
  http://localhost:8982/v1/todos/tasks
```

The server extracts `#casa` and `#urgente`, normalizes them to lowercase and removes them from the stored title. Multiple tags are supported; the first one is the primary tag used for grouping in the full Markdown view. A filter by a non-primary tag still finds the task.

An optional `details` note can be supplied on create or patch (up to 1000 bytes, line breaks preserved). It is returned as `details` in task JSON and rendered as indented lines under the task in both Markdown views. Existing tasks without the field simply report no details:

```bash
curl -X PATCH -H "Authorization: Bearer <key>" -H "Content-Type: application/json" \
  -d '{"details":"Llamar a la tienda antes de las 18:00"}' \
  http://localhost:8982/v1/todos/tasks/<id>
```

On PATCH, `text` updates the title only: it never clears the existing tags (stored titles have their hashtags stripped). Hashtags in a patched `text` are merged with the current tags, lowercased and deduplicated. Only an explicit `tags` field replaces the list — and `"tags":[]` clears all tags:

```bash
curl -X PATCH -H "Authorization: Bearer <key>" -H "Content-Type: application/json" \
  -d '{"text":"Comprar leche entera #urgente"}' \
  http://localhost:8982/v1/todos/tasks/<id>   # keeps #casa, adds #urgente

curl -X PATCH -H "Authorization: Bearer <key>" -H "Content-Type: application/json" \
  -d '{"tags":[]}' http://localhost:8982/v1/todos/tasks/<id>   # clears all tags
```

Mark it done:

```bash
curl -X PATCH -H "Authorization: Bearer <key>" -H "Content-Type: application/json" \
  -d '{"done":true}' http://localhost:8982/v1/todos/tasks/<id>
```

Ordering is deterministic: open tasks first, grouped by primary tag alphabetically, untagged last, oldest first within each group; done tasks appear under `## Hechas` sorted by completion time. There is no manual reordering.

The Markdown is mutable, so clients must re-download it on refresh instead of applying the article "download only if missing" rule. Responses use `Cache-Control: no-store`.

Writes require `API_KEYS`. If auth is disabled, write endpoints return `403` unless `TODO_ALLOW_INSECURE_WRITES=true`.

#### Behavior

- **Persistence:** tasks live in `TODO_FILE` with `schema_version: 1`. Writes are atomic and a `.bak` copy is kept; if a save fails, the stored tasks are left unchanged. Tasks survive restarts, and only one server instance should write the file.
- **Validation:** tags are canonicalized to lowercase and deduplicated; `#café` stays plain text. Text, title, tag and details limits are listed in [Configuration](#configuration). Invalid input returns `400 invalid_request` with a `field` when applicable. Tasks saved by older versions load without a `details` key; the store schema stays at version 1.
- **State:** completing a task sets `done_at`; reopening it clears `done_at`. Every change updates `updated_at` and increments `revision`; the `ETag` on task responses reflects that revision but is informational only.
- **Counts:** `counts` and `tags` in `GET /v1/todos` are global and ignore the `tag`/`status` filters; only `items` is filtered.
- **Truncation:** when a response exceeds the size limits in [Configuration](#configuration), the JSON index is trimmed from the tail and reports `truncated: true`; the Markdown view is shortened and includes a visible `> Lista truncada.` note.

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
  "retry_after_seconds": 1800
}
```

`ErrorResponse.error` is one of `unauthorized`, `warming_up`, `not_found`, or `internal_error`.

## Behavior and limitations

- **No persistence (HN):** snapshots live in memory and are lost on restart. Todo tasks are persisted separately (see [Todos](#todos)).
- **Warm-up:** until the first ready snapshot exists, both API endpoints return `503`.
- **Partial failures:** individual item failures do not fail the whole snapshot.
- **Retention:** while an item remains in the top 20, its last successful article body is reused if a refresh fails. The article header is re-rendered with current metadata.
- **Full refresh failures:** if fetching the top story list fails, the last valid snapshot is kept and the error is retained internally for logs.
- **Header-only articles:** deleted/dead items, Ask HN posts without text, and empty extractions are still served as `ok` with a visible header.
- **Limits:** the index is kept under 128 KiB by trimming the lowest-ranked items; articles over 512 KiB are not stored.
- **Third-party dependencies:** relies on the Hacker News Firebase API and external websites; availability, rate limiting, blocking, and content quality can affect results.
- **Extraction quality:** readability/Markdown conversion quality varies by source page.
- **Authentication:** optional bearer auth when `API_KEYS` is set. Use HTTPS in production.
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

## Breaking changes from the old snapshot API

- `/top` is now a deprecated alias for `/v1/top`; the canonical path is `/v1/top`.
- The index no longer embeds `markdown`; article bodies are downloaded from `/v1/articles/{itemId}.md`.
- `id` is a string, not an integer.
- Snapshot fields `count`, `refresh_interval`, `last_refresh_error`, `time`, `status`, `error`, and `scraped_at` are gone; use `returned_count`, `refresh_interval_seconds`, `content_status`, `content_error`, and `updated_at`.
- The warm-up error uses `error: "warming_up"` instead of `status: "warming_up"`.

## License

MIT. See [`LICENSE`](./LICENSE).
