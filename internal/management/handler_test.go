package management

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

type fakeHostServices struct {
	files []pluginapi.HostAuthFileEntry
}

type fakeOAuthResources struct {
	basePath string
}

func (f *fakeOAuthResources) ConfigureOAuthResourceBasePath(value string) { f.basePath = value }
func (*fakeOAuthResources) HandleOAuthResource(context.Context, pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	return pluginapi.ManagementResponse{StatusCode: http.StatusTeapot}, nil
}

func (f fakeHostServices) ListAuth(context.Context) ([]pluginapi.HostAuthFileEntry, error) {
	return f.files, nil
}

func TestRegisterManagementDeclaresOAuthResources(t *testing.T) {
	oauth := &fakeOAuthResources{}
	handler := New(pluginconfig.Defaults(), mirasim.NewPool(), oauth)
	resp, errRegister := handler.RegisterManagement(context.Background(), pluginapi.ManagementRegistrationRequest{ResourceBasePath: "/v0/resource/plugins/mirasim"})
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	if oauth.basePath != "/v0/resource/plugins/mirasim" {
		t.Fatalf("configured resource base = %q", oauth.basePath)
	}
	if len(resp.Resources) != 2 || resp.Resources[0].Path != "/oauth/start" || resp.Resources[1].Path != "/oauth/callback" {
		t.Fatalf("resources = %#v", resp.Resources)
	}
	for _, resource := range resp.Resources {
		if resource.Handler == nil {
			t.Fatalf("resource handler is nil: %#v", resource)
		}
	}
}

func (fakeHostServices) GetAuth(context.Context, string) (pluginapi.HostAuthGetResponse, error) {
	return pluginapi.HostAuthGetResponse{}, nil
}

func (fakeHostServices) HTTPClient() pluginapi.HostHTTPClient { return nil }

func TestRegisterManagementDeclaresReadOnlyQuotaRoute(t *testing.T) {
	handler := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errRegister := handler.RegisterManagement(context.Background(), pluginapi.ManagementRegistrationRequest{})
	if errRegister != nil {
		t.Fatalf("RegisterManagement() error = %v", errRegister)
	}
	if len(resp.Routes) != 1 || resp.Routes[0].Method != http.MethodGet || resp.Routes[0].Path != QuotaRoute {
		t.Fatalf("routes = %#v", resp.Routes)
	}
	if resp.Routes[0].Handler == nil {
		t.Fatal("quota route handler is nil")
	}
}

func TestResolveSingleAuthFiltersMirasimCredentials(t *testing.T) {
	index, errResolve := resolveSingleAuth(context.Background(), fakeHostServices{files: []pluginapi.HostAuthFileEntry{
		{AuthIndex: "other:1", Provider: "other"},
		{AuthIndex: "mirasim:1", Provider: "mirasim"},
	}})
	if errResolve != nil || index != "mirasim:1" {
		t.Fatalf("index = %q, error = %v", index, errResolve)
	}

	_, errResolve = resolveSingleAuth(context.Background(), fakeHostServices{files: []pluginapi.HostAuthFileEntry{
		{AuthIndex: "mirasim:1", Provider: "mirasim"},
		{AuthIndex: "mirasim:2", Type: "mirasim"},
	}})
	if errResolve == nil || !strings.Contains(errResolve.Error(), "multiple") {
		t.Fatalf("multiple-auth error = %v", errResolve)
	}
}

func TestQuotaRouteRejectsWritesBeforeUsingHostCallbacks(t *testing.T) {
	handler := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errHandle := handler.HandleWithHost(context.Background(), pluginapi.ManagementRequest{Method: http.MethodPost}, nil)
	if errHandle != nil {
		t.Fatalf("HandleWithHost() error = %v", errHandle)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.Body)
	}
}
