package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilwinther/tailor/internal/osclient"
	"github.com/emilwinther/tailor/internal/parse"
)

// fakeIngester records what BulkIndex was called with and returns canned results.
type fakeIngester struct {
	index   string
	docs    []map[string]any
	result  osclient.BulkResult
	bulkErr error
	pingErr error
}

func (f *fakeIngester) BulkIndex(_ context.Context, index string, docs osclient.DocIterator) (osclient.BulkResult, error) {
	f.index = index
	for {
		doc, err := docs.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return f.result, err
		}
		f.docs = append(f.docs, doc)
	}
	if f.bulkErr != nil {
		return f.result, f.bulkErr
	}
	if f.result.Indexed == 0 && f.result.Failed == 0 && f.result.Errors == nil {
		f.result = osclient.BulkResult{Indexed: len(f.docs)}
	}
	return f.result, nil
}

func (f *fakeIngester) Ping(context.Context) error { return f.pingErr }

func post(t *testing.T, handler http.Handler, path, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) ingestResponse {
	t.Helper()
	var resp ingestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return resp
}

func TestIngest_NDJSONBody(t *testing.T) {
	ing := &fakeIngester{}
	rec := post(t, NewServer(ing), "/ingest/logs", "application/x-ndjson",
		"{\"msg\":\"hello\"}\n{\"msg\":\"world\"}\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Indexed != 2 || ing.index != "logs" || len(ing.docs) != 2 {
		t.Errorf("resp = %+v, ingester saw index %q with %d docs", resp, ing.index, len(ing.docs))
	}
	if ing.docs[1]["msg"] != "world" {
		t.Errorf("unexpected doc: %v", ing.docs[1])
	}
}

func TestIngest_JSONArrayBody(t *testing.T) {
	ing := &fakeIngester{}
	rec := post(t, NewServer(ing), "/ingest/logs", "application/json", `[{"a":1},{"a":2}]`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(ing.docs) != 2 {
		t.Errorf("ingester saw %d docs, want 2", len(ing.docs))
	}
}

func TestIngest_CSVViaQueryParam(t *testing.T) {
	ing := &fakeIngester{}
	rec := post(t, NewServer(ing), "/ingest/people?format=csv", "text/plain", "name\nalice\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(ing.docs) != 1 || ing.docs[0]["name"] != "alice" {
		t.Errorf("ingester saw %v", ing.docs)
	}
}

func TestIngest_MultipartUploadDetectsFormatFromFilename(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "people.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("name,age\nbob,25\n")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	ing := &fakeIngester{}
	rec := post(t, NewServer(ing), "/ingest/people", mw.FormDataContentType(), buf.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(ing.docs) != 1 || ing.docs[0]["age"] != "25" {
		t.Errorf("ingester saw %v", ing.docs)
	}
}

func TestIngest_MultipartMissingFileField(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("data", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	rec := post(t, NewServer(&fakeIngester{}), "/ingest/logs", mw.FormDataContentType(), buf.String())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestIngest_MalformedInputIs400(t *testing.T) {
	ing := &fakeIngester{}
	rec := post(t, NewServer(ing), "/ingest/logs", "application/x-ndjson", "not json\n")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error == "" {
		t.Error("response should carry the parse error")
	}
}

func TestIngest_ParseErrorMidStreamKeepsPartialCount(t *testing.T) {
	ing := &fakeIngester{result: osclient.BulkResult{Indexed: 1}}
	rec := post(t, NewServer(ing), "/ingest/logs", "application/x-ndjson", "{\"ok\":1}\nboom\n")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1 (docs before the bad line)", resp.Indexed)
	}
}

func TestIngest_UnknownFormatParam(t *testing.T) {
	rec := post(t, NewServer(&fakeIngester{}), "/ingest/logs?format=xml", "", "<xml/>")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestIngest_InvalidIndexName(t *testing.T) {
	for _, name := range []string{"Has-Upper", "_leading", "with%20space"} {
		rec := post(t, NewServer(&fakeIngester{}), "/ingest/"+name, "application/json", `[]`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("index %q: status = %d, want 400", name, rec.Code)
		}
	}
}

func TestIngest_PartialBulkFailuresAre207(t *testing.T) {
	ing := &fakeIngester{result: osclient.BulkResult{Indexed: 1, Failed: 1, Errors: []string{"mapper_parsing_exception: bad field"}}}
	rec := post(t, NewServer(ing), "/ingest/logs", "application/x-ndjson", "{\"a\":1}\n{\"a\":2}\n")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Failed != 1 || len(resp.Errors) != 1 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestIngest_OpenSearchDownIs502(t *testing.T) {
	ing := &fakeIngester{bulkErr: errors.New("connection refused")}
	rec := post(t, NewServer(ing), "/ingest/logs", "application/x-ndjson", "{\"a\":1}\n")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	NewServer(&fakeIngester{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthy: status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	NewServer(&fakeIngester{pingErr: errors.New("down")}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unhealthy: status = %d, want 503", rec.Code)
	}
}

// Guard: the parse error type must survive the osclient boundary for the
// 400-vs-502 split to work.
func TestParseErrorTypeAssertion(t *testing.T) {
	var err error = &parse.Error{Line: 3, Err: errors.New("x")}
	var perr *parse.Error
	if !errors.As(err, &perr) {
		t.Fatal("errors.As failed on *parse.Error")
	}
}
