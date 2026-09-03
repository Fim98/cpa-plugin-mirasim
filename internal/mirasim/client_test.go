package mirasim

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
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
	storage, publicKey, relayPrivate := newTestStorage(t, accessToken)
	client := NewClient(storage)
	calls := 0
	host := fakeHostClient{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		calls++
		parsed, errParse := url.Parse(req.URL)
		if errParse != nil {
			t.Errorf("parse request URL: %v", errParse)
		}
		switch parsed.Path {
		case sessionPath:
			assertDeviceSessionRequest(t, publicKey, req, accessToken)
			if req.Headers.Get("Authorization") != "Bearer "+accessToken {
				t.Errorf("session authorization = %q", req.Headers.Get("Authorization"))
			}
			return pluginapi.HTTPResponse{
				StatusCode: http.StatusOK,
				Headers:    make(http.Header),
				Body:       []byte(`{"ticket":"device-ticket","expiresIn":900}`),
			}, nil
		case modelsPath:
			assertSealedRelayRequest(t, publicKey, relayPrivate, req, "device-ticket")
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
	accessToken := futureJWT()
	storage, publicKey, relayPrivate := newTestStorage(t, accessToken)
	client := NewClient(storage)
	modelCalls := 0
	host := fakeHostClient{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		parsed, _ := url.Parse(req.URL)
		if parsed.Path == sessionPath {
			assertDeviceSessionRequest(t, publicKey, req, accessToken)
			return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: make(http.Header), Body: []byte(`{"ticket":"device-ticket","expiresIn":900}`)}, nil
		}
		assertSealedRelayRequest(t, publicKey, relayPrivate, req, "device-ticket")
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
	accessToken := futureJWT()
	storage, publicKey, relayPrivate := newTestStorage(t, accessToken)
	client := NewClient(storage)
	var ticketCalls int
	var messageCalls int
	host := fakeHostClient{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		parsed, _ := url.Parse(req.URL)
		if parsed.Path == sessionPath {
			assertDeviceSessionRequest(t, publicKey, req, accessToken)
			ticketCalls++
			return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: make(http.Header), Body: []byte(fmt.Sprintf(`{"ticket":"ticket-%d","expiresIn":900}`, ticketCalls))}, nil
		}
		if parsed.Path != "/v1/messages" {
			return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected path %s", parsed.Path)
		}
		if parsed.Query().Get("beta") != "1" {
			t.Errorf("forwarded query = %q", parsed.RawQuery)
		}
		messageCalls++
		assertSealedRelayRequest(t, publicKey, relayPrivate, req, fmt.Sprintf("ticket-%d", messageCalls))
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

	resp, errDo := client.Do(context.Background(), host, http.MethodPost, "/v1/messages", url.Values{"beta": []string{"1"}}, http.Header{
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

func TestDoDelegatesAccessTokenRefreshToHost(t *testing.T) {
	storage, _, _ := newTestStorage(t, jwtWithExpiry(time.Now().Add(time.Minute)))
	client := NewClient(storage)
	hostCalls := 0
	host := fakeHostClient{do: func(_ context.Context, _ pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		hostCalls++
		return pluginapi.HTTPResponse{}, nil
	}}

	_, errDo := client.Do(context.Background(), host, http.MethodPost, "/v1/messages", nil, nil, []byte(`{"model":"claude-sonnet-5"}`))
	if errDo == nil {
		t.Fatal("Do() accepted an access token inside the refresh lead")
	}
	statusErr, ok := errDo.(interface{ StatusCode() int })
	if !ok || statusErr.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("Do() error = %v, want host-refreshable HTTP 401", errDo)
	}
	if hostCalls != 0 {
		t.Fatalf("host calls = %d, want no relay request before CPA refresh", hostCalls)
	}
}

func TestDeviceSessionUnauthorizedDoesNotRefreshInsideRequest(t *testing.T) {
	accessToken := futureJWT()
	storage, _, _ := newTestStorage(t, accessToken)
	client := NewClient(storage)
	host := fakeHostClient{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		parsed, _ := url.Parse(req.URL)
		if parsed.Path != sessionPath {
			t.Fatalf("unexpected request path %s", parsed.Path)
		}
		return pluginapi.HTTPResponse{StatusCode: http.StatusUnauthorized, Headers: make(http.Header), Body: []byte(`{"error":"expired"}`)}, nil
	}}

	_, errDo := client.Do(context.Background(), host, http.MethodPost, "/v1/messages", nil, nil, []byte(`{"model":"claude-sonnet-5"}`))
	statusErr, ok := errDo.(interface{ StatusCode() int })
	if !ok || statusErr.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("Do() error = %v, want host-refreshable HTTP 401", errDo)
	}
	if client.Storage().AccessToken != accessToken {
		t.Fatal("request path mutated provider storage instead of delegating refresh to CPA")
	}
}

func TestRefreshAccessRotatesInMemoryStorageAndDoesNotLeakErrorBody(t *testing.T) {
	storage, _, _ := newTestStorage(t, futureJWT())
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
	if refreshed := client.Storage(); refreshed.RefreshToken != "rotated-1" || refreshed.AccessToken == "" {
		t.Fatal("first refresh did not update provider-owned storage")
	}

	expected.Store("rotated-1")
	if _, errRefresh := client.RefreshAccess(context.Background()); errRefresh != nil {
		t.Fatalf("second RefreshAccess() error = %v", errRefresh)
	}
	if refreshed := client.Storage(); refreshed.RefreshToken != "rotated-2" {
		t.Fatal("second refresh did not retain the rotated refresh token")
	}

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

func TestRefreshAccessWithProxyUsesHostProxyPrivately(t *testing.T) {
	storage, _, _ := newTestStorage(t, futureJWT())
	storage.AdminURL = "http://auth.invalid"
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Host != "auth.invalid" || r.URL.Path != "/auth/refresh" {
			t.Errorf("proxied URL = %s", r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("refresh-token")) {
			t.Error("proxy did not receive the refresh request body")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": jwtWithExpiry(time.Now().Add(time.Hour))})
	}))
	defer proxy.Close()

	client := NewClient(storage)
	if _, errRefresh := client.RefreshAccessWithProxy(context.Background(), proxy.URL); errRefresh != nil {
		t.Fatalf("RefreshAccessWithProxy() error = %v", errRefresh)
	}
	if calls.Load() != 1 {
		t.Fatalf("proxy calls = %d, want 1", calls.Load())
	}
	if errProxy := client.SetAuthProxy("ftp://proxy.invalid"); errProxy == nil {
		t.Fatal("unsupported proxy URL was accepted")
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
	auth := http.Header{"Authorization": []string{"Bearer ticket"}, "X-Mirasim-Enc": []string{"sealed"}}
	headers := prepareHeaders(http.Header{
		"Authorization":       []string{"Bearer client"},
		"Proxy-Authorization": []string{"proxy-secret"},
		"X-Api-Key":           []string{"client-key"},
		"X-Mirasim-Session":   []string{"caller-controlled"},
	}, auth, false)
	if headers.Get("Authorization") != "Bearer ticket" || headers.Get("X-Mirasim-Enc") != "sealed" {
		t.Fatalf("auth headers = %#v", headers)
	}
	if headers.Get("Proxy-Authorization") != "" || headers.Get("X-Api-Key") != "" || headers.Get("X-Mirasim-Session") != "" {
		t.Fatalf("client credentials survived sanitization: %#v", headers)
	}
}

func newTestStorage(t *testing.T, accessToken string) (credentials.Storage, ed25519.PublicKey, []byte) {
	t.Helper()
	publicKey, privateKey, errKey := ed25519.GenerateKey(rand.Reader)
	if errKey != nil {
		t.Fatal(errKey)
	}
	privateDER, errMarshal := x509.MarshalPKCS8PrivateKey(privateKey)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	relayPrivate := make([]byte, curve25519.ScalarSize)
	if _, errRandom := rand.Read(relayPrivate); errRandom != nil {
		t.Fatal(errRandom)
	}
	relayPublic, errRelay := curve25519.X25519(relayPrivate, curve25519.Basepoint)
	if errRelay != nil {
		t.Fatal(errRelay)
	}
	t.Setenv("MIRASIM_SEAL_PUBKEY", base64.StdEncoding.EncodeToString(relayPublic))
	return credentials.Storage{
		Type:             credentials.Provider,
		AccessToken:      accessToken,
		RefreshToken:     "refresh-token",
		DevicePrivateKey: strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))),
		RelayURL:         "https://relay.example",
		AdminURL:         "https://admin.example",
		ClientVersion:    "test-client",
	}, publicKey, relayPrivate
}

