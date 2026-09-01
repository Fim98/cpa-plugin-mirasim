package mirasim

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
)

type fakeHostClient struct {
	do       func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)
	doStream func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error)
}

func (f fakeHostClient) Do(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	if f.do == nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected non-stream request")
	}
	return f.do(ctx, req)
}

func (f fakeHostClient) DoStream(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	if f.doStream == nil {
		return pluginapi.HTTPStreamResponse{}, fmt.Errorf("unexpected stream request")
	}
	return f.doStream(ctx, req)
}

func TestListModelsSignsRequestsAndCapturesQuota(t *testing.T) {
	accessToken := futureJWT()
	storage, publicKey := newTestStorage(t, accessToken)
	client := NewClient(storage)
	calls := 0
	host := fakeHostClient{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		calls++
		assertSignedRequest(t, publicKey, req)
		parsed, errParse := url.Parse(req.URL)
		if errParse != nil {
			t.Errorf("parse request URL: %v", errParse)
		}
		switch parsed.Path {
		case sessionPath:
			if req.Headers.Get("Authorization") != "Bearer "+accessToken {
				t.Errorf("session authorization = %q", req.Headers.Get("Authorization"))
			}
			return pluginapi.HTTPResponse{
				StatusCode: http.StatusOK,
				Headers:    make(http.Header),
				Body:       []byte(`{"ticket":"device-ticket","expiresIn":900}`),
			}, nil
		case modelsPath:
			if req.Headers.Get("Authorization") != "Bearer device-ticket" {
				t.Errorf("models authorization = %q", req.Headers.Get("Authorization"))
			}
			headers := make(http.Header)
			headers.Set(quotaHeaderNames[0], "0.25")
			headers.Set(quotaHeaderNames[1], "1788167238")
			headers.Set(quotaHeaderNames[2], "0.5")
			headers.Set(quotaHeaderNames[3], "1788431522")
			return pluginapi.HTTPResponse{
				StatusCode: http.StatusOK,
				Headers:    headers,
				Body:       []byte(`{"object":"list","data":[{"id":"claude-sonnet-5","object":"model"},{"id":"gpt-5.6-sol","object":"model"}]}`),
			}, nil
		default:
			return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected path %s", parsed.Path)
		}
	}}

	catalog, errCatalog := client.ListModels(context.Background(), host)
	if errCatalog != nil {
		t.Fatalf("ListModels() error = %v", errCatalog)
	}
	if calls != 2 || len(catalog.Models) != 2 {
		t.Fatalf("calls = %d, models = %#v", calls, catalog.Models)
	}
	if catalog.Quota.FiveHour.Utilization != "0.25" || catalog.Quota.SevenDay.Utilization != "0.5" {
		t.Fatalf("quota = %#v", catalog.Quota)
	}
	if !catalog.Quota.Available {
		t.Fatalf("quota signal unexpectedly unavailable: %#v", catalog.Quota)
	}
	if _, ok := catalog.Quota.Headers["anthropic-ratelimit-unified-5h-utilization"]; !ok {
		t.Fatalf("quota headers do not preserve stable lowercase names: %#v", catalog.Quota.Headers)
	}
	if catalog.Quota.FiveHour.ResetAt == nil || catalog.Quota.FiveHour.ResetAt.Unix() != 1788167238 {
		t.Fatalf("five-hour reset = %#v", catalog.Quota.FiveHour.ResetAt)
	}

	// LastQuota must not expose the cached map by reference.
	catalog.Quota.Headers[quotaHeaderNames[0]] = "changed"
	if client.LastQuota().Headers[quotaHeaderNames[0]] != "0.25" {
		t.Fatal("LastQuota returned mutable cached state")
	}
}

func TestListModelsDoesNotReuseStaleQuotaWhenHeadersDisappear(t *testing.T) {
	storage, publicKey := newTestStorage(t, futureJWT())
	client := NewClient(storage)
	modelCalls := 0
	host := fakeHostClient{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		assertSignedRequest(t, publicKey, req)
		parsed, _ := url.Parse(req.URL)
		if parsed.Path == sessionPath {
			return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: make(http.Header), Body: []byte(`{"ticket":"device-ticket","expiresIn":900}`)}, nil
		}
		modelCalls++
		headers := make(http.Header)
		if modelCalls == 1 {
			headers.Set(quotaHeaderNames[0], "0.25")
		}
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: headers, Body: []byte(`{"data":[{"id":"gpt-5.6-sol"}]}`)}, nil
	}}

	first, errFirst := client.ListModels(context.Background(), host)
	if errFirst != nil || !first.Quota.Available {
		t.Fatalf("first quota = %#v, error = %v", first.Quota, errFirst)
	}
	second, errSecond := client.ListModels(context.Background(), host)
	if errSecond != nil {
		t.Fatalf("second ListModels() error = %v", errSecond)
	}
	if second.Quota.Available || len(second.Quota.Headers) != 0 {
		t.Fatalf("second call reused stale quota: %#v", second.Quota)
	}
}

