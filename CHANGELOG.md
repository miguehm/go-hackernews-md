# Changelog

## Unreleased

### Fixed

- Task responses (`POST`, `GET`, `PATCH /v1/todos/tasks...`) again return `ETag: "<revision>"` as documented. A content hash used to overwrite it, so sending the received `ETag` back in `If-Match` failed with `400`.
- `SCRAPE_TIMEOUT` and `HN_TIMEOUT` with invalid values no longer silently disable HTTP timeouts, and `REFRESH_INTERVAL=0s` no longer panics. All three must be positive durations, otherwise startup fails.
- Shutdown now cancels an in-flight refresh and waits for the refresher to stop.

### Security

- Go 1.27.4 (module and Docker image) fixes GO-2026-6859 in the standard library `net/http`.
- `golang.org/x/net` v0.62.0 and `golang.org/x/text` v0.43.0 fix three vulnerabilities reachable from the HTML pipeline that parses arbitrary pages (GO-2026-6866, GO-2026-6860, GO-2026-5970).
- Every response carries `X-Content-Type-Options: nosniff`, so browsers never reinterpret Markdown or JSON with third-party content as HTML.
- The article scraper refuses to connect to non-public addresses (loopback, private, link-local, CGNAT, cloud metadata and reserved ranges), checked on the resolved IP at dial time. It only follows `http`/`https`, at most 5 redirects, and ignores proxy environment variables.
- `content_error` now holds stable codes such as `fetch_failed`, `fetch_timeout` or `upstream_status_404` instead of raw Go errors, which could expose internal addresses. Details go to the server log.

### Added

- Structured logging with `log/slog` (JSON by default), configured with `LOG_LEVEL` and `LOG_FORMAT`.
- One access-log line per request with `request_id`, `route`, `status`, `bytes` and `duration_ms`.
- `X-Request-ID` on every response: a valid incoming value is reused, otherwise one is generated. It is allowed and exposed through CORS.
- Panic recovery: a handler panic returns `500 internal_error` JSON and is logged with its stack trace.
- `server healthcheck` subcommand and a Docker `HEALTHCHECK` for the distroless image; the image embeds its version through `--build-arg VERSION`.
- Makefile (`make check`), `.golangci.yml`, GitHub Actions CI (gofmt, vet, golangci-lint, race tests, govulncheck, Docker build) and an OpenAPI contract test.
- `HEAD /v1/top`, with the same headers as `GET` and no body.
- `TOP_N` (default `20`) and `MAX_CONCURRENCY` (default `6`) environment variables.
- `/v1/status` reports `last_refresh_error` and `last_refresh_error_at` when the latest refresh failed, including during warm-up.

### Changed

- Configuration is loaded by `internal/config`; all invalid variables are reported at once, and invalid booleans now log a warning instead of being ignored silently.
- Gzip skips bodies under 256 bytes (they would grow) and reuses writers and buffers through a pool.
- `internal/api/todo.go` split into smaller files and duplicated response-trimming loops unified; no behavior change.
- Log output is now JSON on stderr instead of plain text lines. Use `LOG_FORMAT=text` for the human-readable format.
- The warm-up `503` uses a fixed `Retry-After: 15` instead of the full refresh interval.
- The Hacker News client sends a `User-Agent`.

### Migration for clients

- Treat `content_error` as an opaque code; the previous free-form text (`fetch: ...`, `item unavailable`) is gone.

## 1.6.0 — 2026-11-27

### Added

- CORS support with an origin allowlist configured through `CORS_ORIGINS`. Defaults are `web`, `http://localhost:5173`, `https://localhost` and `capacitor://localhost`; setting the variable replaces the defaults and `*` is ignored.
- `OPTIONS` preflight returns `204` before authentication, allowing `Authorization`, `Content-Type`, `If-Match` and `If-None-Match`, exposing `ETag`, `Retry-After` and `Location`, and adding `Vary: Origin`.
- Conditional reads for `GET /v1/todos` and `GET /v1/todos/tags`: send `If-None-Match` with the weak `ETag` to receive `304 Not Modified` with `ETag`, `Last-Modified` and no body.

