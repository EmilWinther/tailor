# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run all tests
go test ./...

# Run tests for a single package
go test ./internal/parse/...

# Run a single test by name
go test ./internal/api/... -run TestIngest_NDJSONBody

# Build the binary
go build -o bin/api ./cmd/api

# Lint (also run in CI) and format
golangci-lint run ./...
gofmt -w -s $(go list -f '{{.Dir}}' ./...)

# Local OpenSearch for end-to-end testing (http://localhost:9200, no auth)
docker compose up -d --wait
```

The module is `github.com/emilwinther/tailor` (Go 1.24). Single external dependency: `opensearch-project/opensearch-go/v4`.

## Architecture

An HTTP API that ingests data files (NDJSON, JSON array/object, CSV) into OpenSearch via batched `_bulk` requests.

```
POST /ingest/{index} ──► parse.Iterator ──► osclient.BulkIndex ──► OpenSearch _bulk
```

**`internal/parse/`** — Format detection and streaming parsers. `Detect(explicit, filename, contentType)` resolves the format (query param → extension → Content-Type → NDJSON default). `New(r, format)` returns an `Iterator` whose `Next()` yields one `map[string]any` per document and `io.EOF` at the end. Malformed input surfaces as `*parse.Error` carrying a line/record number — the API layer relies on this type (via `errors.As`) to distinguish client errors (400) from OpenSearch failures (502). CSV values stay strings; OpenSearch coerces types.

**`internal/osclient/`** — `Client` wraps `opensearchapi.Client`. `BulkIndex` drains a `DocIterator` (satisfied by `parse.Iterator`) into `_bulk` bodies, flushing every 500 docs or 5 MB. It returns a `BulkResult{Indexed, Failed, Errors}` that is meaningful even when the returned error is non-nil: buffered docs are flushed before an iterator error propagates, so partial progress is reported. Per-item rejections (e.g. mapping conflicts) are counted as `Failed` with up to 5 reasons captured — they are not a Go error.

**`internal/api/`** — `NewServer(Ingester)` builds the mux using Go 1.22+ path patterns (`POST /ingest/{index}`). The `Ingester` interface decouples handlers from `osclient` so tests use a fake. `requestFile` accepts either a multipart `file` field or the raw request body. Status mapping: all indexed → 200, partial bulk rejections → 207, `*parse.Error`/bad request → 400, transport error → 502.

**`cmd/api/`** — Flag/env config (`LISTEN_ADDR`, `OPENSEARCH_URL`, optional `OPENSEARCH_USERNAME`/`PASSWORD`; flags override env), graceful shutdown on SIGINT/SIGTERM.

## Testing conventions

No test needs a real OpenSearch. `osclient` tests spin up an `httptest.Server` that parses `_bulk` bodies (see `fakeOpenSearch` in `client_test.go`); `api` tests inject a `fakeIngester`. For end-to-end verification use the compose file and the curl examples in the README.
