package source

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestFileSource_Name(t *testing.T) {
	src := &FileSource{Path: "/var/log/app.log"}
	if src.Name() != "/var/log/app.log" {
		t.Errorf("got %q", src.Name())
	}
}

func TestFileSource_OpenError(t *testing.T) {
	src := &FileSource{Path: "/nonexistent/path/file.log", Poll: time.Millisecond}
	out := make(chan LogLine)
	err := src.Run(context.Background(), out)
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestFileSource_FromStart(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tailor-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fmt.Fprintln(f, "line one")
	fmt.Fprintln(f, "line two")
	fmt.Fprintln(f, "line three")
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}

	src := &FileSource{Path: f.Name(), Poll: 10 * time.Millisecond, FromStart: true}
	out := make(chan LogLine, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go src.Run(ctx, out)

	want := []string{"line one", "line two", "line three"}
	timeout := time.After(2 * time.Second)
	for i, w := range want {
		select {
		case line := <-out:
			if line.Text != w {
				t.Errorf("line %d: got %q, want %q", i, line.Text, w)
			}
			if line.Source != f.Name() {
				t.Errorf("source: got %q, want %q", line.Source, f.Name())
			}
		case <-timeout:
			t.Fatalf("timeout waiting for line %d", i)
		}
	}
}

func TestFileSource_AppendedLines(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tailor-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	src := &FileSource{Path: f.Name(), Poll: 10 * time.Millisecond, FromStart: true}
	out := make(chan LogLine, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go src.Run(ctx, out)

	for _, text := range []string{"alpha", "beta", "gamma"} {
		time.Sleep(30 * time.Millisecond)
		fmt.Fprintln(f, text)
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
	}

	timeout := time.After(3 * time.Second)
	for i, w := range []string{"alpha", "beta", "gamma"} {
		select {
		case line := <-out:
			if line.Text != w {
				t.Errorf("line %d: got %q, want %q", i, line.Text, w)
			}
		case <-timeout:
			t.Fatalf("timeout waiting for line %d (%q)", i, w)
		}
	}
}

func TestFileSource_ContextCancel(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tailor-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	src := &FileSource{Path: f.Name(), Poll: 10 * time.Millisecond}
	out := make(chan LogLine, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- src.Run(ctx, out) }()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("FileSource did not stop after context cancellation")
	}
}

func TestFileSource_Truncation(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/app.log"

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "before truncation")
	f.Sync()
	f.Close()

	src := &FileSource{Path: path, Poll: 10 * time.Millisecond, FromStart: true}
	out := make(chan LogLine, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go src.Run(ctx, out)

	timeout := time.After(2 * time.Second)
	select {
	case line := <-out:
		if line.Text != "before truncation" {
			t.Fatalf("unexpected first line: %q", line.Text)
		}
	case <-timeout:
		t.Fatal("timeout waiting for pre-truncation line")
	}

	// Truncate the file in place and write new content (shorter so pos > size).
	time.Sleep(30 * time.Millisecond)
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "after truncation")
	f.Sync()
	f.Close()

	timeout = time.After(2 * time.Second)
	select {
	case line := <-out:
		if line.Text != "after truncation" {
			t.Errorf("unexpected post-truncation line: %q", line.Text)
		}
	case <-timeout:
		t.Fatal("timeout waiting for post-truncation line")
	}
}
