// Package api exposes the HTTP surface: file ingestion and health checks.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"

	"github.com/emilwinther/tailor/internal/osclient"
	"github.com/emilwinther/tailor/internal/parse"
)

// Ingester is what the handlers need from OpenSearch. *osclient.Client
// satisfies it; tests substitute a fake.
type Ingester interface {
	BulkIndex(ctx context.Context, index string, docs osclient.DocIterator) (osclient.BulkResult, error)
	Ping(ctx context.Context) error
}

// NewServer routes the API onto a fresh mux.
func NewServer(ing Ingester) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ingest/{index}", handleIngest(ing))
	mux.HandleFunc("GET /healthz", handleHealth(ing))
	return mux
}

// ingestResponse is the JSON body returned by POST /ingest/{index}.
type ingestResponse struct {
	Index   string   `json:"index"`
	Indexed int      `json:"indexed"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func handleIngest(ing Ingester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		index := r.PathValue("index")
		if !validIndexName(index) {
			writeJSON(w, http.StatusBadRequest, ingestResponse{
				Index: index,
				Error: "invalid index name: must be lowercase and contain no spaces or characters from " + `\ / * ? " < > | , #`,
			})
			return
		}

		data, filename, err := requestFile(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ingestResponse{Index: index, Error: err.Error()})
			return
		}
		defer func() { _ = data.Close() }()

		format, err := parse.Detect(r.URL.Query().Get("format"), filename, r.Header.Get("Content-Type"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ingestResponse{Index: index, Error: err.Error()})
			return
		}
		docs, err := parse.New(data, format)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ingestResponse{Index: index, Error: err.Error()})
			return
		}

		result, err := ing.BulkIndex(r.Context(), index, docs)
		resp := ingestResponse{Index: index, Indexed: result.Indexed, Failed: result.Failed, Errors: result.Errors}
		switch {
		case err == nil && result.Failed == 0:
			writeJSON(w, http.StatusOK, resp)
		case err == nil:
			writeJSON(w, http.StatusMultiStatus, resp)
		default:
			resp.Error = err.Error()
			var perr *parse.Error
			if errors.As(err, &perr) {
				writeJSON(w, http.StatusBadRequest, resp)
			} else {
				writeJSON(w, http.StatusBadGateway, resp)
			}
		}
	}
}

func handleHealth(ing Ingester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ing.Ping(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable", "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// requestFile extracts the uploaded file from the request: the `file` part
// of a multipart form, or the raw request body. The filename (if any) feeds
// format detection.
func requestFile(r *http.Request) (io.ReadCloser, string, error) {
	ct := r.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ct); err == nil && mt == "multipart/form-data" {
		file, header, err := r.FormFile("file")
		if err != nil {
			return nil, "", errors.New(`multipart upload must include a "file" field`)
		}
		return file, header.Filename, nil
	}
	return r.Body, r.URL.Query().Get("filename"), nil
}

// validIndexName enforces the OpenSearch index naming rules that matter for
// a URL path segment: lowercase, no illegal characters, no leading _ - +.
func validIndexName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return false
	}
	if strings.ContainsAny(name, ` \/*?"<>|,#:`) || name != strings.ToLower(name) {
		return false
	}
	switch name[0] {
	case '_', '-', '+':
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
