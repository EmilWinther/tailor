// Package parse turns uploaded data files (NDJSON, JSON, CSV) into a
// stream of documents ready for indexing.
package parse

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
)

// Format identifies a supported input file format.
type Format string

const (
	NDJSON Format = "ndjson" // one JSON object per line
	JSON   Format = "json"   // a JSON array of objects, or a single object
	CSV    Format = "csv"    // header row defines field names
)

// maxLineSize bounds a single NDJSON line (16 MiB).
const maxLineSize = 16 << 20

// Error is a parse failure tied to a position in the input. Handlers use it
// to distinguish bad input (client error) from downstream failures.
type Error struct {
	Line int // 1-based line or record number, 0 if unknown
	Err  error
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %v", e.Line, e.Err)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Detect resolves the input format from, in priority order: an explicit
// format name (e.g. a ?format= query param), the filename extension, and
// the request Content-Type. Empty inputs are skipped; if nothing matches,
// NDJSON is assumed.
func Detect(explicit, filename, contentType string) (Format, error) {
	if explicit != "" {
		switch strings.ToLower(explicit) {
		case "ndjson", "jsonl":
			return NDJSON, nil
		case "json":
			return JSON, nil
		case "csv":
			return CSV, nil
		default:
			return "", fmt.Errorf("unsupported format %q (want ndjson, json, or csv)", explicit)
		}
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".ndjson", ".jsonl":
		return NDJSON, nil
	case ".json":
		return JSON, nil
	case ".csv":
		return CSV, nil
	}
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		switch mt {
		case "application/x-ndjson", "application/jsonlines":
			return NDJSON, nil
		case "application/json":
			return JSON, nil
		case "text/csv":
			return CSV, nil
		}
	}
	return NDJSON, nil
}

// Iterator yields documents one at a time. Next returns io.EOF after the
// last document; any other error means the input is malformed at that point.
type Iterator interface {
	Next() (map[string]any, error)
}

// New returns an Iterator that reads documents from r in the given format.
func New(r io.Reader, f Format) (Iterator, error) {
	switch f {
	case NDJSON:
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), maxLineSize)
		return &ndjsonIterator{sc: sc}, nil
	case JSON:
		return &jsonIterator{dec: json.NewDecoder(r)}, nil
	case CSV:
		cr := csv.NewReader(r)
		cr.ReuseRecord = true
		return &csvIterator{r: cr}, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", f)
	}
}

type ndjsonIterator struct {
	sc   *bufio.Scanner
	line int
}

func (it *ndjsonIterator) Next() (map[string]any, error) {
	for it.sc.Scan() {
		it.line++
		text := strings.TrimSpace(it.sc.Text())
		if text == "" {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			return nil, &Error{Line: it.line, Err: err}
		}
		return doc, nil
	}
	if err := it.sc.Err(); err != nil {
		return nil, &Error{Line: it.line + 1, Err: err}
	}
	return nil, io.EOF
}

// jsonIterator streams documents from a JSON array, or yields a single
// top-level object as one document.
type jsonIterator struct {
	dec     *json.Decoder
	started bool
	single  bool // input is one object rather than an array
	done    bool
}

func (it *jsonIterator) Next() (map[string]any, error) {
	if it.done {
		return nil, io.EOF
	}
	if !it.started {
		it.started = true
		tok, err := it.dec.Token()
		if err == io.EOF {
			it.done = true
			return nil, io.EOF
		}
		if err != nil {
			return nil, &Error{Err: fmt.Errorf("invalid JSON: %w", err)}
		}
		if delim, ok := tok.(json.Delim); !ok || delim != '[' {
			if delim == '{' {
				// Re-read the object; the token consumed its '{' so we
				// cannot hand the decoder off. Decode field by field.
				doc, err := decodeObjectAfterBrace(it.dec)
				if err != nil {
					return nil, &Error{Err: err}
				}
				it.single = true
				it.done = true
				return doc, nil
			}
			return nil, &Error{Err: fmt.Errorf("expected a JSON array or object, got %v", tok)}
		}
	}
	if !it.dec.More() {
		it.done = true
		if _, err := it.dec.Token(); err != nil && err != io.EOF { // consume closing ']'
			return nil, &Error{Err: err}
		}
		return nil, io.EOF
	}
	var doc map[string]any
	if err := it.dec.Decode(&doc); err != nil {
		return nil, &Error{Err: err}
	}
	return doc, nil
}

// decodeObjectAfterBrace finishes decoding an object whose opening '{' was
// already consumed as a token.
func decodeObjectAfterBrace(dec *json.Decoder) (map[string]any, error) {
	doc := make(map[string]any)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected object key, got %v", keyTok)
		}
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		doc[key] = val
	}
	if _, err := dec.Token(); err != nil { // consume closing '}'
		return nil, err
	}
	return doc, nil
}

// csvIterator maps each CSV record to a document using the header row as
// field names. All values stay strings; OpenSearch coerces types on its side.
type csvIterator struct {
	r      *csv.Reader
	header []string
	record int
}

func (it *csvIterator) Next() (map[string]any, error) {
	if it.header == nil {
		hdr, err := it.r.Read()
		if err == io.EOF {
			return nil, io.EOF
		}
		if err != nil {
			return nil, &Error{Line: 1, Err: err}
		}
		it.header = make([]string, len(hdr))
		copy(it.header, hdr)
		it.header[0] = strings.TrimPrefix(it.header[0], "\ufeff") // strip UTF-8 BOM
		it.record = 1
	}
	rec, err := it.r.Read()
	if err == io.EOF {
		return nil, io.EOF
	}
	it.record++
	if err != nil {
		return nil, &Error{Line: it.record, Err: err}
	}
	doc := make(map[string]any, len(it.header))
	for i, name := range it.header {
		doc[name] = rec[i]
	}
	return doc, nil
}
