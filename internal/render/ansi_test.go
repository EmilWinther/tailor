package render_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yourname/tailor/internal/render"
	"github.com/yourname/tailor/internal/source"
)

func TestANSI_NoColor_NoTimestamp(t *testing.T) {
	var buf bytes.Buffer
	a := render.NewANSI(&buf, false, false)
	a.Write(source.LogLine{Source: "app.log", Text: "hello world"})
	if got := buf.String(); got != "app.log | hello world\n" {
		t.Errorf("got %q", got)
	}
}

func TestANSI_NoColor_WithTimestamp(t *testing.T) {
	var buf bytes.Buffer
	a := render.NewANSI(&buf, false, true)
	ts := time.Date(2026, 6, 9, 14, 3, 2, 456_000_000, time.UTC)
	a.Write(source.LogLine{Source: "app", Time: ts, Text: "msg"})
	got := buf.String()
	if !strings.HasPrefix(got, "14:03:02.456 ") {
		t.Errorf("expected timestamp prefix, got %q", got)
	}
}

func TestANSI_WithColor(t *testing.T) {
	var buf bytes.Buffer
	a := render.NewANSI(&buf, true, false)
	a.Write(source.LogLine{Source: "svc", Text: "line"})
	got := buf.String()
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI escape codes, got %q", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Errorf("expected reset code, got %q", got)
	}
	if !strings.Contains(got, "svc") || !strings.Contains(got, "line") {
		t.Errorf("expected source and text in output, got %q", got)
	}
}

func TestANSI_LabelAlignment(t *testing.T) {
	var buf bytes.Buffer
	a := render.NewANSI(&buf, false, false)

	a.Write(source.LogLine{Source: "a", Text: "short"})
	a.Write(source.LogLine{Source: "long-name", Text: "mid"})
	a.Write(source.LogLine{Source: "a", Text: "short again"})

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}

	// After "long-name" (9 chars) is seen, "a" should be padded to width 9.
	wantPrefix := fmt.Sprintf("%-*s | ", len("long-name"), "a")
	if !strings.HasPrefix(lines[2], wantPrefix) {
		t.Errorf("line 3 should have padded label %q, got %q", wantPrefix, lines[2])
	}
}

func TestANSI_SameSource_SameColor(t *testing.T) {
	var buf bytes.Buffer
	a := render.NewANSI(&buf, true, false)
	a.Write(source.LogLine{Source: "app", Text: "first"})
	a.Write(source.LogLine{Source: "app", Text: "second"})

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0][:5] != lines[1][:5] {
		t.Errorf("same source should get same color: %q vs %q", lines[0][:5], lines[1][:5])
	}
}

func TestANSI_DifferentSources_DifferentColors(t *testing.T) {
	var buf bytes.Buffer
	a := render.NewANSI(&buf, true, false)
	a.Write(source.LogLine{Source: "source1", Text: "a"})
	a.Write(source.LogLine{Source: "source2", Text: "b"})

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0][:5] == lines[1][:5] {
		t.Errorf("different sources should get different colors, both got %q", lines[0][:5])
	}
}

func TestANSI_NoColorEnvVar(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	a := render.NewANSI(&buf, true, false) // color=true, but NO_COLOR overrides
	a.Write(source.LogLine{Source: "app", Text: "msg"})
	got := buf.String()
	if strings.Contains(got, "\x1b[") {
		t.Errorf("NO_COLOR should disable ANSI codes, got %q", got)
	}
}

func TestANSI_OutputEndsWithNewline(t *testing.T) {
	var buf bytes.Buffer
	a := render.NewANSI(&buf, false, false)
	a.Write(source.LogLine{Source: "svc", Text: "line"})
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("output should end with newline, got %q", buf.String())
	}
}
