package osclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sliceIterator yields the given docs, then a terminal error (io.EOF by default).
type sliceIterator struct {
	docs  []map[string]any
	final error
	i     int
}

func (s *sliceIterator) Next() (map[string]any, error) {
	if s.i >= len(s.docs) {
		if s.final != nil {
			return nil, s.final
		}
		return nil, io.EOF
	}
	doc := s.docs[s.i]
	s.i++
	return doc, nil
}

// bulkCall records one _bulk request body received by the fake server.
type bulkCall struct {
	actions []map[string]any
	docs    []map[string]any
}

// fakeOpenSearch returns a test server that answers /_bulk with the
// provided responder and records each parsed bulk body.
func fakeOpenSearch(t *testing.T, respond func(w http.ResponseWriter, call bulkCall)) (*Client, *[]bulkCall) {
	t.Helper()
	var calls []bulkCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_bulk" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{}`)
			return
		}
		var call bulkCall
		sc := bufio.NewScanner(r.Body)
		for lineNo := 0; sc.Scan(); lineNo++ {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("bulk body line %d not JSON: %v", lineNo, err)
				continue
			}
			if lineNo%2 == 0 {
				call.actions = append(call.actions, m)
			} else {
				call.docs = append(call.docs, m)
			}
		}
		calls = append(calls, call)
		respond(w, call)
	}))
	t.Cleanup(srv.Close)
	client, err := New(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

// respondOK acknowledges every document in the batch as created.
func respondOK(w http.ResponseWriter, call bulkCall) {
	items := make([]map[string]any, len(call.docs))
	for i := range items {
		items[i] = map[string]any{"index": map[string]any{"status": 201, "result": "created"}}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"took": 1, "errors": false, "items": items}); err != nil {
		panic(err)
	}
}

func docsN(n int) []map[string]any {
	docs := make([]map[string]any, n)
	for i := range docs {
		docs[i] = map[string]any{"n": i}
	}
	return docs
}

func TestBulkIndex_SingleBatch(t *testing.T) {
	client, calls := fakeOpenSearch(t, respondOK)
	result, err := client.BulkIndex(context.Background(), "logs", &sliceIterator{docs: docsN(3)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Indexed != 3 || result.Failed != 0 {
		t.Errorf("result = %+v, want 3 indexed / 0 failed", result)
	}
	if len(*calls) != 1 {
		t.Fatalf("got %d bulk calls, want 1", len(*calls))
	}
	action := (*calls)[0].actions[0]["index"].(map[string]any)
	if action["_index"] != "logs" {
		t.Errorf("action targets index %v, want logs", action["_index"])
	}
}

func TestBulkIndex_SplitsIntoBatches(t *testing.T) {
	client, calls := fakeOpenSearch(t, respondOK)
	result, err := client.BulkIndex(context.Background(), "logs", &sliceIterator{docs: docsN(maxBatchDocs + 2)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Indexed != maxBatchDocs+2 {
		t.Errorf("Indexed = %d, want %d", result.Indexed, maxBatchDocs+2)
	}
	if len(*calls) != 2 {
		t.Fatalf("got %d bulk calls, want 2", len(*calls))
	}
	if n := len((*calls)[0].docs); n != maxBatchDocs {
		t.Errorf("first batch has %d docs, want %d", n, maxBatchDocs)
	}
	if n := len((*calls)[1].docs); n != 2 {
		t.Errorf("second batch has %d docs, want 2", n)
	}
}

func TestBulkIndex_EmptyInputSendsNothing(t *testing.T) {
	client, calls := fakeOpenSearch(t, respondOK)
	result, err := client.BulkIndex(context.Background(), "logs", &sliceIterator{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Indexed != 0 || len(*calls) != 0 {
		t.Errorf("result = %+v with %d calls, want zero work", result, len(*calls))
	}
}

func TestBulkIndex_PartialItemFailures(t *testing.T) {
	client, _ := fakeOpenSearch(t, func(w http.ResponseWriter, call bulkCall) {
		items := []map[string]any{
			{"index": map[string]any{"status": 201, "result": "created"}},
			{"index": map[string]any{"status": 400, "error": map[string]any{
				"type": "mapper_parsing_exception", "reason": "failed to parse field [n]",
			}}},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"took": 1, "errors": true, "items": items}); err != nil {
			panic(err)
		}
	})
	result, err := client.BulkIndex(context.Background(), "logs", &sliceIterator{docs: docsN(2)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Indexed != 1 || result.Failed != 1 {
		t.Errorf("result = %+v, want 1 indexed / 1 failed", result)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "mapper_parsing_exception") {
		t.Errorf("Errors = %v, want one mapper_parsing_exception reason", result.Errors)
	}
}

func TestBulkIndex_IteratorErrorFlushesBuffered(t *testing.T) {
	client, calls := fakeOpenSearch(t, respondOK)
	iterErr := errors.New("bad line")
	result, err := client.BulkIndex(context.Background(), "logs",
		&sliceIterator{docs: docsN(2), final: iterErr})
	if !errors.Is(err, iterErr) {
		t.Fatalf("err = %v, want the iterator error", err)
	}
	if result.Indexed != 2 {
		t.Errorf("Indexed = %d, want 2 (buffered docs flushed before erroring)", result.Indexed)
	}
	if len(*calls) != 1 {
		t.Errorf("got %d bulk calls, want 1", len(*calls))
	}
}

func TestBulkIndex_ServerUnreachable(t *testing.T) {
	client, err := New(Config{URL: "http://127.0.0.1:1"}) // nothing listens here
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.BulkIndex(context.Background(), "logs", &sliceIterator{docs: docsN(1)})
	if err == nil {
		t.Fatal("want transport error, got nil")
	}
}

func TestPing(t *testing.T) {
	client, _ := fakeOpenSearch(t, respondOK)
	if err := client.Ping(context.Background()); err != nil {
		t.Errorf("Ping() = %v, want nil", err)
	}
	down, err := New(Config{URL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := down.Ping(context.Background()); err == nil {
		t.Error("Ping() against dead server = nil, want error")
	}
}