func assertDeviceSessionRequest(t *testing.T, publicKey ed25519.PublicKey, req pluginapi.HTTPRequest, credential string) {
	t.Helper()
	if req.Headers.Get(headerMirasimEncryptedMetadata) != "" {
		t.Error("device session request unexpectedly sealed its signature headers")
	}
	signed := map[string]string{
		headerMirasimDevice:    req.Headers.Get(headerMirasimDevice),
		headerMirasimTimestamp: req.Headers.Get(headerMirasimTimestamp),
		headerMirasimNonce:     req.Headers.Get(headerMirasimNonce),
		headerMirasimSignature: req.Headers.Get(headerMirasimSignature),
	}
	assertV2Signature(t, publicKey, req, credential, nil, signed)
}

func assertSealedRelayRequest(t *testing.T, publicKey ed25519.PublicKey, relayPrivate []byte, req pluginapi.HTTPRequest, credential string) {
	t.Helper()
	for name := range req.Headers {
		lowerName := strings.ToLower(name)
		if isSealedRelayHeader(lowerName) {
			t.Errorf("Mirasim header %s leaked outside x-mirasim-enc", name)
		}
	}
	sealed := req.Headers.Get(headerMirasimEncryptedMetadata)
	if sealed == "" {
		t.Fatal("relay request is missing x-mirasim-enc")
	}
	metadata := decryptRelayMetadata(t, relayPrivate, req.Method, mustRequestPath(t, req.URL), sealed)
	for _, name := range []string{headerMirasimSession, headerMirasimAgent, headerMirasimCall, headerMirasimDevice, headerMirasimTimestamp, headerMirasimNonce, headerMirasimSignature} {
		if metadata[name] == "" {
			t.Errorf("sealed metadata is missing %s: %#v", name, metadata)
		}
	}
	if metadata[headerMirasimAgent] != relayAgent(mustRequestPath(t, req.URL)) {
		t.Errorf("sealed agent = %q", metadata[headerMirasimAgent])
	}
	if !strings.HasPrefix(metadata[headerMirasimSession], "mirasim_") {
		t.Errorf("sealed session = %q", metadata[headerMirasimSession])
	}
	signatureMetadata := make(map[string]string)
	for name, value := range metadata {
		if _, isSignature := signatureHeaderNames[name]; !isSignature {
			signatureMetadata[name] = value
		}
	}
	assertV2Signature(t, publicKey, req, credential, signatureMetadata, metadata)
}

