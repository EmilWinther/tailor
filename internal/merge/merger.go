// Package merge fans in lines from multiple sources and emits them in
// timestamp order within a small reordering window.
package merge

import (
	"container/heap"
	"context"
	"time"

	"github.com/yourname/tailor/internal/source"
)

// Merger buffers incoming lines briefly so that lines arriving slightly
// out of order across sources are still emitted in timestamp order.
// A line is released once it is older than Window.
type Merger struct {
	// Window is how long a line may be held for reordering. Lines are
	// emitted once now - line.Time > Window. Default 200ms.
	Window time.Duration
	// Flush is how often the buffer is drained. Default Window/2.
	Flush time.Duration
}

// Run consumes in and sends ordered lines to out until in is closed and
// drained, or ctx is cancelled. It closes out when finished.
func (m *Merger) Run(ctx context.Context, in <-chan source.LogLine, out chan<- source.LogLine) {
	defer close(out)

	window := m.Window
	if window <= 0 {
		window = 200 * time.Millisecond
	}
	flush := m.Flush
	if flush <= 0 {
		flush = window / 2
	}

	buf := &lineHeap{}
	heap.Init(buf)

	ticker := time.NewTicker(flush)
	defer ticker.Stop()

	emitReady := func(deadline time.Time) {
		for buf.Len() > 0 && (*buf)[0].Time.Before(deadline) {
			line := heap.Pop(buf).(source.LogLine)
			select {
			case out <- line:
			case <-ctx.Done():
				return
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-in:
			if !ok {
				// Input finished: drain everything in order.
				emitReady(time.Now().Add(window))
				return
			}
			heap.Push(buf, line)
		case <-ticker.C:
			emitReady(time.Now().Add(-window))
		}
	}
}

// lineHeap is a min-heap ordered by LogLine.Time.
type lineHeap []source.LogLine

func (h lineHeap) Len() int            { return len(h) }
func (h lineHeap) Less(i, j int) bool  { return h[i].Time.Before(h[j].Time) }
func (h lineHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lineHeap) Push(x interface{}) { *h = append(*h, x.(source.LogLine)) }
func (h *lineHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
