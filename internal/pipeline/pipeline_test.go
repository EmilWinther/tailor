package pipeline_test

import (
	"strings"
	"testing"

	"github.com/yourname/tailor/internal/pipeline"
	"github.com/yourname/tailor/internal/source"
)

func line(text string) source.LogLine {
	return source.LogLine{Source: "test", Text: text}
}

const hlStart = "\x1b[1;33;7m"
const hlEnd = "\x1b[0m"

func TestPipeline_Empty(t *testing.T) {
	var p pipeline.Pipeline
	got, ok := p.Apply(line("hello"))
	if !ok || got.Text != "hello" {
		t.Errorf("empty pipeline should pass all lines unchanged; ok=%v text=%q", ok, got.Text)
	}
}

func TestPipeline_Filter_Match(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddFilter("error"); err != nil {
		t.Fatal(err)
	}
	_, ok := p.Apply(line("error: connection refused"))
	if !ok {
		t.Error("matching line should pass filter")
	}
}

func TestPipeline_Filter_NoMatch(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddFilter("error"); err != nil {
		t.Fatal(err)
	}
	_, ok := p.Apply(line("info: server started"))
	if ok {
		t.Error("non-matching line should be dropped by filter")
	}
}

func TestPipeline_Filter_InvalidPattern(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddFilter("[invalid"); err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestPipeline_Exclude_Match(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddExclude("debug"); err != nil {
		t.Fatal(err)
	}
	_, ok := p.Apply(line("debug: verbose output"))
	if ok {
		t.Error("matching line should be dropped by exclude")
	}
}

func TestPipeline_Exclude_NoMatch(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddExclude("debug"); err != nil {
		t.Fatal(err)
	}
	_, ok := p.Apply(line("error: something failed"))
	if !ok {
		t.Error("non-matching line should pass exclude")
	}
}

func TestPipeline_Exclude_InvalidPattern(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddExclude("[invalid"); err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestPipeline_Highlight_WithColor(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddHighlight("error", true); err != nil {
		t.Fatal(err)
	}
	got, ok := p.Apply(line("an error occurred"))
	if !ok {
		t.Error("highlight should not drop lines")
	}
	want := "an " + hlStart + "error" + hlEnd + " occurred"
	if got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
}

func TestPipeline_Highlight_NoColor(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddHighlight("error", false); err != nil {
		t.Fatal(err)
	}
	got, ok := p.Apply(line("an error occurred"))
	if !ok {
		t.Error("highlight should not drop lines")
	}
	if got.Text != "an error occurred" {
		t.Errorf("text should be unchanged with color=false, got %q", got.Text)
	}
}

func TestPipeline_Highlight_InvalidPattern(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddHighlight("[invalid", true); err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestPipeline_Highlight_MultipleMatches(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddHighlight("err", true); err != nil {
		t.Fatal(err)
	}
	got, _ := p.Apply(line("first err then err again"))
	if strings.Count(got.Text, hlStart) != 2 {
		t.Errorf("expected 2 highlights, got %q", got.Text)
	}
}

func TestPipeline_Highlight_PreservesPassthrough(t *testing.T) {
	var p pipeline.Pipeline
	if err := p.AddHighlight("x", true); err != nil {
		t.Fatal(err)
	}
	_, ok := p.Apply(line("no match here"))
	if !ok {
		t.Error("highlight should never drop lines")
	}
}

func TestPipeline_ChainedStages(t *testing.T) {
	var p pipeline.Pipeline
	_ = p.AddFilter("important")
	_ = p.AddExclude("boring")

	_, ok := p.Apply(line("important message"))
	if !ok {
		t.Error("should pass both stages")
	}

	_, ok = p.Apply(line("regular message"))
	if ok {
		t.Error("should fail filter")
	}

	_, ok = p.Apply(line("important boring thing"))
	if ok {
		t.Error("should fail exclude")
	}
}

func TestPipeline_FilterThenHighlight(t *testing.T) {
	var p pipeline.Pipeline
	_ = p.AddFilter("error")
	_ = p.AddHighlight("error", true)

	got, ok := p.Apply(line("error: disk full"))
	if !ok {
		t.Error("should pass filter")
	}
	if !strings.Contains(got.Text, hlStart) {
		t.Errorf("expected highlight codes, got %q", got.Text)
	}

	_, ok = p.Apply(line("info: all ok"))
	if ok {
		t.Error("should fail filter before reaching highlight")
	}
}

func TestPipeline_SourceFieldUnchanged(t *testing.T) {
	var p pipeline.Pipeline
	_ = p.AddHighlight("foo", true)
	l := source.LogLine{Source: "mysvc", Text: "foo bar"}
	got, _ := p.Apply(l)
	if got.Source != "mysvc" {
		t.Errorf("source field should be unchanged, got %q", got.Source)
	}
}
