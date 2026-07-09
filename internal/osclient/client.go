// Package osclient wraps the official OpenSearch Go client with batched
// bulk indexing tailored to streaming document iterators.
package osclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	opensearch "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// Batch limits: a bulk request is flushed once it holds maxBatchDocs
// documents or its body exceeds maxBatchBytes.
const (
	maxBatchDocs  = 500
	maxBatchBytes = 5 << 20
	maxErrReasons = 5 // per-document error reasons kept in a BulkResult
)

// Config holds connection settings for an OpenSearch cluster.
type Config struct {
	URL      string
	Username string
	Password string
}

// Client indexes documents into OpenSearch.
type Client struct {
	api *opensearchapi.Client
}

// New connects a Client to the cluster described by cfg.
func New(cfg Config) (*Client, error) {
	api, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{cfg.URL},
			Username:  cfg.Username,
			Password:  cfg.Password,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create opensearch client: %w", err)
	}
	return &Client{api: api}, nil
}

// Ping checks that the cluster is reachable.
func (c *Client) Ping(ctx context.Context) error {
	if _, err := c.api.Ping(ctx, nil); err != nil {
		return fmt.Errorf("opensearch unreachable: %w", err)
	}
	return nil
}

// DocIterator is the stream of documents to index. It matches
// parse.Iterator; Next returns io.EOF after the last document.
type DocIterator interface {
	Next() (map[string]any, error)
}

// BulkResult summarizes a bulk ingestion run.
type BulkResult struct {
	Indexed int      `json:"indexed"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// BulkIndex reads every document from docs and indexes it into index using
// batched _bulk requests. It returns counts for the documents processed so
// far even when the error is non-nil, so callers can report partial
// progress. Iterator errors are returned as-is (e.g. *parse.Error);
// transport failures are wrapped.
func (c *Client) BulkIndex(ctx context.Context, index string, docs DocIterator) (BulkResult, error) {
	var (
		result BulkResult
		body   bytes.Buffer
		count  int
	)
	action, err := json.Marshal(map[string]any{"index": map[string]string{"_index": index}})
	if err != nil {
		return result, err
	}

	flush := func() error {
		if count == 0 {
			return nil
		}
		err := c.sendBatch(ctx, &result, body.Bytes(), count)
		body.Reset()
		count = 0
		return err
	}

	for {
		doc, err := docs.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Index what we already buffered so the caller gets an
			// accurate partial count alongside the parse error.
			if ferr := flush(); ferr != nil {
				return result, ferr
			}
			return result, err
		}
		line, err := json.Marshal(doc)
		if err != nil {
			return result, err
		}
		body.Write(action)
		body.WriteByte('\n')
		body.Write(line)
		body.WriteByte('\n')
		count++
		if count >= maxBatchDocs || body.Len() >= maxBatchBytes {
			if err := flush(); err != nil {
				return result, err
			}
		}
	}
	return result, flush()
}

// sendBatch submits one _bulk body and folds per-item outcomes into result.
func (c *Client) sendBatch(ctx context.Context, result *BulkResult, body []byte, count int) error {
	resp, err := c.api.Bulk(ctx, opensearchapi.BulkReq{Body: bytes.NewReader(body)})
	if err != nil {
		return fmt.Errorf("bulk request: %w", err)
	}
	if !resp.Errors {
		result.Indexed += count
		return nil
	}
	for _, item := range resp.Items {
		for _, detail := range item {
			if detail.Error != nil || detail.Status >= 300 {
				result.Failed++
				if len(result.Errors) < maxErrReasons && detail.Error != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("%s: %s", detail.Error.Type, detail.Error.Reason))
				}
			} else {
				result.Indexed++
			}
		}
	}
	return nil
}
