// Package pipeline applies filtering and highlighting to log lines.
package pipeline

import (
	"fmt"
	"regexp"

	"github.com/yourname/tailor/internal/source"
)

// Stage transforms a line and reports whether it should continue
// through the pipeline (false = drop the line).
type Stage func(source.LogLine) (source.LogLine, bool)

// Pipeline is an ordered list of stages.
type Pipeline struct {
	stages []Stage
}

// Apply runs the line through all stages. ok is false if any stage
// dropped the line.
func (p *Pipeline) Apply(line source.LogLine) (source.LogLine, bool) {
	var ok bool
	for _, s := range p.stages {
		line, ok = s(line)
		if !ok {
			return line, false
		}
	}
	return line, true
}

// AddFilter keeps only lines matching pattern.
func (p *Pipeline) AddFilter(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid --filter pattern: %w", err)
	}
	p.stages = append(p.stages, func(l source.LogLine) (source.LogLine, bool) {
		return l, re.MatchString(l.Text)
	})
	return nil
}

// AddExclude drops lines matching pattern.
func (p *Pipeline) AddExclude(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid --exclude pattern: %w", err)
	}
	p.stages = append(p.stages, func(l source.LogLine) (source.LogLine, bool) {
		return l, !re.MatchString(l.Text)
	})
	return nil
}

const (
	hlStart = "\x1b[1;33;7m" // bold yellow, inverse video
	hlEnd   = "\x1b[0m"
)

// AddHighlight wraps matches of pattern in ANSI highlight codes.
// When color is false this stage is a no-op.
func (p *Pipeline) AddHighlight(pattern string, color bool) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid --highlight pattern: %w", err)
	}
	if !color {
		return nil
	}
	p.stages = append(p.stages, func(l source.LogLine) (source.LogLine, bool) {
		l.Text = re.ReplaceAllString(l.Text, hlStart+"$0"+hlEnd)
		return l, true
	})
	return nil
}
