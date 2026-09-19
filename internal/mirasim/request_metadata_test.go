package mirasim

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"net/http"
	"net/url"
	"testing"
)

func TestRelayOptionsAreSignedAndCannotBeOverridden(t *testing.T) {
	storage, pub, key := newTestStorage(t, agentAccountJWT("acct_42"))
	storage.AccountID = "usr_local"
	off := false
	client := NewPool(RelayOptions{Collect: &off, Locale: "zh-CN"}).Client(storage)
	var sessions []string
	host := fakeHostClient{do: func(_ context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		u, _ := url.Parse(r.URL)
		if u.Path == sessionPath {
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"ticket":"t","expiresIn":900}`)}, nil
		}
		assertSealedRelayRequest(t, pub, key, r, "t")
		m := decryptRelayMetadata(t, key, r.Method, u.Path, r.Headers.Get(headerMirasimEncryptedMetadata))
		if m["x-mirasim-collect"] != "off" || m["x-mirasim-locale"] != "zh-CN" || m["x-mirasim-account"] != "acct_42" || m["x-mirasim-turn"] != "turn-a" {
			t.Fatalf("metadata=%+v", m)
		}
		if m[headerMirasimCall] != "" {
			t.Fatal("obsolete per-call metadata survived")
		}
		sessions = append(sessions, m[headerMirasimSession])
		return pluginapi.HTTPResponse{StatusCode: 200}, nil
	}}
	for _, session := range []string{"one", "one", "two"} {
		ctx := WithRequestIdentity(context.Background(), map[string]any{"execution_session_id": session, "mirasim_turn_id": "turn-a"})
		_, err := client.Do(ctx, host, "POST", "/v1/messages", nil, http.Header{"X-Mirasim-Collect": []string{"on"}, "X-Mirasim-Account": []string{"forged"}}, []byte("{}"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if sessions[0] != sessions[1] || sessions[0] == sessions[2] {
		t.Fatal("session association was lost")
	}
	if safeMetadata("bad\nheader") != "" {
		t.Fatal("control character accepted")
	}
}

func TestRelayOmitsTheAccountHeaderWhenTheTokenNamesNoSubAccount(t *testing.T) {
	// A plain user token carries sub but no account_id. The official client
	// sends nothing in that case, and the user ID is not a stand-in for it.
	storage, pub, key := newTestStorage(t, futureJWT())
	storage.AccountID = "usr_local"
	client := NewPool().Client(storage)
	host := fakeHostClient{do: func(_ context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		u, _ := url.Parse(r.URL)
		if u.Path == sessionPath {
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"ticket":"t","expiresIn":900}`)}, nil
		}
		assertSealedRelayRequest(t, pub, key, r, "t")
		if m := decryptRelayMetadata(t, key, r.Method, u.Path, r.Headers.Get(headerMirasimEncryptedMetadata)); m["x-mirasim-account"] != "" {
			t.Fatalf("a local account identity was reported upstream: %+v", m)
		}
		return pluginapi.HTTPResponse{StatusCode: 200}, nil
	}}
	if _, err := client.Do(context.Background(), host, "POST", "/v1/messages", nil, nil, []byte("{}")); err != nil {
		t.Fatal(err)
	}
}
