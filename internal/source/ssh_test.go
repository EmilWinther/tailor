package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSSHSource_Name(t *testing.T) {
	file := &SSHSource{Host: "web-01", Path: "/var/log/app.log"}
	if got, want := file.Name(), "ssh://web-01/var/log/app.log"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	unit := &SSHSource{Host: "deploy@web-01", Unit: "nginx"}
	if got, want := unit.Name(), "journald://deploy@web-01/nginx"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSSHSource_RemoteCommand_File(t *testing.T) {
	s := &SSHSource{Host: "web-01", Path: "/var/log/app.log"}
	if got, want := s.remoteCommand(false), "tail -n 0 -F -- '/var/log/app.log'"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := s.remoteCommand(true), "tail -n +1 -F -- '/var/log/app.log'"; got != want {
		t.Errorf("fromStart: got %q, want %q", got, want)
	}
}

func TestSSHSource_RemoteCommand_Unit(t *testing.T) {
	s := &SSHSource{Host: "web-01", Unit: "nginx.service"}
	got := s.remoteCommand(false)
	want := "journalctl --no-pager -o short-iso -f -u 'nginx.service' -n 0"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	s.Since = "10m"
	got = s.remoteCommand(false)
	want = "journalctl --no-pager -o short-iso -f -u 'nginx.service' --since '-10m'"
	if got != want {
		t.Errorf("since: got %q, want %q", got, want)
	}

	// An absolute timestamp is passed through as written.
	s.Since = "2026-06-09 14:00:00"
	if got := s.remoteCommand(false); !strings.Contains(got, "--since '2026-06-09 14:00:00'") {
		t.Errorf("absolute since: got %q", got)
	}
}

func TestSSHSource_RemoteCommand_QuotesPath(t *testing.T) {
	s := &SSHSource{Host: "web-01", Path: "/var/log/a'b; rm -rf /.log"}
	got := s.remoteCommand(false)
	if strings.Contains(got, "; rm -rf /") && !strings.Contains(got, `'\''`) {
		t.Fatalf("path not quoted: %q", got)
	}
	if want := `tail -n 0 -F -- '/var/log/a'\''b; rm -rf /.log'`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSSHSource_BuildArgs(t *testing.T) {
	s := &SSHSource{Host: "web-01", Path: "/var/log/app.log", Options: []string{"-p", "2222"}}
	args := s.buildArgs(false)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Errorf("expected BatchMode in %q", joined)
	}
	if !strings.Contains(joined, "-p 2222") {
		t.Errorf("expected user options in %q", joined)
	}
	if args[len(args)-2] != "web-01" {
		t.Errorf("host should precede the remote command, got %v", args)
	}
	if args[len(args)-1] != s.remoteCommand(false) {
		t.Errorf("last arg should be the remote command, got %q", args[len(args)-1])
	}
}

func TestSSHSource_Run_RejectsBadConfig(t *testing.T) {
	cases := map[string]*SSHSource{
		"no host":   {Path: "/var/log/app.log"},
		"no target": {Host: "web-01"},
		"both":      {Host: "web-01", Path: "/var/log/app.log", Unit: "nginx"},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.Run(context.Background(), make(chan LogLine, 1)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// fakeSSH writes a script that stands in for the ssh binary and returns its
// path. The script's arguments are ignored; body decides what it prints.
func fakeSSH(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSSHSource_Run_EmitsLines(t *testing.T) {
	s := &SSHSource{
		Host: "web-01",
		Path: "/var/log/app.log",
		bin:  fakeSSH(t, "echo '2026-06-09T14:03:02+0200 hello from web-01'\nsleep 30"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan LogLine, 4)
	go func() { _ = s.Run(ctx, out) }()

	select {
	case line := <-out:
		if line.Source != "ssh://web-01/var/log/app.log" {
			t.Errorf("source: got %q", line.Source)
		}
		if line.Text != "2026-06-09T14:03:02+0200 hello from web-01" {
			t.Errorf("text: got %q", line.Text)
		}
		want := time.Date(2026, 6, 9, 14, 3, 2, 0, time.FixedZone("", 2*3600))
		if !line.Time.Equal(want) {
			t.Errorf("time: got %v, want %v", line.Time, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a line")
	}
}

func TestSSHSource_Run_FailsFastOnFirstConnection(t *testing.T) {
	s := &SSHSource{
		Host:  "web-01",
		Path:  "/var/log/app.log",
		Retry: time.Millisecond,
		bin:   fakeSSH(t, "echo 'Permission denied (publickey).' >&2\nexit 255"),
	}

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), make(chan LogLine, 1)) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "Permission denied") {
			t.Errorf("expected ssh stderr in the error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run retried instead of failing fast")
	}
}

func TestSSHSource_Run_Reconnects(t *testing.T) {
	// The connection drops after every line; Run should reconnect and keep
	// streaming rather than returning.
	s := &SSHSource{
		Host:  "web-01",
		Unit:  "nginx",
		Retry: time.Millisecond,
		bin:   fakeSSH(t, "echo 'still here'"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan LogLine)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, out) }()

	for i := 0; i < 3; i++ {
		select {
		case line := <-out:
			if line.Text != "still here" {
				t.Fatalf("line %d: got %q", i, line.Text)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for line %d", i)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected ctx error after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestParseSSHSpec_File(t *testing.T) {
	got, err := ParseSSHSpec("ssh://web-01/var/log/app.log")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sources, want 1", len(got))
	}
	if got[0].Host != "web-01" || got[0].Path != "/var/log/app.log" || got[0].Unit != "" {
		t.Errorf("got %+v", got[0])
	}
}

func TestParseSSHSpec_MultipleHostsInheritUser(t *testing.T) {
	got, err := ParseSSHSpec("ssh://deploy@web-01,web-02,root@db-01/var/log/app.log")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deploy@web-01", "deploy@web-02", "root@db-01"}
	if len(got) != len(want) {
		t.Fatalf("got %d sources, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Host != w {
			t.Errorf("host %d: got %q, want %q", i, got[i].Host, w)
		}
		if got[i].Path != "/var/log/app.log" {
			t.Errorf("path %d: got %q", i, got[i].Path)
		}
	}
}

func TestParseSSHSpec_Journald(t *testing.T) {
	got, err := ParseSSHSpec("journald://web-01,web-02/nginx.service")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sources, want 2", len(got))
	}
	for i, s := range got {
		if s.Unit != "nginx.service" || s.Path != "" {
			t.Errorf("source %d: got %+v", i, s)
		}
	}
	if got[1].Name() != "journald://web-02/nginx.service" {
		t.Errorf("name: got %q", got[1].Name())
	}
}

func TestParseSSHSpec_Errors(t *testing.T) {
	for _, spec := range []string{
		"app.log",
		"docker://api",
		"ssh://web-01",
		"ssh://web-01/",
		"ssh:///var/log/app.log",
		"journald://nginx",
		"ssh://web-01,,web-02/var/log/app.log",
	} {
		if _, err := ParseSSHSpec(spec); err == nil {
			t.Errorf("%q: expected an error", spec)
		}
	}
}

func TestLimitedBuffer_Caps(t *testing.T) {
	b := &limitedBuffer{max: 8}
	n, err := b.Write([]byte("0123456789"))
	if err != nil || n != 10 {
		t.Fatalf("Write returned (%d, %v), want (10, nil)", n, err)
	}
	if got := b.suffix(); got != ": 01234567" {
		t.Errorf("got %q", got)
	}
}

func TestParseTimestamp_ShortISO(t *testing.T) {
	want := time.Date(2026, 6, 9, 14, 3, 2, 0, time.FixedZone("", 2*3600))
	got := ParseTimestamp("2026-06-09T14:03:02+0200 web-01 nginx[1]: hi", time.Time{})
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestSSHSource_Run_ThroughFakeTransport runs the generated remote command
// for real. The fake ssh runs its last argument through sh instead of over
// the network, which checks that what we send is a valid, correctly quoted
// shell command and that lines stream out of it as the file grows.
func TestSSHSource_Run_ThroughFakeTransport(t *testing.T) {
	if _, err := exec.LookPath("tail"); err != nil {
		t.Skip("tail not available")
	}

	logPath := filepath.Join(t.TempDir(), "app's.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	s := &SSHSource{
		Host: "web-01",
		Path: logPath,
		// Discard the ssh flags, run the last argument locally.
		bin: fakeSSH(t, `for a in "$@"; do cmd="$a"; done; exec sh -c "$cmd"`),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan LogLine, 4)
	go func() { _ = s.Run(ctx, out) }()

	// tail -n 0 starts at the end, so keep writing until a line lands.
	deadline := time.After(10 * time.Second)
	for {
		if _, err := f.WriteString("2026-06-09 14:03:02 hello\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case line := <-out:
			if line.Text != "2026-06-09 14:03:02 hello" {
				t.Errorf("text: got %q", line.Text)
			}
			if line.Source != "ssh://web-01"+logPath {
				t.Errorf("source: got %q", line.Source)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for a tailed line")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
