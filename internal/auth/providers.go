package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

var providerSlug = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type loginProvider struct{ ID, Label string }

// Discovery is public and unauthenticated. Do not reuse caller browser headers
// or OAuth credentials, and never fall back to providers explicitly disabled.
func discoverLoginProviders(ctx context.Context, adminURL, proxyURL string) ([]loginProvider, error) {
	_, err := buildMirasimOAuthURL(adminURL, "discovery", "http://127.0.0.1", "")
	if err != nil {
		return nil, err
	}
	// The configured origin passed the same validation as login URLs.
	transport, _, err := proxyutil.BuildHTTPTransport(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure Mirasim OAuth discovery proxy")
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if transport != nil {
		client.Transport = transport
		defer transport.CloseIdleConnections()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(adminURL, "/")+"/auth/oauth/providers", nil)
	if err != nil {
		return nil, fmt.Errorf("invalid OAuth discovery URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Mirasim sign-in providers are unreachable; retry login")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Mirasim sign-in provider discovery returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, fmt.Errorf("invalid Mirasim sign-in provider response")
	}
	var payload struct {
		Providers []string `json:"providers"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil, fmt.Errorf("invalid Mirasim sign-in provider response")
	}
	providers := []loginProvider{}
	seen := map[string]bool{}
	for _, id := range payload.Providers {
		id = strings.ToLower(strings.TrimSpace(id))
		if !providerSlug.MatchString(id) || seen[id] {
			continue
		}
		label := id
		switch id {
		case "github":
			label = "GitHub"
		case "google":
			label = "Google"
		}
		providers = append(providers, loginProvider{ID: id, Label: label})
		seen[id] = true
	}
	return providers, nil
}

func providerOffered(providers []loginProvider, id string) bool {
	for _, p := range providers {
		if p.ID == id {
			return true
		}
	}
	return false
}
