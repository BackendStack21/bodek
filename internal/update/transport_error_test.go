package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roundTripperFunc stubs the transport so a test can inject an arbitrary
// transport error without hitting the network.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// RED: a transport error whose message merely contains "403" (or "401"/"429")
// is not an HTTP status — it must NOT trigger the HTML fallback, which would
// fabricate conventionalAssets URLs for a tag we never verified.
func TestTransportErrorWithStatusDigitsDoesNotFallback(t *testing.T) {
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/BackendStack21/bodek/releases/tag/v9.9.9")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(fallback.Close)
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("proxy connect: refused 10.0.0.3:403")
	})}

	_, _, err := fetchLatest(context.Background(), client, LatestURL, fallback.URL)
	if err == nil {
		t.Fatal("transport error must propagate — the HTML fallback fired on a non-status error")
	}
	if !strings.Contains(err.Error(), "proxy connect") {
		t.Fatalf("expected the original transport error, got %v", err)
	}
}

// Typed status errors must still classify as retryable.
func TestStatusRetryableTypedStatuses(t *testing.T) {
	for _, code := range []int{401, 403, 429} {
		err := statusError(code, "403 Forbidden")
		var se *statusErr
		if !errors.As(err, &se) || se.code != code {
			t.Fatalf("statusError(%d) should wrap a typed statusErr, got %v", code, err)
		}
		if !statusRetryable(err) {
			t.Errorf("status %d should be retryable", code)
		}
	}
	for _, code := range []int{400, 404, 500} {
		if statusRetryable(statusError(code, "oops")) {
			t.Errorf("status %d should not be retryable", code)
		}
	}
	// Wrapped transport errors are never retryable.
	if statusRetryable(errors.New("dial tcp: lookup api.github.com: 429 no such host")) {
		t.Error("transport error containing 429 must not be retryable")
	}
}
