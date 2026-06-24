package source

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// FileSource tails a file on disk, surviving truncation and rotation.
// It uses polling rather than inotify to stay dependency-free and to
// work on every platform and filesystem (including NFS and containers).
type FileSource struct {
	Path string
	// Poll is how often we check for new data. Defaults to 250ms.
	Poll time.Duration
	// FromStart reads the whole file instead of seeking to the end.
	FromStart bool
}

func (f *FileSource) Name() string { return f.Path }

func (f *FileSource) Run(ctx context.Context, out chan<- LogLine) error {
	poll := f.Poll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}

	file, err := os.Open(f.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", f.Path, err)
	}
	defer func() { _ = file.Close() }()

	if !f.FromStart {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return fmt.Errorf("seek %s: %w", f.Path, err)
		}
	}

	reader := bufio.NewReader(file)
	var partial []byte // carry incomplete line between polls

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		// Drain everything currently available.
		for {
			chunk, err := reader.ReadBytes('\n')
			if len(chunk) > 0 {
				if chunk[len(chunk)-1] == '\n' {
					line := string(append(partial, chunk[:len(chunk)-1]...))
					partial = partial[:0]
					f.emit(ctx, out, line)
				} else {
					// No newline yet; stash and wait for more.
					partial = append(partial, chunk...)
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return fmt.Errorf("read %s: %w", f.Path, err)
			}
		}

		// Handle rotation/truncation: if the file shrank or was replaced,
		// reopen and start from the beginning of the new file.
		if rotated, nf := f.checkRotation(file); rotated {
			_ = file.Close()
			if nf == nil {
				// File vanished; keep polling for it to reappear.
				nf = f.waitForFile(ctx, poll)
				if nf == nil {
					return ctx.Err()
				}
			}
			file = nf
			reader.Reset(file)
			partial = partial[:0]
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (f *FileSource) emit(ctx context.Context, out chan<- LogLine, line string) {
	now := time.Now()
	select {
	case out <- LogLine{Source: f.Path, Time: ParseTimestamp(line, now), Text: line}:
	case <-ctx.Done():
	}
}

// checkRotation reports whether the file at f.Path is no longer the file we
// hold open (rotated) or has been truncated. If rotated, it returns the
// freshly opened replacement (or nil if the path is currently missing).
func (f *FileSource) checkRotation(current *os.File) (bool, *os.File) {
	curInfo, err := current.Stat()
	if err != nil {
		return true, nil
	}
	diskInfo, err := os.Stat(f.Path)
	if err != nil {
		return true, nil // file removed
	}
	// Truncated in place: reset to start of same file.
	if pos, _ := current.Seek(0, io.SeekCurrent); pos > diskInfo.Size() && os.SameFile(curInfo, diskInfo) {
		_, _ = current.Seek(0, io.SeekStart)
		return false, nil
	}
	if os.SameFile(curInfo, diskInfo) {
		return false, nil
	}
	nf, err := os.Open(f.Path)
	if err != nil {
		return true, nil
	}
	return true, nf
}

func (f *FileSource) waitForFile(ctx context.Context, poll time.Duration) *os.File {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if nf, err := os.Open(f.Path); err == nil {
				return nf
			}
		}
	}
}
