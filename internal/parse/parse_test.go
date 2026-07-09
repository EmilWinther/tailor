package parse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// drain collects all documents from an iterator, failing on unexpected errors.
func drain(t *testing.T, it Iterator) []map[string]any {
	t.Helper()
	var docs []map[string]any
	for {
		doc, err := it.Next()
		if err == io.EOF {
			return docs
		}
		if err != nil {
			t.Fatalf("unexpected error after %d docs: %v", len(docs), err)
		}
		docs = append(docs, doc)
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name                   string
		explicit, filename, ct string
		want                   Format
		wantErr                bool
	}{
		{"explicit wins", "csv", "data.json", "application/json", CSV, false},
		{"explicit jsonl alias", "jsonl", "", "", NDJSON, false},
		{"explicit unknown", "xml", "", "", "", true},
		{"extension ndjson", "", "logs.ndjson", "", NDJSON, false},
		{"extension jsonl", "", "logs.JSONL", "", NDJSON, false},
		{"extension json", "", "dump.json", "text/csv", JSON, false},
		{"extension csv", "", "table.csv", "", CSV, false},
		{"content type json", "", "", "application/json; charset=utf-8", JSON, false},
		{"content type ndjson", "", "", "application/x-ndjson", NDJSON, false},
		{"content type csv", "", "", "text/csv", CSV, false},
		{"default ndjson", "", "", "", NDJSON, false},
		{"unknown extension falls through", "", "data.txt", "text/csv", CSV, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Detect(tt.explicit, tt.filename, tt.ct)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Detect() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNDJSON(t *testing.T) {
	input := "{\"a\":1}\n\n  \n{\"b\":\"two\"}\n"
	it, err := New(strings.NewReader(input), NDJSON)
	if err != nil {
		t.Fatal(err)
	}
	docs := drain(t, it)
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
	if docs[0]["a"] != float64(1) || docs[1]["b"] != "two" {
		t.Errorf("unexpected docs: %v", docs)
	}
}

func TestNDJSON_MalformedLineReportsLineNumber(t *testing.T) {
	it, _ := New(strings.NewReader("{\"ok\":true}\nnot json\n"), NDJSON)
	if _, err := it.Next(); err != nil {
		t.Fatalf("first doc: %v", err)
	}
	_, err := it.Next()
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("want *parse.Error, got %v", err)
	}
	if perr.Line != 2 {
		t.Errorf("Line = %d, want 2", perr.Line)
	}
}

func TestJSONArray(t *testing.T) {
	it, _ := New(strings.NewReader(`[{"a":1},{"a":2},{"a":3}]`), JSON)
	docs := drain(t, it)
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3", len(docs))
	}
	if docs[2]["a"] != float64(3) {
		t.Errorf("unexpected last doc: %v", docs[2])
	}
	// After EOF, Next keeps returning EOF.
	if _, err := it.Next(); err != io.EOF {
		t.Errorf("post-EOF Next() = %v, want io.EOF", err)
	}
}

func TestJSONSingleObject(t *testing.T) {
	it, _ := New(strings.NewReader(`{"name":"solo","n":{"nested":true}}`), JSON)
	docs := drain(t, it)
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	if docs[0]["name"] != "solo" {
		t.Errorf("unexpected doc: %v", docs[0])
	}
	nested, ok := docs[0]["n"].(map[string]any)
	if !ok || nested["nested"] != true {
		t.Errorf("nested object not preserved: %v", docs[0]["n"])
	}
}

func TestJSONEmptyArray(t *testing.T) {
	it, _ := New(strings.NewReader(`[]`), JSON)
	if docs := drain(t, it); len(docs) != 0 {
		t.Errorf("got %d docs, want 0", len(docs))
	}
}

func TestJSONInvalidTopLevel(t *testing.T) {
	it, _ := New(strings.NewReader(`"just a string"`), JSON)
	_, err := it.Next()
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("want *parse.Error, got %v", err)
	}
}

func TestCSV(t *testing.T) {
	input := "name,age,city\nalice,30,berlin\nbob,25,oslo\n"
	it, _ := New(strings.NewReader(input), CSV)
	docs := drain(t, it)
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
	if docs[0]["name"] != "alice" || docs[0]["age"] != "30" {
		t.Errorf("unexpected first doc: %v", docs[0])
	}
	if docs[1]["city"] != "oslo" {
		t.Errorf("unexpected second doc: %v", docs[1])
	}
}

func TestCSV_BOMHeader(t *testing.T) {
	it, _ := New(strings.NewReader("\ufeffid,v\n1,x\n"), CSV)
	docs := drain(t, it)
	if len(docs) != 1 || docs[0]["id"] != "1" {
		t.Errorf("BOM not stripped from header: %v", docs)
	}
}

func TestCSV_InconsistentRow(t *testing.T) {
	it, _ := New(strings.NewReader("a,b\n1,2\n3\n"), CSV)
	if _, err := it.Next(); err != nil {
		t.Fatalf("first row: %v", err)
	}
	_, err := it.Next()
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("want *parse.Error for short row, got %v", err)
	}
	if perr.Line != 3 {
		t.Errorf("Line = %d, want 3", perr.Line)
	}
}

func TestCSV_EmptyInput(t *testing.T) {
	it, _ := New(strings.NewReader(""), CSV)
	if _, err := it.Next(); err != io.EOF {
		t.Errorf("Next() = %v, want io.EOF", err)
	}
}
