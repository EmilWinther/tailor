# tailor

Merge, filter, and colorize log streams from multiple sources in one terminal view.

Think of it as `tail -f` that can follow more than one thing at once: a few files, some docker containers, a systemd unit across a fleet of boxes, whatever. Lines get merged in timestamp order so you read them like a single stream, and you can filter or highlight with regex along the way. It's a single static binary with no runtime deps.

## Quickstart

```bash
# Merge two files
tailor app.log nginx/access.log

# Mix files and docker containers
tailor app.log docker://api docker://worker

# Only errors and warnings, with user IDs highlighted
tailor app.log docker://api --filter "ERROR|WARN" --highlight "user_id=\d+"

# Hide noise
tailor docker://api --exclude "healthcheck"

# Include the last 10 minutes of container history
tailor docker://api --since 10m

# Follow a file on a remote host
tailor ssh://web-01/var/log/app.log

# One service across the whole fleet, in timestamp order
tailor journald://deploy@web-01,web-02,web-03/api.service --filter ERROR
```

## Install

```bash
go install github.com/yourname/tailor/cmd/tailor@latest
```

Or from source:

```bash
git clone https://github.com/yourname/tailor && cd tailor
go build -o tailor ./cmd/tailor
```

## Flags

| Flag | What it does |
|---|---|
| `--filter REGEX` | only show matching lines |
| `--exclude REGEX` | hide matching lines |
| `--highlight REGEX` | highlight matches in the output |
| `--since DUR` | pull in history, e.g. `10m` (docker and journald sources) |
| `--from-start` | read files from the top instead of the end |
| `--ssh-opt FLAG` | extra flag to pass to `ssh`, repeatable, e.g. `--ssh-opt -p --ssh-opt 2222` |
| `--window DUR` | reordering window for merging across sources (default `200ms`) |
| `--timestamps=false` | hide timestamps |
| `--no-color` | turn off color (also respects `NO_COLOR`) |

Flags can go before or after the sources, whatever's easier to type.

## How it works

```
 FileSource   ──┐
 DockerSource ──┼──► Merger ──► Pipeline ──► Renderer
 SSHSource    ──┘    (k-way     (filter/     (ANSI,
                      timestamp  exclude/     color per
                      merge)     highlight)   source)
```

Each source runs in its own goroutine and pushes `LogLine`s into a bounded channel, which gives you backpressure for free. File tailing is polling-based, so it keeps up through truncation, rotation, and the delete-then-recreate dance. Docker sources just shell out to `docker logs -f --timestamps`, so they inherit whatever docker context and auth you already have.

## Remote sources

Two source syntaxes reach other machines, both by shelling out to your system `ssh`:

```bash
tailor ssh://HOST/ABSOLUTE/PATH        # tail -F a file over there
tailor journald://HOST/UNIT            # journalctl -f -u UNIT over there
```

`HOST` is anything ssh takes — `web-01`, `deploy@web-01`, or an alias from your `~/.ssh/config` — so keys, jump hosts, and ports configured there work without telling tailor about them. Nothing is installed on the remote side; it needs `tail` or `journalctl` and nothing else.

Since one service usually lives on more than one box, the host part accepts a comma-separated list, and hosts without their own user inherit the first one's:

```bash
tailor journald://deploy@web-01,web-02,web-03/api.service
```

That expands to one source per host, each labelled and coloured separately, all merged into a single timestamp-ordered stream. Mixing transports in one invocation is fine:

```bash
tailor ./local.log docker://api ssh://web-01/var/log/app.log journald://db-01/postgresql
```

Connections use `BatchMode=yes`, so a host that would prompt for a password fails instead of hanging, and keepalives turn a dead link into a clean exit. A failure on the *first* connection is reported and tailor gives up on that source — that's your typo or your missing key. After that, drops are treated as normal and reconnect with backoff, resuming at the end of the file, so a rebooting box rejoins the stream on its own.

## How the merging works

The merger keeps lines in a min-heap for a short window (`--window`) before emitting them. That's what lets lines that show up slightly out of order across sources still come out sorted by timestamp. After that, the pipeline runs the compiled regexes — filter, then exclude, then highlight — and the renderer gives each source a stable color and lines up the labels.

Timestamps come from the start of each line (RFC3339, journalctl's `short-iso`, `YYYY-MM-DD HH:MM:SS`, and syslog formats). If a line doesn't have one, it falls back to arrival time.

## Roadmap

Stuff I'd like to add, roughly in order of how much I want it:

- [x] `ssh://HOST/PATH` and `journald://HOST/UNIT` sources
- [ ] `journald://UNIT` for the local machine (today journald means remote)
- [ ] `k8s://namespace/pod` source (or pod label selectors)
- [ ] An interactive TUI — scrollback, pause, editing the filter live — probably with Bubble Tea
- [ ] JSON log auto-detection and field extraction
- [ ] `--json` structured output mode

If you want to add a source, `internal/source/source.go` is the place to look. The `Source` interface is small, so a new one is a pretty approachable first PR.

## License

MIT
