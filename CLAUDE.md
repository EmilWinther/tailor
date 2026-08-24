# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run all tests
go test ./...

# Run tests for a single package
go test ./internal/pipeline/...

# Run a single test by name
go test ./internal/source/... -run TestParseTimestamp_Syslog

# Build the binary (cmd/tailor/main.go is the entry point, not yet in the repo)
go build -o tailor ./cmd/tailor

# Verbose test output
go test -v ./...
```

No linter or Makefile is configured. The module is `github.com/yourname/tailor` (Go 1.22) with zero external dependencies.

## Architecture

```
Sources ──► Merger ──► Pipeline ──► Renderer
```

Each layer communicates via `chan source.LogLine`. Sources own nothing after sending; the caller owns the channel. The `LogLine` struct (`internal/source/source.go`) is the common currency: `Source` (label), `Time` (parsed or arrival), `Text` (raw line, no trailing newline).

**`internal/source/`** — Three concrete source types plus shared helpers.
- `FileSource`: polling-based tail (no inotify), handles truncation and rotation via `checkRotation` comparing inode and file position. Uses a `bufio.Reader`; after truncation the fd is seeked to 0 and the reader's buffer is implicitly drained, so the next read picks up new content.
- `DockerSource`: shells out to `docker logs -f --timestamps`, then strips the RFC3339Nano prefix Docker adds via `splitTimestamp`.
- `SSHSource`: shells out to `ssh`, running either `tail -F` (when `Path` is set) or `journalctl -f -o short-iso` (when `Unit` is set) on the far end. `Run` loops: the first connection failure is returned so misconfiguration surfaces immediately, later ones reconnect with doubling backoff up to 30s. `remoteCommand` builds the far-side shell command and is the unit under test; `shellQuote` keeps command-line paths from executing anything remotely. `ParseSSHSpec` expands `ssh://h1,h2/path` and `journald://h1,h2/unit` into one source per host, and is the only place that knows the URL syntax (there is still no `cmd/`, so nothing calls it yet).
- `ParseTimestamp`: tries RFC3339/Nano → `YYYY-MM-DD HH:MM:SS` → syslog (`Jun  9 14:03:02`) in order. Syslog has no year, so it injects the current year via `AddDate`.

**`internal/merge/`** — `Merger` buffers lines in a min-heap for a configurable `Window` (default 200ms) before emitting them in timestamp order. A flush ticker drains lines older than `now - window`; when the input channel closes the entire heap is drained immediately. Use a large `Window`/`Flush` in tests to make the ticker irrelevant and exercise only the drain-on-close path.

**`internal/pipeline/`** — Ordered list of `Stage` functions (filter → exclude → highlight). Each stage returns `(LogLine, bool)`; `false` short-circuits the rest. Highlight wraps regex matches in `\x1b[1;33;7m...\x1b[0m` (bold yellow inverse). No-op when `color=false` (no stage is added at all).

**`internal/render/`** — `ANSI` assigns a stable color (6-color palette, cycled by source name) and dynamically widens the source label column as new, longer labels are seen. Respects the `NO_COLOR` env var. Thread-safe via mutex.

## Adding a new source

Implement the `Source` interface in `internal/source/source.go`:
```go
type Source interface {
    Name() string
    Run(ctx context.Context, out chan<- LogLine) error
}
```
`Run` must not close `out` and must return when `ctx` is cancelled. See `file.go` or `docker.go` for reference.

Sources that shell out to a binary are tested by pointing an unexported `bin` field at a shell script written to `t.TempDir()` (see `fakeSSH` in `ssh_test.go`) — no network, no docker daemon. A fake that runs its last argument through `sh` also exercises the generated remote command end to end.
