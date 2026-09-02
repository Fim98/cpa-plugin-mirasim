package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

const QuotaRoute = "/mirasim/quota"

type HostServices interface {
	ListAuth(context.Context) ([]pluginapi.HostAuthFileEntry, error)
	GetAuth(context.Context, string) (pluginapi.HostAuthGetResponse, error)
	HTTPClient() pluginapi.HostHTTPClient
}

type OAuthResources interface {
	ConfigureOAuthResourceBasePath(string)
	HandleOAuthResource(context.Context, pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error)
}

type Handler struct {
	settings pluginconfig.Settings
	pool     *mirasim.Pool
	oauth    OAuthResources
}

func New(settings pluginconfig.Settings, pool *mirasim.Pool, oauth ...OAuthResources) *Handler {
	handler := &Handler{settings: settings, pool: pool}
	if len(oauth) > 0 {
		handler.oauth = oauth[0]
	}
	return handler
}

func (h *Handler) RegisterManagement(_ context.Context, req pluginapi.ManagementRegistrationRequest) (pluginapi.ManagementRegistrationResponse, error) {
	response := pluginapi.ManagementRegistrationResponse{Routes: []pluginapi.ManagementRoute{{
		Method:      http.MethodGet,
		Path:        QuotaRoute,
		Description: "Refreshes GET /v1/models and returns Mirasim rate-limit response-header signals.",
		Handler:     h,
	}}}
	if h.oauth != nil {
		h.oauth.ConfigureOAuthResourceBasePath(req.ResourceBasePath)
		response.Resources = []pluginapi.ResourceRoute{
			{Path: "/oauth/start", Description: "Starts a Mirasim browser OAuth login.", Handler: h},
			{Path: "/oauth/callback", Description: "Receives a Mirasim browser OAuth callback.", Handler: h},
		}
	}
	return response, nil
}

func (h *Handler) HandleManagement(ctx context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	if h.oauth != nil && isOAuthResource(req.Path) {
		return h.oauth.HandleOAuthResource(ctx, req)
	}
	return jsonResponse(http.StatusServiceUnavailable, map[string]any{
		"error": "live host callbacks are unavailable",
	}), nil
}

func (h *Handler) HandleWithHost(ctx context.Context, req pluginapi.ManagementRequest, host HostServices) (pluginapi.ManagementResponse, error) {
	if h.oauth != nil && isOAuthResource(req.Path) {
		return h.oauth.HandleOAuthResource(ctx, req)
	}
	if !strings.EqualFold(req.Method, http.MethodGet) {
		return jsonResponse(http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}), nil
	}
	if host == nil || host.HTTPClient() == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "host callbacks are unavailable"}), nil
	}
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" {
		resolved, errResolve := resolveSingleAuth(ctx, host)
		if errResolve != nil {
			return jsonResponse(http.StatusBadRequest, map[string]any{"error": errResolve.Error()}), nil
		}
		authIndex = resolved
	}
	auth, errGet := host.GetAuth(ctx, authIndex)
	if errGet != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"error": errGet.Error()}), nil
	}
	storage, errParse := credentials.Parse(auth.JSON, h.settings)
	if errParse != nil {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": errParse.Error()}), nil
	}
	if storage == nil {
		return jsonResponse(http.StatusBadRequest, map[string]any{"error": "selected auth is not a Mirasim credential"}), nil
	}
	catalog, errCatalog := h.pool.Client(*storage).ListModels(ctx, host.HTTPClient())
	if errCatalog != nil {
		return jsonResponse(statusFromError(errCatalog), map[string]any{
			"auth_index": authIndex,
			"error":      errCatalog.Error(),
		}), nil
	}
	return jsonResponse(http.StatusOK, map[string]any{
		"auth_index":  authIndex,
		"model_count": len(catalog.Models),
		"quota":       catalog.Quota,
		"note":        "These are rate-limit signals from GET /v1/models response headers, not billing usage.",
	}), nil
}

func isOAuthResource(path string) bool {
	return strings.HasSuffix(path, "/oauth/start") || strings.HasSuffix(path, "/oauth/callback")
}

func resolveSingleAuth(ctx context.Context, host HostServices) (string, error) {
	files, errList := host.ListAuth(ctx)
	if errList != nil {
		return "", errList
	}
	indexes := make([]string, 0)
	for _, file := range files {
		if !strings.EqualFold(strings.TrimSpace(file.Provider), credentials.Provider) && !strings.EqualFold(strings.TrimSpace(file.Type), credentials.Provider) {
			continue
		}
		index := strings.TrimSpace(file.AuthIndex)
		if index == "" {
			index = strings.TrimSpace(file.ID)
		}
		if index != "" {
			indexes = append(indexes, index)
		}
	}
	switch len(indexes) {
	case 0:
		return "", fmt.Errorf("no Mirasim auth is loaded")
	case 1:
		return indexes[0], nil
	default:
		return "", fmt.Errorf("multiple Mirasim auths are loaded; specify auth_index")
	}
}

func statusFromError(err error) int {
	if provider, ok := err.(interface{ StatusCode() int }); ok {
		if status := provider.StatusCode(); status >= 400 && status <= 599 {
			return status
		}
	}
	return http.StatusBadGateway
}

func jsonResponse(status int, value any) pluginapi.ManagementResponse {
	body, errMarshal := json.MarshalIndent(value, "", "  ")
	if errMarshal != nil {
		body = []byte(`{"error":"encode management response"}`)
		status = http.StatusInternalServerError
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type":  []string{"application/json; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
		},
		Body: body,
	}
}

var _ pluginapi.ManagementAPI = (*Handler)(nil)
var _ pluginapi.ManagementHandler = (*Handler)(nil)
