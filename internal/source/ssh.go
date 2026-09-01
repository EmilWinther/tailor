package source

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SSHSource streams logs from a remote host by running a follower command
// there over ssh: `tail -F` for a file, `journalctl -f` for a systemd unit,
// or `docker logs -f` for a container. Like DockerSource it shells out to
// the system binary rather than speaking the protocol itself, which keeps
// tailor dependency-free and inherits whatever is already configured in
// ~/.ssh/config, the agent, and known_hosts.
//
// Exactly one of Path, Unit, or Container must be set.
type SSHSource struct {
	// Host is anything ssh accepts as a destination: "web-01",
	// "deploy@web-01", or an ssh_config alias.
	Host string
	// Path is an absolute path to a remote file to tail.
	Path string
	// Unit is a systemd unit name to follow with journalctl.
	Unit string
	// Container is a docker container to follow on the remote daemon.
	// Unlike DOCKER_HOST, which redirects every docker source in the
	// process at once, this is per-source: one invocation can follow the
	// same container on several hosts.
	Container string
	// Since limits history for journald and docker sources, e.g. "10m".
	// A bare duration is interpreted as "ago". Ignored for file sources.
	Since string
	// FromStart reads a remote file or container from the beginning
	// instead of the end. It applies to the initial connection only, not
	// to reconnects.
	FromStart bool
	// Options are extra flags passed to ssh, e.g. []string{"-p", "2222"}.
	Options []string
	// Retry is the initial delay before reconnecting after the connection
	// drops. It doubles up to maxRetry. Defaults to 2s.
	Retry time.Duration

	// bin is the ssh binary to execute. Empty means "ssh" from PATH;
	// tests override it.
	bin string
}

const (
	maxRetry       = 30 * time.Second
	stderrCaptured = 4096
)

// Name returns the label used in output, echoing the spec that produced it:
// ssh://host/path, journald://host/unit, or docker://host/container.
func (s *SSHSource) Name() string {
	switch {
	case s.Unit != "":
		return "journald://" + s.Host + "/" + s.Unit
	case s.Container != "":
		return "docker://" + s.Host + "/" + s.Container
	default:
		return "ssh://" + s.Host + s.Path
	}
}

// Run follows the remote log until ctx is cancelled, reconnecting with
// backoff whenever the connection drops. A failure on the very first
// connection is returned instead of retried, so that misconfiguration
// (unknown host, refused key, missing file) surfaces immediately rather
// than looping in the background.
func (s *SSHSource) Run(ctx context.Context, out chan<- LogLine) error {
	switch {
	case s.Host == "":
		return fmt.Errorf("ssh source: no host specified")
	case s.targets() == 0:
		return fmt.Errorf("ssh source %s: need a remote path, unit, or container", s.Host)
	case s.targets() > 1:
		return fmt.Errorf("ssh source %s: path, unit, and container are mutually exclusive", s.Host)
	}

	backoff := s.retryDelay()
	first := true

	for {
		n, err := s.stream(ctx, out, first && s.FromStart)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if first && n == 0 && err != nil {
			return err
		}
		if n > 0 {
			backoff = s.retryDelay() // the connection worked; reset backoff
		}
		first = false

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff = backoff * 2; backoff > maxRetry {
			backoff = maxRetry
		}
	}
}

// stream runs one ssh invocation to completion, returning how many lines it
// emitted. Reconnects always resume at the end of the remote file: whatever
// was written while the link was down is lost, which is the same trade-off
// `tail -F` makes locally.
func (s *SSHSource) stream(ctx context.Context, out chan<- LogLine, fromStart bool) (int, error) {
	cmd := exec.CommandContext(ctx, s.binary(), s.buildArgs(fromStart)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	// ssh's own diagnostics ("Permission denied (publickey)") and the
	// remote command's stderr both land here. They are not log lines, so
	// keep them aside for the error message instead of emitting them.
	stderr := &limitedBuffer{max: stderrCaptured}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("ssh %s: %w (is ssh installed?)", s.Host, err)
	}

	var n int
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		ts, text := s.parseLine(scanner.Text())
		select {
		case out <- LogLine{Source: s.Name(), Time: ts, Text: text}:
			n++
		case <-ctx.Done():
			_ = cmd.Wait()
			return n, ctx.Err()
		}
	}

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return n, fmt.Errorf("%s: %w%s", s.Name(), err, stderr.suffix())
	}
	return n, nil
}

func (s *SSHSource) binary() string {
	if s.bin != "" {
		return s.bin
	}
	return "ssh"
}

// targets reports how many of Path, Unit, and Container are set.
func (s *SSHSource) targets() int {
	n := 0
	for _, t := range []string{s.Path, s.Unit, s.Container} {
		if t != "" {
			n++
		}
	}
	return n
}

// parseLine turns a raw remote line into a timestamp and display text.
// Docker prefixes every line with an RFC3339Nano stamp because we ask for
// one, so that prefix is authoritative and gets stripped; tail and
// journalctl emit whatever the log itself carries, which ParseTimestamp
// reads without removing.
func (s *SSHSource) parseLine(line string) (time.Time, string) {
	if s.Container != "" {
		return splitDockerTimestamp(line)
	}
	return ParseTimestamp(line, time.Now()), line
}

func (s *SSHSource) retryDelay() time.Duration {
	if s.Retry > 0 {
		return s.Retry
	}
	return 2 * time.Second
}

// buildArgs assembles the ssh invocation. BatchMode makes a missing key fail
// fast instead of hanging on a password prompt, and the keepalives let a
// dropped link surface as an exit rather than a silent stall.
func (s *SSHSource) buildArgs(fromStart bool) []string {
	args := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	}
	args = append(args, s.Options...)
	return append(args, s.Host, s.remoteCommand(fromStart))
}

