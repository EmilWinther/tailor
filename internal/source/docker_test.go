package source

import (
	"testing"
	"time"
)

func TestDockerSource_Name(t *testing.T) {
	d := &DockerSource{Container: "my-container"}
	if d.Name() != "docker://my-container" {
		t.Errorf("got %q", d.Name())
	}
}

func TestDockerSource_SplitTimestamp_Valid(t *testing.T) {
	d := &DockerSource{}
	ts, text := d.splitTimestamp("2026-06-09T14:03:02.123456789Z some log text")

	want := time.Date(2026, 6, 9, 14, 3, 2, 123_456_789, time.UTC)
	if !ts.Equal(want) {
		t.Errorf("timestamp: got %v, want %v", ts, want)
	}
	if text != "some log text" {
		t.Errorf("text: got %q, want %q", text, "some log text")
	}
}

func TestDockerSource_SplitTimestamp_NoSpace(t *testing.T) {
	d := &DockerSource{}
	before := time.Now()
	ts, text := d.splitTimestamp("notimestamp")
	after := time.Now()

	if ts.Before(before) || ts.After(after) {
		t.Errorf("expected ts ≈ now, got %v", ts)
	}
	if text != "notimestamp" {
		t.Errorf("text: got %q, want %q", text, "notimestamp")
	}
}

func TestDockerSource_SplitTimestamp_InvalidTimestamp(t *testing.T) {
	d := &DockerSource{}
	before := time.Now()
	ts, text := d.splitTimestamp("not-a-ts some text")
	after := time.Now()

	if ts.Before(before) || ts.After(after) {
		t.Errorf("expected ts ≈ now, got %v", ts)
	}
	if text != "not-a-ts some text" {
		t.Errorf("text: got %q", text)
	}
}

func TestDockerSource_SplitTimestamp_EmptyTextAfterTS(t *testing.T) {
	d := &DockerSource{}
	ts, text := d.splitTimestamp("2026-06-09T14:03:02Z ")

	want := time.Date(2026, 6, 9, 14, 3, 2, 0, time.UTC)
	if !ts.Equal(want) {
		t.Errorf("timestamp: got %v, want %v", ts, want)
	}
	if text != "" {
		t.Errorf("text: got %q, want empty", text)
	}
}
