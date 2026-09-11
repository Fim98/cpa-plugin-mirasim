package auth

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDiscoveredProvidersControlChooserAndRedirect(t *testing.T) {
	body := `{"providers":["gitlab","google","google","../bad","<script>"]}`
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/oauth/providers" || r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Fatalf("bad request %s", r.URL)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	settings := pluginconfig.Defaults()
	settings.AdminURL = server.URL
	p := New(settings, mirasim.NewPool())
	p.ConfigureOAuthResourceBasePath("/v0/resource/plugins/mirasim")
	start, err := p.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{BaseURL: "http://127.0.0.1:8317"})
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"state": []string{start.State}}
	request := func() pluginapi.ManagementResponse {
		r, e := p.HandleOAuthResource(context.Background(), pluginapi.ManagementRequest{Path: "/oauth/start", Query: q})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r := request()
	if r.StatusCode != 200 || !strings.Contains(string(r.Body), "gitlab") || strings.Contains(string(r.Body), "GitHub") || strings.Contains(string(r.Body), "<script>") {
		t.Fatalf("chooser=%s", r.Body)
	}
	q.Set("provider", "gitlab")
	if request().StatusCode != 302 {
		t.Fatal("discovered provider rejected")
	}
	body = `{"providers":["google"]}`
	if request().StatusCode != 400 {
		t.Fatal("disabled provider accepted")
	}
	q.Del("provider")
	body = `{"providers":[]}`
	if request().StatusCode != 503 {
		t.Fatal("empty discovery used fallback")
	}
	status = 500
	if request().StatusCode != 503 {
		t.Fatal("failed discovery used fallback")
	}
}
