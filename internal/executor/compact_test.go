package executor

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
	"github.com/tidwall/gjson"
	"net/http"
	"net/url"
	"testing"
)

func TestCompactPreservesOpaqueHistoryAndJSON(t *testing.T) {
	storage := executorTestStorage(t)
	want := []byte(`{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":42}}`)
	calls := 0
	host := executorHostClient{do: func(_ context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		u, _ := url.Parse(r.URL)
		if u.Path == "/v1/device/session" {
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"ticket":"t","expiresIn":900}`)}, nil
		}
		calls++
		if u.Path != compactPath || u.Query().Get("beta") != "true" || r.Headers.Get("Accept") != "application/json" {
			t.Fatalf("request=%+v", r)
		}
		if gjson.GetBytes(r.Body, "stream").Exists() || gjson.GetBytes(r.Body, "model").String() != "gpt-6-astra" || gjson.GetBytes(r.Body, "input.0.encrypted_content").String() != "opaque" {
			t.Fatalf("body=%s", r.Body)
		}
		return pluginapi.HTTPResponse{StatusCode: 200, Body: want, Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
	}}
	e := New(pluginconfig.Defaults(), mirasim.NewPool())
	for _, format := range []string{"codex", "openai-response"} {
		r, err := e.Execute(context.Background(), pluginapi.ExecutorRequest{Alt: "responses/compact", Model: "mirasim/gpt-6-astra", SourceFormat: format, Format: format, Payload: []byte(`{"model":"gpt-6-astra","stream":false,"input":[{"type":"compaction","encrypted_content":"opaque"}]}`), Query: url.Values{"beta": []string{"true"}}, StorageJSON: storage.JSON(), HTTPClient: host})
		if err != nil || string(r.Payload) != string(want) {
			t.Fatalf("response=%s err=%v", r.Payload, err)
		}
	}
	rawResponse, rawErr := e.HttpRequest(context.Background(), pluginapi.ExecutorHTTPRequest{
		URL:         "https://chatgpt.com/backend-api/codex/responses/compact?beta=true",
		Method:      http.MethodPost,
		Body:        []byte(`{"model":"mirasim/gpt-6-astra","stream":false,"input":[{"type":"compaction","encrypted_content":"opaque"}]}`),
		StorageJSON: storage.JSON(), HTTPClient: host,
	})
	if rawErr != nil || string(rawResponse.Body) != string(want) {
		t.Fatalf("raw compact error=%v response=%s", rawErr, rawResponse.Body)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
	if normalizeRelayPath("/backend-api/codex/responses/compact") != compactPath {
		t.Fatal("alias missing")
	}
	if _, err := e.ExecuteStream(context.Background(), pluginapi.ExecutorRequest{Alt: "responses/compact"}); err == nil {
		t.Fatal("stream accepted")
	}
	for _, body := range []string{`{"stream":true}`, `null`} {
		if _, err := compactBody([]byte(body), "gpt-6-astra"); err == nil {
			t.Fatal("invalid body accepted")
		}
	}
}
