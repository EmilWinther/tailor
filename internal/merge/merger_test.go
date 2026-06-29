package merge_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/yourname/tailor/internal/merge"
	"github.com/yourname/tailor/internal/source"
)

// runMerger feeds lines into a Merger with a closed input and collects all output.
// Using Window=time.Hour ensures the flush ticker never fires during the test;
// the drain-on-close path is what gets exercised.
func runMerger(lines []source.LogLine) []source.LogLine {
	m := &merge.Merger{Window: time.Hour, Flush: time.Hour}
	in := make(chan source.LogLine, len(lines)+1)
	out := make(chan source.LogLine, len(lines)+1)
	for _, l := range lines {
		in <- l
	}
	close(in)
	m.Run(context.Background(), in, out)
	var result []source.LogLine
	for l := range out {
		result = append(result, l)
	}
	return result
}

func texts(lines []source.LogLine) []string {
	s := make([]string, len(lines))
	for i, l := range lines {
		s[i] = l.Text
	}
	return s
}

func TestMerger_EmptyInput(t *testing.T) {
	result := runMerger(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d: %v", len(result), result)
	}
}

func TestMerger_SingleLine(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	result := runMerger([]source.LogLine{{Time: base, Text: "only"}})
	if len(result) != 1 || result[0].Text != "only" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestMerger_LinesAlreadyOrdered(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	lines := []source.LogLine{
		{Time: base, Text: "first"},
		{Time: base.Add(time.Second), Text: "second"},
		{Time: base.Add(2 * time.Second), Text: "third"},
	}
	got := texts(runMerger(lines))
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMerger_OutOfOrderLines(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	lines := []source.LogLine{
		{Time: base.Add(2 * time.Second), Text: "third"},
		{Time: base, Text: "first"},
		{Time: base.Add(time.Second), Text: "second"},
	}
	got := texts(runMerger(lines))
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMerger_ReverseOrder(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	lines := []source.LogLine{
		{Time: base.Add(4 * time.Second), Text: "e"},
		{Time: base.Add(3 * time.Second), Text: "d"},
		{Time: base.Add(2 * time.Second), Text: "c"},
		{Time: base.Add(time.Second), Text: "b"},
		{Time: base, Text: "a"},
	}
	got := texts(runMerger(lines))
	want := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMerger_ClosesOutput(t *testing.T) {
	m := &merge.Merger{Window: time.Hour, Flush: time.Hour}
	in := make(chan source.LogLine)
	out := make(chan source.LogLine, 1)
	close(in)
	m.Run(context.Background(), in, out)
	if _, open := <-out; open {
		t.Error("output channel should be closed after Run returns")
	}
}

func TestMerger_ContextCancel(t *testing.T) {
	m := &merge.Merger{Window: time.Hour, Flush: time.Hour}
	in := make(chan source.LogLine) // never sends
	out := make(chan source.LogLine, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx, in, out)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Merger did not stop after context cancellation")
	}
}

func TestMerger_PreservesFields(t *testing.T) {
	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	in := source.LogLine{Source: "svc", Time: base, Text: "hello"}
	result := runMerger([]source.LogLine{in})
	if len(result) != 1 {
		t.Fatalf("expected 1 line, got %d", len(result))
	}
	got := result[0]
	if got.Source != "svc" || got.Text != "hello" || !got.Time.Equal(base) {
		t.Errorf("fields not preserved: %+v", got)
	}
}
