package httpmw_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/httpmw"
)

// TestMaxBytes_AcceptsSmallBody pins the happy path: a body under
// the configured cap reaches the downstream handler intact.
func TestMaxBytes_AcceptsSmallBody(t *testing.T) {
	t.Parallel()

	const bodyText = "ok"

	var received string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		received = string(data)

		w.WriteHeader(http.StatusOK)
	})

	handler := httpmw.MaxBytes(64)(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader(bodyText))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (body under cap)", resp.StatusCode)
	}

	if received != bodyText {
		t.Errorf("received = %q, want %q", received, bodyText)
	}
}

// TestMaxBytes_RejectsOversizedBody pins the rejection path: a body
// strictly above the cap causes the inner handler's io.ReadAll to
// fail with a MaxBytesError, which the middleware maps to 413
// Request Entity Too Large.
func TestMaxBytes_RejectsOversizedBody(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 4096)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			// MaxBytes wraps the body with http.MaxBytesReader; an
			// oversized read produces *http.MaxBytesError that the
			// middleware's StatusCodeOnOverflow ResponseWriter wrapper
			// will already have set. Just try to reply; if the
			// headers are already written the client sees 413.
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)

			return
		}

		w.WriteHeader(http.StatusOK)
	})

	handler := httpmw.MaxBytes(64)(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader(big))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 (body over cap)", resp.StatusCode)
	}
}

// TestMaxBytes_ZeroCapDisables pins the documented escape hatch:
// passing 0 to MaxBytes leaves the body untouched. Useful for dev
// and tests that want to POST arbitrary payloads.
func TestMaxBytes_ZeroCapDisables(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("z", 10_000)

	var receivedLen int
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		receivedLen = len(data)

		w.WriteHeader(http.StatusOK)
	})

	handler := httpmw.MaxBytes(0)(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader(big))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (zero cap = disabled)", resp.StatusCode)
	}

	if receivedLen != len(big) {
		t.Errorf("received len = %d, want %d (full body must pass)", receivedLen, len(big))
	}
}
