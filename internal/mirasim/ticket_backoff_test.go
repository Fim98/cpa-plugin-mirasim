package mirasim

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// The mint path wraps a transport failure with context before storing it, so
// reading the status off the immediate error would lose it.
func TestTicketBackoffReportsAWrappedUpstreamStatus(t *testing.T) {
	cause := fmt.Errorf("mint Mirasim device ticket: %w", NewStatusError(http.StatusTooManyRequests, nil, nil))
	if status := newTicketBackoffError(cause, time.Second).StatusCode(); status != http.StatusTooManyRequests {
		t.Fatalf("status = %d", status)
	}
}

func TestTicketBackoffFallsBackWhenNoStatusIsCarried(t *testing.T) {
	if status := newTicketBackoffError(errors.New("dial failed"), time.Second).StatusCode(); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", status)
	}
}

func TestTicketBackoffReadsRetryabilityThroughAWrap(t *testing.T) {
	cause := fmt.Errorf("mint Mirasim device ticket: %w", newRefreshHTTPError(http.StatusUnauthorized, nil, nil))
	if newTicketBackoffError(cause, time.Second).Retryable() {
		t.Fatal("a rejected credential is not retryable")
	}
}
