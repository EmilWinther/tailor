// Package render writes formatted log lines to a terminal or pipe.
package render

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/yourname/tailor/internal/source"
)

// palette of ANSI foreground colors cycled across sources. Chosen to be
// readable on both dark and light terminals.
var palette = []string{
	"\x1b[36m", // cyan
	"\x1b[32m", // green
	"\x1b[35m", // magenta
	"\x1b[34m", // blue
	"\x1b[33m", // yellow
	"\x1b[31m", // red
}

const reset = "\x1b[0m"

// ANSI renders lines as "[source] text" with a stable color per source.
type ANSI struct {
	Out        io.Writer
	Color      bool
	Timestamps bool

	mu     sync.Mutex
	colors map[string]string
	next   int
	width  int // widest source label seen, for alignment
}

// NewANSI builds a renderer. Color auto-disables when NO_COLOR is set,
// honoring the informal standard at no-color.org.
func NewANSI(out io.Writer, color, timestamps bool) *ANSI {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		color = false
	}
	return &ANSI{Out: out, Color: color, Timestamps: timestamps, colors: map[string]string{}}
}

// Write renders one line.
func (a *ANSI) Write(line source.LogLine) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(line.Source) > a.width {
		a.width = len(line.Source)
	}
	label := fmt.Sprintf("%-*s", a.width, line.Source)

	ts := ""
	if a.Timestamps {
		ts = line.Time.Format("15:04:05.000") + " "
	}

	if a.Color {
		c, ok := a.colors[line.Source]
		if !ok {
			c = palette[a.next%len(palette)]
			a.colors[line.Source] = c
			a.next++
		}
		fmt.Fprintf(a.Out, "%s%s%s%s | %s\n", c, ts, label, reset, line.Text)
		return
	}
	fmt.Fprintf(a.Out, "%s%s | %s\n", ts, label, line.Text)
}
