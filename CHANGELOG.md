# Changelog

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