// remoteCommand is the shell command executed on the far end. Each variant
// mirrors the local source it stands in for, so a remote container behaves
// like DockerSource and a remote file like FileSource.
func (s *SSHSource) remoteCommand(fromStart bool) string {
	switch {
	case s.Unit != "":
		cmd := []string{"journalctl", "--no-pager", "-o", "short-iso", "-f", "-u", shellQuote(s.Unit)}
		if since := journalSince(s.Since); since != "" {
			cmd = append(cmd, "--since", shellQuote(since))
		} else {
			cmd = append(cmd, "-n", "0")
		}
		return strings.Join(cmd, " ")

	case s.Container != "":
		cmd := []string{"docker", "logs", "-f", "--timestamps"}
		switch {
		case s.Since != "":
			cmd = append(cmd, "--since", shellQuote(s.Since))
		case fromStart:
			cmd = append(cmd, "--tail", "all")
		default:
			cmd = append(cmd, "--tail", "0")
		}
		return strings.Join(append(cmd, shellQuote(s.Container)), " ")

	default:
		start := "-n 0"
		if fromStart {
			start = "-n +1"
		}
		// -F keeps following across rotation and recreation, like FileSource.
		return "tail " + start + " -F -- " + shellQuote(s.Path)
	}
}

var bareDuration = regexp.MustCompile(`^([0-9]+[smhdw])+$`)

// journalSince converts a bare duration like "10m" into the relative form
// journalctl expects ("-10m"). Anything else is passed through untouched, so
// absolute timestamps still work.
func journalSince(v string) string {
	if bareDuration.MatchString(v) {
		return "-" + v
	}
	return v
}

// shellQuote wraps s for the remote shell. Paths and unit names come from the
// command line, so they must not be able to run anything on the far end.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- source specs ---------------------------------------------------------

// IsRemote reports whether spec names a source on another host, and so
// belongs to ParseSSHSpec rather than to a local source. It exists because
// the docker scheme is the ambiguous one: docker://api is the local daemon,
// docker://web-01/api is the daemon on web-01.
func IsRemote(spec string) bool {
	rest, scheme, ok := splitScheme(spec)
	if !ok {
		return false
	}
	if scheme == "docker" {
		return strings.ContainsRune(rest, '/')
	}
	return true
}

// ParseSSHSpec expands a command-line source argument into one source per
// host. All three schemes accept a comma-separated host list, so a service
// spread over a fleet is one argument:
//
//	ssh://web-01/var/log/app.log
//	journald://deploy@web-01,web-02/api.service
//	docker://web-01,web-02/api
//
// A host without its own user inherits the user of the first host. The
// returned sources have only Host and the target field set; the caller
// applies flag-derived fields (Since, FromStart, Options) before Run.
func ParseSSHSpec(spec string) ([]*SSHSource, error) {
	rest, scheme, ok := splitScheme(spec)
	if !ok {
		return nil, fmt.Errorf("not a remote source: %q", spec)
	}

	usage := map[string]string{
		"ssh":      "want ssh://HOST/PATH",
		"journald": "want journald://HOST/UNIT",
		"docker":   "want docker://HOST/CONTAINER (a local container is just docker://CONTAINER)",
	}[scheme]

	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return nil, fmt.Errorf("%s source %q: %s", scheme, spec, usage)
	}
	hosts, err := expandHosts(rest[:slash])
	if err != nil {
		return nil, fmt.Errorf("%s source %q: %w", scheme, spec, err)
	}
	target := rest[slash:] // keeps the leading slash, which paths need

	// Unit and container names cannot contain a slash, so a second one is
	// a mistyped spec rather than something to hand to the remote shell.
	if scheme != "ssh" && strings.ContainsRune(target[1:], '/') {
		return nil, fmt.Errorf("%s source %q: %s", scheme, spec, usage)
	}

	out := make([]*SSHSource, 0, len(hosts))
	for _, h := range hosts {
		src := &SSHSource{Host: h}
		switch scheme {
		case "journald":
			src.Unit = target[1:]
		case "docker":
			src.Container = target[1:]
		default:
			src.Path = target
		}
		out = append(out, src)
	}
	return out, nil
}

// splitScheme separates a known source scheme from the rest of the spec.
func splitScheme(spec string) (rest, scheme string, ok bool) {
	for _, s := range []string{"ssh", "journald", "docker"} {
		if strings.HasPrefix(spec, s+"://") {
			return strings.TrimPrefix(spec, s+"://"), s, true
		}
	}
	return "", "", false
}

// expandHosts splits a comma-separated host list, propagating the first
// entry's user to entries that do not name one of their own.
func expandHosts(list string) ([]string, error) {
	parts := strings.Split(list, ",")
	var user string
	hosts := make([]string, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("empty host in %q", list)
		}
		if at := strings.LastIndexByte(p, '@'); at >= 0 {
			if i == 0 {
				user = p[:at+1]
			}
		} else if i > 0 {
			p = user + p
		}
		hosts = append(hosts, p)
	}
	return hosts, nil
}

// --- helpers --------------------------------------------------------------

// limitedBuffer keeps the first max bytes written to it and discards the
// rest, so a chatty remote command cannot grow it without bound.
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.max - len(l.buf); room > 0 {
		if len(p) > room {
			l.buf = append(l.buf, p[:room]...)
		} else {
			l.buf = append(l.buf, p...)
		}
	}
	return len(p), nil
}

// suffix renders the captured stderr for appending to an error message.
func (l *limitedBuffer) suffix() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := strings.TrimSpace(string(l.buf))
	if s == "" {
		return ""
	}
	return ": " + strings.ReplaceAll(s, "\n", "; ")
}
