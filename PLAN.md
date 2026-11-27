# Plan — hnmd 1.6.0

Estado: backend 1.6.0 completado y verificado (2026-11-27). El plan 1.5.0 se conserva más abajo como histórico.

## Objetivo

Cerrar los huecos de revalidación y de acceso desde clientes web/Android sobre el backend 1.5.0, sin romper el contrato existente: ETags estables en los índices de tareas, CORS con allowlist y documentación actualizada.

## Decisiones

- `generated_at` de `/v1/todos` y `/v1/todos/tags` fijado a `store.LastModified()`; fallback al arranque del handler cuando el store está vacío.
- ETag débil calculado sobre el cuerpo final; `If-None-Match` → `304` con `ETag`, `Last-Modified` y sin cuerpo.
- `Cache-Control: private, no-cache` en los dos índices JSON; `no-store` en tareas, Markdown, `/v1/status` y errores. `/v1/todos/top` se deja fuera de alcance.
- CORS con `CORS_ORIGINS` (default `web`, `http://localhost:5173`, `https://localhost`, `capacitor://localhost`), reemplazando los defaults cuando la variable está definida. `*` se ignora.
- `OPTIONS` → `204` antes de auth; se permiten `Authorization`, `Content-Type`, `If-Match`, `If-None-Match`; se exponen `ETag`, `Retry-After`, `Location`; `Vary: Origin`.
- Orden de middleware: `GzipMiddleware(CORS(mux))`, con auth aplicada por ruta dentro del mux.

## Fases

### C0 — Baseline

- [x] Verificación base de herramientas y tests al cierre de la iteración (`gofmt`, `go vet`, `go test`).

### C1 — ETag y caché

- [x] `internal/api/todo.go`: timestamp de fallback estable en `TodoHandler`.
- [x] Helpers `indexModified` / `indexGeneratedAt` y writer `writeTodoIndexJSON`.
- [x] `Index` y `Tags` usan `generated_at` estable, `Last-Modified` y revalidación.
- [x] El ETag se calcula después del truncado y varía con paginación y filtros.
- [x] `writeTodoJSON` mantiene `no-store` para tareas y errores.

### C2 — CORS

- [x] `internal/api/cors.go` con allowlist, preflight `204`, headers permitidos/expuestos y `Vary: Origin`.
- [x] `parseCORSOrigins` en `cmd/server/main.go` con defaults, limpieza y deduplicación.

### C3 — Wiring

- [x] Mux envuelto con `api.GzipMiddleware(api.CORS(corsOrigins)(mux))`.
- [x] Rutas auditadas: todas las del contrato siguen registradas con la misma auth y gzip.

### C4 — Tests

- [x] `internal/api/todo_cache_test.go`: ETag estable y 304 en `/v1/todos` y `/v1/todos/tags`, cambio tras escritura, paginación y `no-store` en mutables.
- [x] `internal/api/cors_test.go`: preflight permitido/denegado, sin auth, headers, `Vary` y convivencia con gzip.
- [x] `cmd/server/main_test.go`: `parseCORSOrigins`.
- [x] Tests preexistentes de paginación, idempotencia, `412`, `304` y gzip siguen pasando.

### C5 — Documentación

- [x] `openapi.yaml` 1.6.0: CORS, 304 en índices, `Last-Modified`, `Cache-Control` y descripción de `generated_at`.
- [x] `README.md`: configuración, sección CORS, revalidación de índices y cambios 1.6.0.
- [x] `CHANGELOG.md`: entrada 1.6.0 y notas de migración.
- [x] Este `PLAN.md`.

### C6 — Verificación

- [x] `gofmt -l .`
- [x] `go vet ./...`
- [x] `go test ./...`
- [x] `go test -race ./...`
- [x] `go build ./...`
- [ ] Matriz manual `curl` completa de preflight CORS, 304, gzip, paginación, idempotencia y `412` contra un servidor en ejecución. Ya verificados: preflight CORS y `304` en `/v1/todos`.

## Criterios de aceptación

