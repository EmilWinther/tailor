// Command api runs the ingestion HTTP server: it accepts data file uploads
// and bulk-indexes their contents into OpenSearch.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emilwinther/tailor/internal/api"
	"github.com/emilwinther/tailor/internal/osclient"
)

func main() {
	var (
		listen = flag.String("listen", envOr("LISTEN_ADDR", ":8080"), "address for the HTTP server")
		osURL  = flag.String("opensearch-url", envOr("OPENSEARCH_URL", "http://localhost:9200"), "OpenSearch base URL")
	)
	flag.Parse()

	client, err := osclient.New(osclient.Config{
		URL:      *osURL,
		Username: os.Getenv("OPENSEARCH_USERNAME"),
		Password: os.Getenv("OPENSEARCH_PASSWORD"),
	})
	if err != nil {
		log.Fatalf("connect to opensearch: %v", err)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.NewServer(client),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("listening on %s, forwarding to OpenSearch at %s", *listen, *osURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

// envOr returns the value of the environment variable key, or fallback if
// it is unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
