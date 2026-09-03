package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestBridgeForwardsOnlyMirasimOAuthResources(t *testing.T) {
	var calls atomic.Int32
	var expectedHost string
	state := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Host != expectedHost {
			t.Errorf("Host = %q, want %q", req.Host, expectedHost)
		}
		for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer", "X-Api-Key"} {
			if value := req.Header.Get(name); value != "" {
				t.Errorf("%s was forwarded", name)
			}
		}
		switch req.URL.Path {
		case defaultResourceBase + "/oauth/start":
			if req.URL.Query().Get("state") != state {
				t.Error("start request lost OAuth state")
			}
			w.WriteHeader(http.StatusOK)
		case defaultResourceBase + "/oauth/callback":
			if req.URL.Query().Get("state") != state {
				t.Error("state-less callback was not bound to the pending loopback session")
			}
			if req.URL.Query().Get("access_token") != "secret" {
				t.Error("callback lost access token")
			}
			w.Header().Set("Location", "http://127.0.0.1:18317/v0/resource/plugins/mirasim/oauth/callback?result=complete")
			w.WriteHeader(http.StatusFound)
		default:
			t.Errorf("unexpected upstream path %q", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	target, errParse := url.Parse(upstream.URL)
	if errParse != nil {
		t.Fatal(errParse)
	}
	expectedHost = target.Host
	paths, errPaths := oauthResourcePaths(defaultResourceBase)
	if errPaths != nil {
		t.Fatal(errPaths)
	}
	bridge := httptest.NewServer(newBridgeHandler(target, http.DefaultTransport, paths))
	defer bridge.Close()

	client := bridge.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	startResp, errStart := client.Get(bridge.URL + defaultResourceBase + "/oauth/start?state=" + url.QueryEscape(state))
	if errStart != nil {
		t.Fatal(errStart)
	}
	_, _ = io.Copy(io.Discard, startResp.Body)
	_ = startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", startResp.StatusCode)
	}

	req, errRequest := http.NewRequest(http.MethodGet, bridge.URL+defaultResourceBase+"/oauth/callback?access_token=secret&refresh_token=secret", nil)
	if errRequest != nil {
		t.Fatal(errRequest)
	}
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer", "X-Api-Key"} {
		req.Header.Set(name, "must-not-forward")
	}
	resp, errDo := client.Do(req)
	if errDo != nil {
		t.Fatal(errDo)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d", calls.Load())
	}

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodPost, path: defaultResourceBase + "/oauth/start", want: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/v0/management/config", want: http.StatusNotFound},
	} {
		reqDenied, _ := http.NewRequest(tc.method, bridge.URL+tc.path, nil)
		respDenied, errDenied := client.Do(reqDenied)
		if errDenied != nil {
			t.Fatal(errDenied)
		}
		_, _ = io.Copy(io.Discard, respDenied.Body)
		_ = respDenied.Body.Close()
		if respDenied.StatusCode != tc.want {
			t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, respDenied.StatusCode, tc.want)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("denied requests reached upstream; calls = %d", calls.Load())
	}
}

func TestBridgeRejectsStateLessCallbackWithoutPendingSession(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	paths, _ := oauthResourcePaths(defaultResourceBase)
	bridge := httptest.NewServer(newBridgeHandler(target, http.DefaultTransport, paths))
	defer bridge.Close()

	resp, errDo := bridge.Client().Get(bridge.URL + defaultResourceBase + "/oauth/callback?access_token=secret&refresh_token=secret")
	if errDo != nil {
		t.Fatal(errDo)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if calls.Load() != 0 {
		t.Fatal("unbound callback reached upstream")
	}
}

func TestPendingOAuthStateExpiresAndClears(t *testing.T) {
	now := time.Unix(1_788_422_225, 0)
	pending := &pendingOAuthState{now: func() time.Time { return now }}
	state := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if !pending.remember(state) {
		t.Fatal("valid state was rejected")
	}
	if got, ok := pending.current(); !ok || got != state {
		t.Fatal("pending state was not retained")
	}
	pending.clear("different-state")
	if _, ok := pending.current(); !ok {
		t.Fatal("different state cleared the pending session")
	}
	now = now.Add(pendingStateTTL)
	if _, ok := pending.current(); ok {
		t.Fatal("expired state was retained")
	}
}

func TestValidateListenAddress(t *testing.T) {
	for _, value := range []string{"127.0.0.1:18317", "localhost:18317", "[::1]:18317"} {
		if err := validateListenAddress(value); err != nil {
			t.Errorf("validateListenAddress(%q): %v", value, err)
		}
	}
	for _, value := range []string{"0.0.0.0:18317", "192.168.1.2:18317", "missing-port"} {
		if err := validateListenAddress(value); err == nil {
			t.Errorf("validateListenAddress(%q) succeeded", value)
		}
	}
}

func TestParseDialAddress(t *testing.T) {
	for input, want := range map[string]string{
		"":                     "",
		" 69.63.194.69:58317 ": "69.63.194.69:58317",
		"[2001:db8::1]:443":    "[2001:db8::1]:443",
	} {
		got, err := parseDialAddress(input)
		if err != nil {
			t.Errorf("parseDialAddress(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseDialAddress(%q) = %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"cpa.example:443", "69.63.194.69", "69.63.194.69:0", "69.63.194.69:65536"} {
		if _, err := parseDialAddress(input); err == nil {
			t.Errorf("parseDialAddress(%q) succeeded", input)
		}
	}
}

func TestPinnedTransportBypassesDNSWithoutChangingRequestHost(t *testing.T) {
	requestHost := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestHost <- req.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, errParse := url.Parse(upstream.URL)
	if errParse != nil {
		t.Fatal(errParse)
	}

	transport, dialAddress, errTransport := newBridgeTransport(upstreamURL.Host)
	if errTransport != nil {
		t.Fatal(errTransport)
	}
	if dialAddress != upstreamURL.Host {
		t.Fatalf("dial address = %q, want %q", dialAddress, upstreamURL.Host)
	}
	client := &http.Client{Transport: transport}
	resp, errDo := client.Get("http://does-not-resolve.invalid/probe")
	if errDo != nil {
		t.Fatal(errDo)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := <-requestHost; got != "does-not-resolve.invalid" {
		t.Fatalf("request Host = %q", got)
	}
}

func TestParseUpstreamRequiresHTTPSOutsideLoopback(t *testing.T) {
	for _, value := range []string{"https://cpa.example", "http://127.0.0.1:8317"} {
		if _, err := parseUpstream(value); err != nil {
			t.Errorf("parseUpstream(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "http://cpa.example", "ftp://cpa.example", "https://user:pass@cpa.example"} {
		if _, err := parseUpstream(value); err == nil {
			t.Errorf("parseUpstream(%q) succeeded", value)
		}
	}
}

func TestOAuthResourcePaths(t *testing.T) {
	paths, err := oauthResourcePaths("/gateway/v0/resource/plugins/mirasim/")
	if err != nil {
		t.Fatal(err)
	}
	if paths.start != "/gateway/v0/resource/plugins/mirasim/oauth/start" {
		t.Errorf("start path = %q", paths.start)
	}
	if paths.callback != "/gateway/v0/resource/plugins/mirasim/oauth/callback" {
		t.Errorf("callback path = %q", paths.callback)
	}
	if _, errInvalid := oauthResourcePaths("../management"); errInvalid == nil {
		t.Fatal("invalid resource base was accepted")
	}
}
