# hnmd

Backend en Go que obtiene las 20 noticias más destacadas de Hacker News, extrae el contenido de cada noticia, lo convierte a Markdown y lo expone vía HTTP JSON. El scraping se ejecuta periódicamente en background (una sola pasada por ciclo) y las peticiones solo retornan la información cacheada.

## Requisitos

- Go 1.22+

## Ejecutar

```bash
go mod tidy
go run ./cmd/server
```

El servidor escucha por defecto en `:8982`.

## Endpoint

- `GET /top` — retorna el snapshot cacheado (no dispara scraping).
  - `200 OK`: JSON con `generated_at`, `next_refresh_at`, `refresh_interval`, `count`, `items`.
  - `503 Service Unavailable`: mientras corre el warm-up inicial (`{"status":"warming_up", ...}`).
- `GET /health` — healthcheck simple.

Ejemplo:

```bash
curl http://localhost:8982/top
```

Cada item incluye: `id`, `rank`, `type`, `title`, `url`, `hn_url`, `by`, `score`, `descendants`, `time`, `published_at`, `domain`, `markdown`, `status` (`ok`/`error`), `error`, `scraped_at`.

El campo `markdown` contiene metadatos (fuente, enlace HN, autor, puntos, comentarios, fecha, tipo, dominio) y el contenido principal extraído del artículo (o el texto del post para Ask HN). En caso de fallo parcial de scraping, `status` es `error`, `error` describe el motivo y `markdown` es vacío.

## Configuración (variables de entorno)

| Variable | Default | Descripción |
| --- | --- | --- |
| `PORT` | `8982` | Puerto HTTP |
| `REFRESH_INTERVAL` | `30m` | Intervalo de scraping periódico (ej. `10m`, `1h`) |
| `SCRAPE_TIMEOUT` | `10s` | Timeout por sitio al scrapear |
| `HN_TIMEOUT` | `10s` | Timeout para la API de Hacker News |

## Notas

- Si un ciclo de refresh falla completamente (no se puede obtener el ranking), se conserva el último snapshot válido.
- Concurrencia de scraping acotada para evitar saturación.
- Dependencias: `go-readability` (extracción), `html-to-markdown` (conversión).
