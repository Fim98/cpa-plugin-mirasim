package mirasim

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRelayOptionsAreSignedAndCannotBeOverridden(t *testing.T) {
	storage, pub, key := newTestStorage(t, agentAccountJWT("acct_42"))
	storage.AccountID = "usr_local"
	off := false
	client := NewPool(RelayOptions{Collect: &off, Locale: "zh-CN"}).Client(storage)
	var sessions []string
	var calls []string
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
		if m[headerMirasimCall] == "" {
			t.Fatalf("relay call was not identified: %+v", m)
		}
		calls = append(calls, m[headerMirasimCall])
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
	// Two calls in one session still have to be told apart.
	if calls[0] == calls[1] || calls[1] == calls[2] || calls[0] == calls[2] {
		t.Fatalf("relay calls shared an identifier: %#v", calls)
	}
	if safeMetadata("bad\nheader") != "" {
		t.Fatal("control character accepted")
	}
}

func TestControlPlaneRoutesReportNothingAboutTheSession(t *testing.T) {
	// Listing models, reading limits and fetching the roster describe the
	// account, not a conversation. The official client signs them with empty
	// metadata and seals nothing, so a configured locale or collection signal
	// has no request to ride along on.
	storage, pub, _ := newTestStorage(t, agentAccountJWT("acct_42"))
	off := false
	client := NewPool(RelayOptions{Collect: &off, Locale: "zh-CN"}).Client(storage)
	host := fakeHostClient{do: func(_ context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		u, _ := url.Parse(r.URL)
		if u.Path == sessionPath {
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"ticket":"t","expiresIn":900}`)}, nil
		}
		assertControlPlaneRequest(t, pub, r, "t")
		switch u.Path {
		case modelsPath:
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"data":[{"id":"claude-sonnet-5"}]}`)}, nil
		case limitsPath:
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"windows":[]}`)}, nil
		default:
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"version":"v2","agents":{"codex":[{"id":"gpt-6-astra","contextWindow":1050000}]}}`)}, nil
		}
	}}
	ctx := WithRequestIdentity(context.Background(), map[string]any{"execution_session_id": "one", "mirasim_turn_id": "turn-a"})
	if _, err := client.ListModels(ctx, host); err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchQuota(ctx, host); err != nil {
		t.Fatal(err)
	}
	if roster := client.ModelRoster(ctx, host); roster.Version != "v2" {
		t.Fatalf("roster = %+v", roster)
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

func TestWireProfileIsAbsentUntilConfigured(t *testing.T) {
	if profile := (RelayOptions{}).wireProfile(); profile != nil {
		t.Fatalf("profile = %#v", profile)
	}
}

func TestLowercaseRelayHeadersImpliesHTTP1(t *testing.T) {
	profile := RelayOptions{LowercaseRelayHeaders: true}.wireProfile()
	if profile == nil || !profile.HTTP1Only {
		t.Fatalf("profile = %#v", profile)
	}
	if len(profile.HeaderProfile) != len(relayHeaderProfile) {
		t.Fatalf("header profile = %#v", profile.HeaderProfile)
	}
	for _, name := range profile.HeaderProfile {
		if name != strings.ToLower(name) {
			t.Fatalf("header profile entry is not lower case: %q", name)
		}
	}
}

func TestHTTP1OnlyDoesNotReorderHeaders(t *testing.T) {
	profile := RelayOptions{HTTP1Only: true}.wireProfile()
	if profile == nil || !profile.HTTP1Only || len(profile.HeaderProfile) != 0 {
		t.Fatalf("profile = %#v", profile)
	}
}

// Every signed header must be spelled in the profile, or the host would leave
// it in Go's canonical form while its neighbours moved to lower case.
func TestHeaderProfileCoversTheSignedHeaders(t *testing.T) {
	present := make(map[string]bool, len(relayHeaderProfile))
	for _, name := range relayHeaderProfile {
		present[name] = true
	}
	for _, name := range []string{
		headerMirasimDevice, headerMirasimTimestamp, headerMirasimNonce, headerMirasimSignature,
		headerMirasimClient, headerMirasimEncryptedMetadata, headerMirasimSession,
		headerMirasimAgent, headerMirasimCall, quotaProbeHeader,
	} {
		if !present[name] {
			t.Fatalf("header %q is missing from the wire profile", name)
		}
	}
}
