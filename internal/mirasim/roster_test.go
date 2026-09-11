package mirasim

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestRosterCacheFallbackAndIsolation(t *testing.T) {
	storage, pub, relayKey := newTestStorage(t, futureJWT())
	client := NewClient(storage)
	now := time.Now()
	client.now = func() time.Time { return now }
	status, calls := 200, 0
	host := fakeHostClient{do: func(_ context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		u, _ := url.Parse(r.URL)
		if u.Path == sessionPath {
			return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"ticket":"t","expiresIn":900}`)}, nil
		}
		if u.Path != rosterPath || r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s", u.Path)
		}
		calls++
		assertSealedRelayRequest(t, pub, relayKey, r, "t")
		return pluginapi.HTTPResponse{StatusCode: status, Body: []byte(`{"version":"v2","agents":{"codex":[{"id":"gpt-6-astra","contextWindow":1050000,"maxOutput":128000,"effort":["high","max"]},{"id":"bad","contextWindow":1}],"claude":[{"id":"claude-bad","contextWindow":0}]}}`)}, nil
	}}
	first := client.ModelRoster(context.Background(), host)
	if first.Version != "v2" || len(first.Agents["codex"]) != 1 {
		t.Fatalf("roster=%+v", first)
	}
	first.Agents["codex"][0].Effort[0] = "mutated"
	if client.ModelRoster(context.Background(), host).Agents["codex"][0].Effort[0] != "high" || calls != 1 {
		t.Fatal("cache aliased or missed")
	}
	now = now.Add(11 * time.Minute)
	status = 404
	if client.ModelRoster(context.Background(), host).Version != "v2" || calls != 2 {
		t.Fatal("fallback failed")
	}
	fresh := NewClient(storage)
	if fresh.ModelRoster(context.Background(), host).Version != "" {
		t.Fatal("cached roster leaked to another client")
	}
}

func TestPoolSeparatesAccountsEvenWhenDeviceKeyIsReused(t *testing.T) {
	a, _, _ := newTestStorage(t, futureJWT())
	a.AccountID = "account-a"
	b := a
	b.AccountID = "account-b"
	pool := NewPool()
	first, second := pool.Client(a), pool.Client(b)
	if first == second {
		t.Fatal("different accounts share mutable relay state")
	}
	first.roster = ModelRoster{Version: "a"}
	if second.roster.Version != "" {
		t.Fatal("roster crossed account boundary")
	}
	if pool.Client(a) != first {
		t.Fatal("same account did not retain client")
	}
}
