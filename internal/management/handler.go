// Package management registers the plugin's browser-facing OAuth resources
// through CPA's Management API. Limits are reported by the quota provider
// capability, so nothing here needs host auth callbacks or a route of its own.
package management

import (
	"context"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type OAuthResources interface {
	ConfigureOAuthResourceBasePath(string)
	HandleOAuthResource(context.Context, pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error)
}

type Handler struct {
	oauth OAuthResources
}

func New(oauth OAuthResources) *Handler {
	return &Handler{oauth: oauth}
}

func (h *Handler) RegisterManagement(_ context.Context, req pluginapi.ManagementRegistrationRequest) (pluginapi.ManagementRegistrationResponse, error) {
	if h.oauth == nil {
		return pluginapi.ManagementRegistrationResponse{}, nil
	}
	h.oauth.ConfigureOAuthResourceBasePath(req.ResourceBasePath)
	return pluginapi.ManagementRegistrationResponse{Resources: []pluginapi.ResourceRoute{
		{Path: "/oauth/start", Description: "Starts a Mirasim browser OAuth login.", Handler: h},
		{Path: "/oauth/callback", Description: "Receives a Mirasim browser OAuth callback.", Handler: h},
	}}, nil
}

func (h *Handler) HandleManagement(ctx context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	if h.oauth != nil && isOAuthResource(req.Path) {
		return h.oauth.HandleOAuthResource(ctx, req)
	}
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusNotFound,
		Headers: http.Header{
			"Content-Type":  []string{"text/plain; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
		},
		Body: []byte("Mirasim resource not found\n"),
	}, nil
}

func isOAuthResource(path string) bool {
	return strings.HasSuffix(path, "/oauth/start") || strings.HasSuffix(path, "/oauth/callback")
}

var _ pluginapi.ManagementAPI = (*Handler)(nil)
var _ pluginapi.ManagementHandler = (*Handler)(nil)
