package management

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type fakeOAuthResources struct {
	basePath string
}

func (f *fakeOAuthResources) ConfigureOAuthResourceBasePath(value string) { f.basePath = value }
func (*fakeOAuthResources) HandleOAuthResource(context.Context, pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	return pluginapi.ManagementResponse{StatusCode: http.StatusTeapot}, nil
}

func TestRegisterManagementDeclaresOAuthResources(t *testing.T) {
	oauth := &fakeOAuthResources{}
	handler := New(oauth)
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

// Quota moved to the host's quota provider, so the plugin must no longer claim
// an authenticated Management API route of its own.
func TestRegisterManagementDeclaresNoManagementRoutes(t *testing.T) {
	resp, errRegister := New(&fakeOAuthResources{}).RegisterManagement(context.Background(), pluginapi.ManagementRegistrationRequest{})
	if errRegister != nil {
		t.Fatalf("RegisterManagement() error = %v", errRegister)
	}
	if len(resp.Routes) != 0 {
		t.Fatalf("routes = %#v", resp.Routes)
	}
}

func TestHandleManagementRoutesOAuthResourcesOnly(t *testing.T) {
	handler := New(&fakeOAuthResources{})
	resp, errHandle := handler.HandleManagement(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/mirasim/oauth/start",
	})
	if errHandle != nil {
		t.Fatalf("HandleManagement() error = %v", errHandle)
	}
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("oauth status = %d", resp.StatusCode)
	}

	resp, errHandle = handler.HandleManagement(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/mirasim/quota",
	})
	if errHandle != nil {
		t.Fatalf("HandleManagement() error = %v", errHandle)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("retired quota route status = %d", resp.StatusCode)
	}
}
