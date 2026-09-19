package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailCodeSignInExchangesAMailedCodeForCredentials(t *testing.T) {
	var requested map[string]string
	var verified map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		switch r.URL.Path {
		case emailCodeResource:
			_ = json.NewDecoder(r.Body).Decode(&requested)
			_ = json.NewEncoder(w).Encode(map[string]string{})
		case emailVerifyResource:
			_ = json.NewDecoder(r.Body).Decode(&verified)
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access", "refresh_token": "refresh"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	if errRequest := requestEmailCode(context.Background(), server.URL, "", "user@example.com"); errRequest != nil {
		t.Fatalf("requestEmailCode() error = %v", errRequest)
	}
	if requested["email"] != "user@example.com" {
		t.Fatalf("code request body = %#v", requested)
	}
	accessToken, refreshToken, errVerify := verifyEmailCode(context.Background(), server.URL, "", "user@example.com", "123456")
	if errVerify != nil {
		t.Fatalf("verifyEmailCode() error = %v", errVerify)
	}
	if accessToken != "access" || refreshToken != "refresh" {
		t.Fatalf("credentials = %q / %q", accessToken, refreshToken)
	}
	if verified["email"] != "user@example.com" || verified["code"] != "123456" {
		t.Fatalf("verify request body = %#v", verified)
	}
}

func TestEmailCodeSignInRefusesCredentialsThatCannotBeRenewed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access"})
	}))
	defer server.Close()
	if _, _, errVerify := verifyEmailCode(context.Background(), server.URL, "", "user@example.com", "123456"); errVerify == nil {
		t.Fatal("a credential CPA cannot refresh was accepted")
	}
}

func TestEmailCodeSignInReportsTheServiceDetailWithoutLeakingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "too many code requests"})
	}))
	defer server.Close()
	errRequest := requestEmailCode(context.Background(), server.URL, "", "user@example.com")
	if errRequest == nil || !strings.Contains(errRequest.Error(), "too many code requests") {
		t.Fatalf("error = %v", errRequest)
	}
	if got := adminErrorDetail([]byte(`{"detail":"line\none"}`)); got != "" {
		t.Fatalf("control characters survived into an error: %q", got)
	}
}

func TestEmailSignInRejectsUnusableAddressesAndCodes(t *testing.T) {
	for _, address := range []string{"", "user", "user@example", "user @example.com", "user@exa mple.com"} {
		if _, errEmail := normalizeLoginEmail(address); errEmail == nil {
			t.Fatalf("accepted address %q", address)
		}
	}
	if got, errEmail := normalizeLoginEmail("  user@example.co.uk "); errEmail != nil || got != "user@example.co.uk" {
		t.Fatalf("address = %q, error = %v", got, errEmail)
	}
	for _, code := range []string{"", "   ", "12\n34", strings.Repeat("9", 65)} {
		if _, errCode := normalizeLoginCode(code); errCode == nil {
			t.Fatalf("accepted code %q", code)
		}
	}
	if got, errCode := normalizeLoginCode(" 123456\n"); errCode != nil || got != "123456" {
		t.Fatalf("code = %q, error = %v", got, errCode)
	}
}

func TestEmailSignInRejectsAPlaintextAuthenticationService(t *testing.T) {
	if _, errEndpoint := adminEndpoint("http://auth.example.com", emailCodeResource); errEndpoint == nil {
		t.Fatal("a plaintext non-loopback authentication service was accepted")
	}
	endpoint, errEndpoint := adminEndpoint("https://auth.example.com/", emailVerifyResource)
	if errEndpoint != nil || endpoint != "https://auth.example.com/auth/verify" {
		t.Fatalf("endpoint = %q, error = %v", endpoint, errEndpoint)
	}
}
