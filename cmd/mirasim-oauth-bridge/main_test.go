package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestBridgeForwardsOnlyMirasimOAuthResources(t *testing.T) {
	var calls atomic.Int32
	var expectedHost string
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
		w.Header().Set("Location", "http://127.0.0.1:18317/v0/resource/plugins/mirasim/oauth/callback?result=complete")
		w.WriteHeader(http.StatusFound)
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
	req, errRequest := http.NewRequest(http.MethodGet, bridge.URL+defaultResourceBase+"/oauth/callback?state=test&access_token=secret", nil)
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
	if calls.Load() != 1 {
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
	if calls.Load() != 1 {
		t.Fatalf("denied requests reached upstream; calls = %d", calls.Load())
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
	for _, path := range []string{
		"/gateway/v0/resource/plugins/mirasim/oauth/start",
		"/gateway/v0/resource/plugins/mirasim/oauth/callback",
	} {
		if _, ok := paths[path]; !ok {
			t.Errorf("missing path %q", path)
		}
	}
	if _, errInvalid := oauthResourcePaths("../management"); errInvalid == nil {
		t.Fatal("invalid resource base was accepted")
	}
}
