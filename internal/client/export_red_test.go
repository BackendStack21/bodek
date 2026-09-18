package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// RED: ExportSession must not silently truncate an export exceeding the
// in-memory bound — it must error, not return a clipped file.
func TestExportSessionRejectsOversized(t *testing.T) {
	big := strings.Repeat("x", maxExportBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, http: srv.Client()}
	if _, err := c.ExportSession("s1", "", "md"); err == nil {
		t.Fatal("oversized export silently truncated; want error")
	}
}