- Dos lecturas idénticas de `/v1/todos` y `/v1/todos/tags` devuelven el mismo ETag; `If-None-Match` devuelve `304` sin cuerpo.
- El ETag cambia cuando cambian las tareas, el filtro o la página.
- Los índices usan `private, no-cache`; tareas, Markdown, status y errores mantienen `no-store`.
- `OPTIONS` responde `204` sin API key y solo los orígenes permitidos reciben `Access-Control-Allow-Origin`.
- Se exponen `ETag`, `Retry-After` y `Location`, se permiten los cuatro headers requeridos y las respuestas llevan `Vary: Origin`.
- `go test ./...`, `go test -race ./...`, `go vet ./...` y `go build ./...` pasan.

---

# Plan — hnmd 1.5.0

Estado: backend 1.5.0 completado y verificado (2026-11-24).

## Objetivo

Cerrar los huecos de contrato y robustez del backend antes de construir el cliente
Android. El alcance acordado es P0 + P1, con cambios breaking documentados en
`CHANGELOG.md`.

## Decisiones

- Errores JSON unificados en todos los endpoints.
- `/v1/status` dentro de `/v1` y protegido por bearer cuando `API_KEYS` está activo.
- `tz` por petición y `TODO_TIMEZONE` como fallback; default `UTC`.
- Gzip con `Vary: Accept-Encoding`; ETag débil en top y artículos.
- 304 mediante `If-None-Match` para `/v1/top` y `/v1/articles/{itemId}.md`.
- Paginación `limit`/`offset`:
  - `/v1/top`: límite 20, máximo 30.
  - `/v1/todos` y `/v1/todos/tags`: límite 100, máximo 500.
- Creación idempotente con `id` opcional: si existe, `200`; si es nueva, `201`.
- `If-Match` obligatorio en `PATCH` y `DELETE` de tareas; `428` si falta y `412`
  si la revisión no coincide. `If-Match: *` fuerza la escritura.
- `LastModified` de tareas basado en un timestamp de store monótono.

## Fases

### B0 — Baseline

- [x] `go test ./...` en `0b06280`: todo pasa.
- [x] `gofmt -l .`: sin salida.
- [x] `go vet ./...`
- [x] `go test -race ./...`
- [x] `go build ./...`

### B1 — P0

- [x] Unificar errores y corregir `method_not_allowed`.
- [x] Añadir `/v1/status`.
- [x] Añadir `tz`/`TODO_TIMEZONE`.
- [x] Añadir middleware gzip.
- [x] Implementar 304 con ETag débil en top y artículos.

### B2 — P1

- [x] Paginación `limit`/`offset` en top, todos y tags.
- [x] Creación idempotente.
- [x] `If-Match` obligatorio y conflicto de revisión.
- [x] Timestamp de store monótono.

### B3 — Documentación

- [x] `openapi.yaml` 1.5.0.
- [x] `README.md`.
- [x] `CHANGELOG.md`.
- [x] Este `PLAN.md`.

### B4 — Verificación

- [x] `gofmt -l .`
- [x] `go vet ./...`
- [x] `go test ./...`
- [x] `go test -race ./...`
- [x] `go build ./...`
- [ ] Matriz `curl` de status, 304, gzip, paginación, idempotencia, `If-Match` y timezone.

### B5 — Handoff Android

- [x] Congelar contrato 1.5.0 en `openapi.yaml`, `README.md` y `CHANGELOG.md`.
- [x] Documentar flujos Android: 304, `If-Match`, `tz` y paginación en la sección
      `Migration for clients` de `CHANGELOG.md`.

## Criterios de aceptación

- No hay respuestas de error con formatos distintos.
- Un cliente puede revalidar top/artículos con `If-None-Match` y recibir 304.
- Gzip funciona sin romper ETag, `Content-Length` ni `Vary`.
- Las escrituras sin `If-Match` reciben 428; las obsoletas reciben 412 con la
  revisión actual.
- `LastModified` no retrocede al borrar la última tarea.
- `go test ./...`, `go test -race ./...`, `go vet ./...` y `go build ./...` pasan.
