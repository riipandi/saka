package logger_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// otlpRequest is one export the collector received, reduced to what a test
// asserts on.
type otlpRequest struct {
	path          string
	authorization string
	body          []byte
}

// otlpTestServer is a stand-in for an OpenTelemetry collector: it answers the
// export endpoint with the empty success response the protocol expects, and
// records what arrived.
type otlpTestServer struct {
	*httptest.Server

	mu       sync.Mutex
	recorded []otlpRequest
}

// newOTLPTestServer starts the collector stand-in. It is closed with the test.
func newOTLPTestServer(t *testing.T) *otlpTestServer {
	t.Helper()

	server := &otlpTestServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(decompressed(r))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		server.mu.Lock()
		server.recorded = append(server.recorded, otlpRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			body:          body,
		})
		server.mu.Unlock()

		// An empty ExportLogsServiceResponse is a valid protobuf message, which
		// is what the exporter reads as success.
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	return server
}

// decompressed reads an export body, which the exporter gzips by default.
//
// The reader is what a real collector does with Content-Encoding: without it a
// test would assert against compressed bytes and see none of the payload. A body
// that is not gzipped is read as-is, so a test that configures no compression
// still works.
func decompressed(r *http.Request) io.Reader {
	if r.Header.Get("Content-Encoding") != "gzip" {
		return r.Body
	}
	reader, err := gzip.NewReader(r.Body)
	if err != nil {
		return r.Body
	}
	return reader
}

// requests returns what the collector has been sent so far.
//
// A shutdown flushes the batch processor synchronously, so a test that reads
// after one sees every record the run produced; there is nothing to wait for.
// Reading before a shutdown would race the batch interval, which is why the
// tests that assert delivery call Shutdown first.
func (s *otlpTestServer) requests() []otlpRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]otlpRequest{}, s.recorded...)
}