func TestDoRetriesOneUnauthorizedResponseWithFreshTicket(t *testing.T) {
	storage, publicKey := newTestStorage(t, futureJWT())
	client := NewClient(storage)
	var ticketCalls int
	var messageCalls int
	host := fakeHostClient{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		assertSignedRequest(t, publicKey, req)
		parsed, _ := url.Parse(req.URL)
		if parsed.Path == sessionPath {
			ticketCalls++
			return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: make(http.Header), Body: []byte(fmt.Sprintf(`{"ticket":"ticket-%d","expiresIn":900}`, ticketCalls))}, nil
		}
		if parsed.Path != "/v1/messages" {
			return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected path %s", parsed.Path)
		}
		messageCalls++
		if messageCalls == 1 {
			if req.Headers.Get("Authorization") != "Bearer ticket-1" {
				t.Errorf("first ticket = %q", req.Headers.Get("Authorization"))
			}
			return pluginapi.HTTPResponse{StatusCode: http.StatusUnauthorized, Headers: make(http.Header), Body: []byte(`{"error":"expired"}`)}, nil
		}
		if req.Headers.Get("Authorization") != "Bearer ticket-2" {
			t.Errorf("second ticket = %q", req.Headers.Get("Authorization"))
		}
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: make(http.Header), Body: []byte(`{"ok":true}`)}, nil
	}}

	resp, errDo := client.Do(context.Background(), host, http.MethodPost, "/v1/messages", nil, http.Header{
		"Authorization": []string{"Bearer client-secret"},
		"X-Api-Key":     []string{"client-key"},
	}, []byte(`{"model":"claude-sonnet-5"}`))
	if errDo != nil {
		t.Fatalf("Do() error = %v", errDo)
	}
	if resp.StatusCode != http.StatusOK || ticketCalls != 2 || messageCalls != 2 {
		t.Fatalf("status = %d, ticket calls = %d, message calls = %d", resp.StatusCode, ticketCalls, messageCalls)
	}
}

