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
