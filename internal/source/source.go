// Package source defines the Source interface and concrete log sources.
package source

import (
	"context"
	"regexp"
	"time"
)

// LogLine is a single line of log output, annotated with its origin
// and best-effort parsed timestamp.
type LogLine struct {
	Source string    // human-readable source label, e.g. "app.log" or "docker://api"
	Time   time.Time // parsed from the line if possible, otherwise arrival time
	Text   string    // the raw line, without trailing newline
}

// Source is any producer of log lines. Implementations must respect
// ctx cancellation and must close nothing: the caller owns the channel.
type Source interface {
	// Name returns the label used to identify this source in output.
	Name() string
	// Run streams lines into out until ctx is cancelled or the source
	// terminates. It must not close out.
	Run(ctx context.Context, out chan<- LogLine) error
}

// --- timestamp extraction -------------------------------------------------

// tsPatterns are tried in order against the start of a line. Each pattern's
// first capture group is parsed with the paired layout.
var tsPatterns = []struct {
	re     *regexp.Regexp
	layout string
}{
	// RFC3339 / RFC3339Nano: 2026-06-09T14:03:02.123456789Z
	{regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))`), time.RFC3339Nano},
	// Common app format: 2026-06-09 14:03:02
	{regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`), "2006-01-02 15:04:05"},
	// Syslog-ish: Jun  9 14:03:02
	{regexp.MustCompile(`^([A-Z][a-z]{2} +\d{1,2} \d{2}:\d{2}:\d{2})`), "Jan 2 15:04:05"},
}

// ParseTimestamp attempts to extract a timestamp from the beginning of line.
// If none is found, it returns fallback. Syslog-style timestamps (which lack
// a year) are assigned the current year.
func ParseTimestamp(line string, fallback time.Time) time.Time {
	for _, p := range tsPatterns {
		m := p.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		t, err := time.Parse(p.layout, m[1])
		if err != nil {
			continue
		}
		if t.Year() == 0 { // syslog format has no year
			now := time.Now()
			t = t.AddDate(now.Year(), 0, 0)
		}
		return t
	}
	return fallback
}