func TestRefreshAccessReadsLatestDiskTokenAndDoesNotLeakErrorBody(t *testing.T) {
	storage, _ := newTestStorage(t, futureJWT())
	var expected atomic.Value
	expected.Store("refresh-token")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		_ = json.Unmarshal(body, &payload)
		if payload["refresh_token"] != expected.Load().(string) {
			t.Errorf("refresh token = %q, want %q", payload["refresh_token"], expected.Load().(string))
		}
		call := calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  jwtWithExpiry(time.Now().Add(time.Hour)),
			"refresh_token": fmt.Sprintf("rotated-%d", call),
		})
	}))
	defer server.Close()
	storage.AdminURL = server.URL
	client := NewClient(storage)

	if _, errRefresh := client.RefreshAccess(context.Background()); errRefresh != nil {
		t.Fatalf("RefreshAccess() error = %v", errRefresh)
	}
	assertCredentialFile(t, storage.CredentialDir, "refresh-token.txt", "rotated-1")

	// Simulate mira2api or another process rotating the project-local token.
	expected.Store("external-rotation")
	if errWrite := os.WriteFile(filepath.Join(storage.CredentialDir, "refresh-token.txt"), []byte("external-rotation\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	if _, errRefresh := client.RefreshAccess(context.Background()); errRefresh != nil {
		t.Fatalf("second RefreshAccess() error = %v", errRefresh)
	}
	assertCredentialFile(t, storage.CredentialDir, "refresh-token.txt", "rotated-2")

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"SUPER_SECRET_REFLECTION"}`))
	}))
	defer errorServer.Close()
	storage.AdminURL = errorServer.URL
	errClient := NewClient(storage)
	_, errRefresh := errClient.RefreshAccess(context.Background())
	if errRefresh == nil || strings.Contains(errRefresh.Error(), "SUPER_SECRET_REFLECTION") {
		t.Fatalf("refresh error leaks response body: %v", errRefresh)
	}
}

func TestParseModelCatalogSupportsDataAndModelsShapes(t *testing.T) {
	models, errParse := ParseModelCatalog([]byte(`{"models":["one",{"id":"two"},{"id":"one"}]}`))
	if errParse != nil {
		t.Fatalf("ParseModelCatalog() error = %v", errParse)
	}
	if len(models) != 2 || models[0].ID != "one" || models[1].ID != "two" {
		t.Fatalf("models = %#v", models)
	}
}

func TestPrepareHeadersDropsClientCredentials(t *testing.T) {
	auth := http.Header{"Authorization": []string{"Bearer ticket"}, "X-Mirasim-Sig": []string{"signature"}}
	headers := prepareHeaders(http.Header{
		"Authorization":       []string{"Bearer client"},
		"Proxy-Authorization": []string{"proxy-secret"},
		"X-Api-Key":           []string{"client-key"},
	}, auth, false)
	if headers.Get("Authorization") != "Bearer ticket" || headers.Get("X-Mirasim-Sig") != "signature" {
		t.Fatalf("auth headers = %#v", headers)
	}
	if headers.Get("Proxy-Authorization") != "" || headers.Get("X-Api-Key") != "" {
		t.Fatalf("client credentials survived sanitization: %#v", headers)
	}
}

func newTestStorage(t *testing.T, accessToken string) (credentials.Storage, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, errKey := ed25519.GenerateKey(rand.Reader)
	if errKey != nil {
		t.Fatal(errKey)
	}
	privateDER, errMarshal := x509.MarshalPKCS8PrivateKey(privateKey)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"refresh-token.txt":      []byte("refresh-token\n"),
		"access-token.txt":       []byte(accessToken + "\n"),
		"device-private-key.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	}
	for name, data := range files {
		if errWrite := os.WriteFile(filepath.Join(dir, name), data, 0o600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
	return credentials.Storage{
		Type:          credentials.Provider,
		CredentialDir: dir,
		RelayURL:      "https://relay.example",
		AdminURL:      "https://admin.example",
		ClientVersion: "test-client",
	}, publicKey
}

func assertSignedRequest(t *testing.T, publicKey ed25519.PublicKey, req pluginapi.HTTPRequest) {
	t.Helper()
	parsed, errParse := url.Parse(req.URL)
	if errParse != nil {
		t.Errorf("parse signed URL: %v", errParse)
		return
	}
	timestamp := req.Headers.Get("X-Mirasim-Ts")
	nonce := req.Headers.Get("X-Mirasim-Nonce")
	signatureText := req.Headers.Get("X-Mirasim-Sig")
	signature, errDecode := base64.RawURLEncoding.DecodeString(signatureText)
	if errDecode != nil {
		t.Errorf("decode signature: %v", errDecode)
		return
	}
	digest := sha256.Sum256(req.Body)
	payload := strings.Join([]string{
		signatureVersion,
		strings.ToUpper(req.Method),
		parsed.Path,
		timestamp,
		nonce,
		hex.EncodeToString(digest[:]),
	}, "\n")
	if !ed25519.Verify(publicKey, []byte(payload), signature) {
		t.Errorf("invalid signature for %s %s", req.Method, parsed.Path)
	}
	if req.Headers.Get("X-Mirasim-Client") != "test-client" {
		t.Errorf("client version = %q", req.Headers.Get("X-Mirasim-Client"))
	}
}

func futureJWT() string {
	return jwtWithExpiry(time.Now().Add(time.Hour))
}

func jwtWithExpiry(expiry time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiry.Unix())))
	return header + "." + payload + ".signature"
}

func assertCredentialFile(t *testing.T, dir, name, expected string) {
	t.Helper()
	raw, errRead := os.ReadFile(filepath.Join(dir, name))
	if errRead != nil {
		t.Fatal(errRead)
	}
	if strings.TrimSpace(string(raw)) != expected {
		t.Fatalf("%s = %q, want %q", name, raw, expected)
	}
}

func TestStatusErrorLimitsDisplayedBody(t *testing.T) {
	body := bytes.Repeat([]byte("x"), maxErrorMessage+100)
	message := NewStatusError(http.StatusBadGateway, body, nil).Error()
	if len(message) > maxErrorMessage+100 || !strings.HasSuffix(message, "...") {
		t.Fatalf("status error was not bounded: length=%d", len(message))
	}
}
