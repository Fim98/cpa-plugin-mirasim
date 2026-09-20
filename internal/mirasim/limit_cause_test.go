package mirasim

import (
	"net/http"
	"strings"
	"testing"
)

func rateLimited(errorType string, headers http.Header) *StatusError {
	body := []byte(`{"error":{"type":"` + errorType + `","message":"slow down"}}`)
	return NewStatusError(http.StatusTooManyRequests, body, headers)
}

func utilizationHeaders(pairs map[string]string) http.Header {
	headers := http.Header{}
	for name, value := range pairs {
		headers.Set("anthropic-ratelimit-unified-"+name, value)
	}
	return headers
}

// A spent budget clears on its own and a blocked region does not, so a caller
// deciding whether to wait needs the two told apart.
func TestLimitCauseSeparatesAnExhaustedBudgetFromARefusal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		errorType string
		headers   http.Header
		want      string
	}{
		{"region", "shared_quota_unavailable", nil, LimitRegionBlocked},
		{"plan", "credit_exhausted_shared", nil, LimitPlanRequired},
		{"throttle", "rate_limited", nil, LimitThrottled},
		{"unknown", "", nil, LimitCauseUnknown},
		{
			name:      "spent budget outranks the error type",
			errorType: "rate_limited",
			headers:   utilizationHeaders(map[string]string{"5h-utilization": "1"}),
			want:      LimitCreditExhausted,
		},
		{
			// A region block is refused outright, so it stays a refusal even when
			// a window happens to be full as well.
			name:      "region block survives a full window",
			errorType: "shared_quota_unavailable",
			headers:   utilizationHeaders(map[string]string{"7d-utilization": "1"}),
			want:      LimitRegionBlocked,
		},
		{
			name:      "an unspent window is not exhaustion",
			errorType: "rate_limited",
			headers:   utilizationHeaders(map[string]string{"5h-utilization": "0.4"}),
			want:      LimitThrottled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := rateLimited(tc.errorType, tc.headers).LimitCause()
			if cause == nil || cause.Cause != tc.want {
				t.Fatalf("cause = %#v, want %s", cause, tc.want)
			}
		})
	}
}

// The window that governs the wait is the one that clears last.
func TestLimitCauseReportsTheFurthestReset(t *testing.T) {
	cause := rateLimited("rate_limited", utilizationHeaders(map[string]string{
		"5h-utilization": "1",
		"5h-reset":       "1788200000",
		"7d-utilization": "1.2",
		"7d-reset":       "1788900000",
	})).LimitCause()

	if cause == nil || cause.Cause != LimitCreditExhausted || cause.Window != "7d" {
		t.Fatalf("cause = %#v", cause)
	}
	if cause.ResetAt == nil || cause.ResetAt.Unix() != 1788900000 {
		t.Fatalf("reset = %v", cause.ResetAt)
	}
	if !strings.Contains(cause.Describe(), "credit exhausted in 7d") {
		t.Fatalf("describe = %q", cause.Describe())
	}
}

func TestLimitCauseIgnoresAnyOtherStatus(t *testing.T) {
	if cause := NewStatusError(http.StatusBadGateway, []byte(`{"error":{"type":"rate_limited"}}`), nil).LimitCause(); cause != nil {
		t.Fatalf("cause = %#v", cause)
	}
}

// The classification has to reach the message CPA surfaces, or it only exists
// for callers that know to ask for it.
func TestStatusErrorMessageCarriesTheCause(t *testing.T) {
	message := rateLimited("credit_exhausted_shared", nil).Error()
	if !strings.Contains(message, "plan required") {
		t.Fatalf("message = %q", message)
	}
	plain := NewStatusError(http.StatusInternalServerError, []byte("boom"), nil).Error()
	if strings.Contains(plain, "(") {
		t.Fatalf("non-429 message gained a cause: %q", plain)
	}
}