### Fixed

- The `ETag` of `/v1/todos` and `/v1/todos/tags` no longer changes on every request: `generated_at` is now the store's `LastModified()` timestamp instead of a per-request clock value. The validator still changes when tasks or filters change the payload.

### Changed

- `GET /v1/todos` and `GET /v1/todos/tags` now use `Cache-Control: private, no-cache` instead of `no-store`. Task responses, task Markdown, `/v1/status` and error responses keep `no-store`.
- `openapi.yaml` version is now `1.6.0` and documents the CORS policy, the 304 responses and the updated cache directives.

### Migration for clients

- Revalidate the todo and tag indexes with `If-None-Match`. On `304`, keep the existing local index; a pagination page is identified by its own `ETag`.
- Do not treat `generated_at` in these two indexes as the wall-clock response time. It is the last store modification time and is stable between writes.
- Browser clients may call the API from the default origins above; add others through `CORS_ORIGINS`. Preflight does not require an API key.

## 1.5.0 — 2026-11-24

Breaking and contract changes. Android clients should be updated before enabling this version in production.

### Added

- `GET /v1/status` reports readiness, version, auth state, snapshot metadata, todo count and server time. It is protected by the same bearer key as `/v1` when `API_KEYS` is set.
- Conditional reads for `GET /v1/top` and `GET|HEAD /v1/articles/{itemId}.md`: send `If-None-Match` with the returned weak `ETag` to receive `304 Not Modified`.
- Gzip responses with `Vary: Accept-Encoding` on all endpoints.
- Offset pagination on `GET /v1/top`, `GET /v1/todos` and `GET /v1/todos/tags`:
  - `offset` (default 0), `limit` (default 20 for top, 100 for todos/tags; max 30 and 500 respectively).
  - Responses add `offset`, `limit`, `has_more` and `next_offset`.
- `tz` query parameter on todo endpoints and `TODO_TIMEZONE` server setting (default `UTC`) for due-date filtering, counters and Markdown rendering.
- Optional `id` on `POST /v1/todos/tasks` for idempotent creation.

### Changed

- ETags on `/v1/top` and articles are weak (`W/"..."`) and are now real validators. Gzip preserves the weak validator.
- `GET /v1/top` and article responses use `Cache-Control: private, no-cache` instead of informational-only cache headers.
- `PATCH` and `DELETE /v1/todos/tasks/{id}` now require `If-Match` with the current task revision (strong or weak ETag, a comma-separated list, or `*`).
  - Missing header returns `428 precondition_required`.
  - Stale revision returns `412 revision_conflict` with `current_revision` and the current strong `ETag`.
- `POST /v1/todos/tasks` with an existing `id` returns `200` and the stored task instead of creating a duplicate.
- All errors share one JSON shape: `error`, `message`, and optional `field`, `item_id`, `task_id`, `retry_after_seconds`, `current_revision`.
- Unsupported methods return `405` with `error: "method_not_allowed"` and an `Allow` header.
- Task `Last-Modified` values use a monotonic store timestamp and no longer move backwards when the last task is deleted.

### Migration for clients

- Store the `ETag` returned with each task and send it as `If-Match` on the next `PATCH` or `DELETE`. For a forced overwrite, send `If-Match: *`.
- On `412`, read `current_revision`, refresh the task and retry only if the change is still intended.
- On `304`, keep the existing local top index or article body; do not delete local files.
- When paginating, follow `next_offset` while `has_more` is true. Treat `truncated: true` without `has_more` as a 128 KiB cap trim.
- For due dates, pass the device timezone as `tz` if it differs from the server's `TODO_TIMEZONE`.
- For safe retries of task creation, generate a client-side `id` and send it with `POST`.
