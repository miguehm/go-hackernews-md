# hnmd

`hnmd` is a Go backend that periodically fetches the top 20 Hacker News stories, extracts each story's content, converts it to Markdown, and serves a cached snapshot over HTTP as JSON.

Scraping runs in the background (one pass per refresh cycle). API requests never trigger scraping; they only return the currently cached snapshot from memory.

## Features

- Background refresh loop with initial warm-up on startup
- Top 20 Hacker News stories per cycle (bounded concurrent processing)
- Article extraction and HTML-to-Markdown conversion
- In-memory snapshot store (thread-safe reads/writes)
- Partial per-item failures are captured without failing the whole snapshot
- Last valid snapshot is preserved if a full refresh cycle fails

## Architecture

**Request/data flow**

1. `cmd/server/main.go` loads configuration, starts the refresher, and serves HTTP.
2. `internal/service` fetches top story IDs and items from the Hacker News API, then processes items concurrently.
3. For each item, `internal/scraper` fetches/extracts article HTML (or uses HN text for text-only posts) and `internal/markdown` converts/renders the final Markdown.
4. `internal/store` keeps the latest ready snapshot in memory.
5. `internal/api` serves cached data from the store (`GET /top`, `GET /health`).

**Packages**

| Package | Responsibility |
| --- | --- |
| `cmd/server` | Entrypoint, env config, HTTP server, graceful shutdown |
| `internal/api` | HTTP handlers (`/top`, `/health` behavior in main) |
| `internal/service` | Refresh cycle, concurrency control, per-item processing, error handling |
| `internal/store` | In-memory snapshot/item models and thread-safe access |
| `internal/hn` | Hacker News Firebase API client |
| `internal/scraper` | HTML fetch (timeouts/limits/content-type) and readability extraction |
| `internal/markdown` | HTML-to-Markdown conversion and Markdown rendering/metadata |

## Requirements

- Go 1.22+ (see `go.mod`)

## Quickstart

```bash
go mod tidy
go run ./cmd/server
```

By default the server listens on `http://localhost:8982`.

## Configuration

Environment variables read at startup:

| Variable | Default | Format / Notes | Description |
| --- | --- | --- | --- |
| `PORT` | `8982` | string | HTTP listen port |
| `REFRESH_INTERVAL` | `30m` | Go duration (`time.ParseDuration`, e.g. `10m`, `1h`) | Background refresh interval. **Invalid values cause startup failure.** |
| `SCRAPE_TIMEOUT` | `10s` | Go duration | Timeout per target site fetch |
| `HN_TIMEOUT` | `10s` | Go duration | Timeout for Hacker News API requests |
| `API_KEYS` | _(empty)_ | Comma-separated string(s) | When set, enables API key auth. Requests to protected endpoints must send `Authorization: Bearer <key>`. Multiple keys are accepted for key rotation. |
| `PROTECT_HEALTH` | `false` | `true/false`, `1/0`, `yes/no`, `on/off` | When `true` and auth is enabled, `GET /health` also requires a valid API key. Default keeps `/health` public for probes. |

> **Security:** when authentication is enabled, run behind HTTPS so the bearer key is not exposed in transit. Keys are compared in constant time. If `API_KEYS` is unset, auth is disabled and a startup warning is logged.

Values fixed in code (not configurable via env vars):

| Setting | Value | Notes |
| --- | --- | --- |
| Top stories | `20` | Number of HN items per snapshot |
| Max concurrency | `6` | Bounded concurrent item processing |
| Max body size | `5MB` | Per-page fetch limit; larger bodies are rejected |

## API

See [`openapi.yaml`](./openapi.yaml) for the OpenAPI 3.1 specification.

### `GET /top`

Returns the cached snapshot. This endpoint does not trigger scraping.

- `200 OK` (`application/json`): snapshot with `generated_at`, `next_refresh_at`, `refresh_interval`, `count`, `items`, and optional `last_refresh_error`
- `401 Unauthorized` (`application/json`): missing or invalid `Authorization: Bearer <key>` when `API_KEYS` is configured (`WWW-Authenticate: Bearer`)
- `503 Service Unavailable` (`application/json`): warm-up state before the first ready snapshot

Example (with auth enabled):

```bash
curl -H "Authorization: Bearer <key>" http://localhost:8982/top
```

### `GET /health`

Simple health check.

- `200 OK` (`text/plain`): `ok`

Public by default (suitable for liveness/readiness probes). Set `PROTECT_HEALTH=true` to require the same bearer API key when auth is enabled.

## Embedded Examples

### `GET /top` — `200 OK` (cached snapshot)

