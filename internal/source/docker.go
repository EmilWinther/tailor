package source

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DockerSource streams logs from a container by shelling out to
// `docker logs -f --timestamps`. Using the CLI instead of the Docker SDK
// keeps tailor a zero-dependency binary and inherits the user's existing
// docker context/auth configuration for free.
type DockerSource struct {
	Container string
	// Since limits history, e.g. "10m". Empty means "from now" (tail 0).
	Since string
}

func (d *DockerSource) Name() string { return "docker://" + d.Container }

func (d *DockerSource) Run(ctx context.Context, out chan<- LogLine) error {
	args := []string{"logs", "-f", "--timestamps"}
	if d.Since != "" {
		args = append(args, "--since", d.Since)
	} else {
		args = append(args, "--tail", "0")
	}
	args = append(args, d.Container)

	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // docker writes container stderr here too
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("docker logs %s: %w (is docker installed and the container running?)", d.Container, err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		ts, text := d.splitTimestamp(line)
		select {
		case out <- LogLine{Source: d.Name(), Time: ts, Text: text}:
		case <-ctx.Done():
			break
		}
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("docker logs %s exited: %w", d.Container, err)
	}
	return ctx.Err()
}

// splitTimestamp separates the RFC3339Nano prefix that --timestamps adds.
func (d *DockerSource) splitTimestamp(line string) (time.Time, string) {
	return splitDockerTimestamp(line)
}

// splitDockerTimestamp separates the RFC3339Nano prefix that --timestamps
// adds. It is shared with SSHSource, which runs the same command remotely.
func splitDockerTimestamp(line string) (time.Time, string) {
	now := time.Now()
	sp := strings.IndexByte(line, ' ')
	if sp <= 0 {
		return now, line
	}
	t, err := time.Parse(time.RFC3339Nano, line[:sp])
	if err != nil {
		return now, line
	}
	return t, line[sp+1:]
}
