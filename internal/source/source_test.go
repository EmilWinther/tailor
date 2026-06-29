package source

import (
	"testing"
	"time"
)

func TestParseTimestamp_RFC3339(t *testing.T) {
	want := time.Date(2026, 6, 9, 14, 3, 2, 0, time.UTC)
	got := ParseTimestamp("2026-06-09T14:03:02Z extra text", time.Time{})
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTimestamp_RFC3339Nano(t *testing.T) {
	want := time.Date(2026, 6, 9, 14, 3, 2, 123_456_789, time.UTC)
	got := ParseTimestamp("2026-06-09T14:03:02.123456789Z extra text", time.Time{})
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTimestamp_RFC3339WithOffset(t *testing.T) {
	loc := time.FixedZone("UTC+1", 3600)
	want := time.Date(2026, 6, 9, 14, 3, 2, 0, loc)
	got := ParseTimestamp("2026-06-09T14:03:02+01:00 extra text", time.Time{})
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTimestamp_AppFormat(t *testing.T) {
	want := time.Date(2026, 6, 9, 14, 3, 2, 0, time.UTC)
	got := ParseTimestamp("2026-06-09 14:03:02 some log text", time.Time{})
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTimestamp_Syslog(t *testing.T) {
	got := ParseTimestamp("Jun 9 14:03:02 host sshd[123]: accepted", time.Time{})
	now := time.Now()
	if got.Year() != now.Year() {
		t.Errorf("syslog year: got %d, want %d", got.Year(), now.Year())
	}
	if got.Month() != time.June || got.Day() != 9 {
		t.Errorf("unexpected date: %v", got)
	}
	if got.Hour() != 14 || got.Minute() != 3 || got.Second() != 2 {
		t.Errorf("unexpected time: %v", got)
	}
}

func TestParseTimestamp_NoTimestamp(t *testing.T) {
	fallback := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	got := ParseTimestamp("no timestamp here", fallback)
	if !got.Equal(fallback) {
		t.Errorf("got %v, want fallback %v", got, fallback)
	}
}

func TestParseTimestamp_EmptyLine(t *testing.T) {
	fallback := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	got := ParseTimestamp("", fallback)
	if !got.Equal(fallback) {
		t.Errorf("got %v, want fallback %v", got, fallback)
	}
}

func TestParseTimestamp_RFC3339BeforeAppFormat(t *testing.T) {
	// Ensure RFC3339 is tried first — the T in the timestamp distinguishes it.
	want := time.Date(2026, 6, 9, 14, 3, 2, 0, time.UTC)
	got := ParseTimestamp("2026-06-09T14:03:02Z", time.Time{})
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