```json
{
  "generated_at": "2026-11-15T18:30:00Z",
  "next_refresh_at": "2026-11-15T19:00:00Z",
  "refresh_interval": "30m0s",
  "count": 2,
  "items": [
    {
      "id": 12345678,
      "rank": 1,
      "type": "story",
      "title": "Example article title",
      "url": "https://example.com/article",
      "hn_url": "https://news.ycombinator.com/item?id=12345678",
      "by": "alice",
      "score": 321,
      "descendants": 98,
      "time": 1763229000,
      "published_at": "2026-11-15T17:50:00Z",
      "domain": "example.com",
      "markdown": "# Example article title\n\n- **Fuente:** https://example.com/article\n- **HN:** https://news.ycombinator.com/item?id=12345678\n- **Autor:** alice | **Puntos:** 321 | **Comentarios:** 98\n- **Publicado:** 2026-11-15T17:50:00Z | **Tipo:** story | **Dominio:** example.com\n\n---\n\nExtracted article body converted to Markdown...\n",
      "status": "ok",
      "error": null,
      "scraped_at": "2026-11-15T18:29:58Z"
    },
    {
      "id": 12345679,
      "rank": 2,
      "type": "story",
      "title": "Site that failed to scrape",
      "url": "https://blocked.example.com/post",
      "hn_url": "https://news.ycombinator.com/item?id=12345679",
      "by": "bob",
      "score": 120,
      "descendants": 45,
      "time": 1763225400,
      "published_at": "2026-11-15T16:50:00Z",
      "domain": "blocked.example.com",
      "markdown": "",
      "status": "error",
      "error": "fetch: status 403",
      "scraped_at": "2026-11-15T18:29:59Z"
    }
  ]
}
```

Notes:
- `items` may include a mix of `status: "ok"` and `status: "error"` entries in the same snapshot.
- For item-level failures, `markdown` is an empty string and `error` contains the reason (e.g. fetch/extract/convert failure, deleted/dead item).
- `last_refresh_error` is included only when the most recent full refresh failed but a previous valid snapshot is still being served.

### `GET /top` — `401 Unauthorized` (when auth is enabled)

```json
{
  "error": "unauthorized",
  "message": "missing or invalid API key"
}
```

### `GET /top` — `503 Service Unavailable` (warm-up)

```json
{
  "status": "warming_up",
  "message": "initial scrape in progress, try again shortly"
}
```

### `GET /health` — `200 OK`

```text
ok
```

## Data Model Notes

**Snapshot (`/top` 200)**

| Field | Type | Notes |
| --- | --- | --- |
| `generated_at` | string (RFC3339) | Time the snapshot was produced |
| `next_refresh_at` | string (RFC3339) | Scheduled next refresh time |
| `refresh_interval` | string | Go duration string (e.g. `30m0s`) |
| `count` | integer | Number of items in `items` |
| `items` | array[NewsItem] | Cached story items |
| `last_refresh_error` | string (optional) | Present when a refresh failed but last valid snapshot was retained |

**NewsItem**

| Field | Type | Notes |
| --- | --- | --- |
| `id` | integer | Hacker News item ID |
| `rank` | integer | Rank within the fetched top list (1-based) |
| `type` | string | HN item type (e.g. `story`) |
| `title` | string | Item title |
| `url` | string \| null | External URL, or `null` for text-only posts (e.g. Ask HN) |
| `hn_url` | string | Canonical Hacker News discussion URL |
| `by` | string | Author |
| `score` | integer | Score/points |
| `descendants` | integer | Comment count |
| `time` | integer | Unix timestamp |
| `published_at` | string | RFC3339 timestamp (derived from `time`) |
| `domain` | string | Hostname of external URL, empty string when not applicable |
| `markdown` | string | Rendered Markdown; empty string on item error |
| `status` | string | `ok` or `error` |
| `error` | string \| null | Error message for failed item processing |
| `scraped_at` | string \| null | RFC3339 timestamp when the item was processed |

## Behavior, Limitations, and Operations

- **No persistence:** snapshots are stored in memory only and are lost on restart.
- **Warm-up:** until the first successful ready snapshot exists, `GET /top` returns `503`.
- **Partial failures:** individual items may fail (fetch/extract/convert/deleted/dead); other items can still succeed in the same snapshot.
- **Full refresh failures:** if fetching the top story list fails, the service keeps the last valid snapshot (when available) and records `last_refresh_error`.
- **Third-party dependencies:** relies on the Hacker News Firebase API and external websites; availability, rate limiting, blocking, and content quality can affect results.
- **Fetch constraints:** per-request timeouts, max response size (5MB), and content-type checks (HTML/XHTML expected for scraped pages).
- **Extraction quality:** readability/Markdown conversion quality varies by source page.
- **Authentication:** optional API key auth via `Authorization: Bearer <key>` when `API_KEYS` is set. `GET /health` stays public by default (`PROTECT_HEALTH=true` to protect it). Use HTTPS in production to protect the key in transit. If `API_KEYS` is unset, endpoints remain unauthenticated (startup warning logged).
- **Shutdown:** server performs graceful HTTP shutdown on interrupt/termination signals.

## Development

Repository layout (high level):

```text
cmd/server/          # application entrypoint
internal/api/        # HTTP handlers
internal/hn/         # Hacker News API client
internal/markdown/   # HTML-to-Markdown and output rendering
internal/scraper/    # page fetch and content extraction
internal/service/    # refresh orchestration and item processing
internal/store/      # snapshot/item models and in-memory store
```

Key dependencies (see `go.mod` / `go.sum`): `github.com/go-shiori/go-readability`, `github.com/JohannesKaufmann/html-to-markdown/v2`, `golang.org/x/sync`.

Suggested verification commands (no automated test suite is currently included):

```bash
go fmt ./...
go vet ./...
go build ./...
```

## License

MIT. See [`LICENSE`](./LICENSE).
