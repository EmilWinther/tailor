# OpenSearch ingest API

A small Go HTTP API that takes data files — NDJSON, JSON, or CSV — and bulk-indexes their contents into a local OpenSearch instance.

```
data file ──► POST /ingest/{index} ──► parse ──► _bulk batches ──► OpenSearch
```

## Quickstart

```bash
# 1. Start OpenSearch (single node, security disabled, http://localhost:9200)
docker compose up -d --wait

# 2. Run the API
go run ./cmd/api

# 3. Ingest a file
curl -XPOST --data-binary @examples/users.ndjson \
  -H 'Content-Type: application/x-ndjson' \
  localhost:8080/ingest/users
# → {"index":"users","indexed":3,"failed":0}

# 4. See the documents in OpenSearch
curl 'localhost:9200/users/_search?pretty'
```

Optionally add OpenSearch Dashboards at http://localhost:5601:

```bash
docker compose --profile dashboards up -d
```

## API

### `POST /ingest/{index}`

Indexes every document in the uploaded file into `{index}` (created on the fly by OpenSearch if it doesn't exist). The file can be sent two ways:

```bash
# Raw body
curl -XPOST --data-binary @examples/users.json \
  -H 'Content-Type: application/json' \
  localhost:8080/ingest/users

# Multipart form upload (field name: file)
curl -XPOST -F file=@examples/users.csv localhost:8080/ingest/users
```

**Formats** — resolved in this order, first match wins:

1. `?format=` query param: `ndjson`, `json`, or `csv`
2. Filename extension (multipart uploads, or `?filename=` on raw bodies): `.ndjson`/`.jsonl`, `.json`, `.csv`
3. `Content-Type` header: `application/x-ndjson`, `application/json`, `text/csv`
4. Default: NDJSON

| Format | Expectation |
|---|---|
| `ndjson` | One JSON object per line; blank lines skipped |
| `json` | A JSON array of objects, or a single top-level object |
| `csv` | Header row defines field names; values are indexed as strings |

**Responses**

| Status | Meaning |
|---|---|
| `200` | Everything indexed. `{"index":"users","indexed":3,"failed":0}` |
| `207` | OpenSearch rejected some documents; counts and the first few error reasons are in the body |
| `400` | Malformed input (with line number where possible), unknown format, or invalid index name |
| `502` | OpenSearch unreachable |

Documents are streamed to OpenSearch's `_bulk` API in batches of 500 docs / 5 MB, so large files don't buffer fully in memory. If a file fails to parse midway, everything before the bad line is still indexed and reported in the `indexed` count.

### `GET /healthz`

`200 {"status":"ok"}` when OpenSearch answers a ping, `503` otherwise.

## Configuration

| Env var | Flag | Default |
|---|---|---|
| `LISTEN_ADDR` | `-listen` | `:8080` |
| `OPENSEARCH_URL` | `-opensearch-url` | `http://localhost:9200` |
| `OPENSEARCH_USERNAME` | — | (none) |
| `OPENSEARCH_PASSWORD` | — | (none) |

Flags override env vars. Credentials are only needed against a secured cluster; the bundled compose file disables the security plugin for local development.

## Development

```bash
make test    # go test ./...
make build   # builds bin/api
make fmt     # gofmt -w -s
make lint    # golangci-lint run
make up      # docker compose up -d --wait
```

Layout:

```
cmd/api/            entry point (flags, server lifecycle)
internal/parse/     NDJSON / JSON / CSV → document iterators
internal/osclient/  batched _bulk indexing via opensearch-go
internal/api/       HTTP handlers
```