func assertV2Signature(t *testing.T, publicKey ed25519.PublicKey, req pluginapi.HTTPRequest, credential string, metadata, signed map[string]string) {
	t.Helper()
	parsed, errParse := url.Parse(req.URL)
	if errParse != nil {
		t.Errorf("parse signed URL: %v", errParse)
		return
	}
	signatureText := signed[headerMirasimSignature]
	signature, errDecode := base64.RawURLEncoding.DecodeString(signatureText)
	if errDecode != nil {
		t.Errorf("decode signature: %v", errDecode)
		return
	}
	payload, errCanonical := canonicalSignaturePayload(signingInput{
		Method:        req.Method,
		Path:          parsed.Path,
		Timestamp:     signed[headerMirasimTimestamp],
		Nonce:         signed[headerMirasimNonce],
		DeviceID:      signed[headerMirasimDevice],
		ClientVersion: req.Headers.Get(headerMirasimClient),
		Credential:    credential,
		Metadata:      metadata,
		Body:          req.Body,
	})
	if errCanonical != nil {
		t.Fatalf("canonicalSignaturePayload() error = %v", errCanonical)
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		t.Errorf("invalid signature for %s %s", req.Method, parsed.Path)
	}
	if req.Headers.Get(headerMirasimClient) != "test-client" {
		t.Errorf("client version = %q", req.Headers.Get(headerMirasimClient))
	}
}

func decryptRelayMetadata(t *testing.T, relayPrivate []byte, method, requestPath, encoded string) map[string]string {
	t.Helper()
	packed, errDecode := base64.RawURLEncoding.DecodeString(encoded)
	if errDecode != nil {
		t.Fatalf("decode x-mirasim-enc: %v", errDecode)
	}
	minimum := curve25519.PointSize + chacha20poly1305.NonceSize + chacha20poly1305.Overhead
	if len(packed) < minimum {
		t.Fatalf("x-mirasim-enc length = %d, want at least %d", len(packed), minimum)
	}
	ephemeralPublic := packed[:curve25519.PointSize]
	nonce := packed[curve25519.PointSize : curve25519.PointSize+chacha20poly1305.NonceSize]
	ciphertext := packed[curve25519.PointSize+chacha20poly1305.NonceSize:]
	sharedSecret, errShared := curve25519.X25519(relayPrivate, ephemeralPublic)
	if errShared != nil {
		t.Fatalf("derive relay shared key: %v", errShared)
	}
	key, errHKDF := hkdf.Key(sha256.New, sharedSecret, ephemeralPublic, sealVersion, chacha20poly1305.KeySize)
	if errHKDF != nil {
		t.Fatalf("derive relay seal key: %v", errHKDF)
	}
	aead, errAEAD := chacha20poly1305.New(key)
	if errAEAD != nil {
		t.Fatal(errAEAD)
	}
	aad := []byte(strings.Join([]string{sealVersion, strings.ToUpper(method), requestPath}, "\n"))
	plaintext, errOpen := aead.Open(nil, nonce, ciphertext, aad)
	if errOpen != nil {
		t.Fatalf("open x-mirasim-enc: %v", errOpen)
	}
	var metadata map[string]string
	if errJSON := json.Unmarshal(plaintext, &metadata); errJSON != nil {
		t.Fatalf("decode sealed metadata: %v", errJSON)
	}
	return metadata
}

func mustRequestPath(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, errParse := url.Parse(rawURL)
	if errParse != nil {
		t.Fatal(errParse)
	}
	return parsed.Path
}

func futureJWT() string {
	return jwtWithExpiry(time.Now().Add(time.Hour))
}

func jwtWithExpiry(expiry time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiry.Unix())))
	return header + "." + payload + ".signature"
}

func TestStatusErrorLimitsDisplayedBody(t *testing.T) {
	body := bytes.Repeat([]byte("x"), maxErrorMessage+100)
	message := NewStatusError(http.StatusBadGateway, body, nil).Error()
	if len(message) > maxErrorMessage+100 || !strings.HasSuffix(message, "...") {
		t.Fatalf("status error was not bounded: length=%d", len(message))
	}
}
