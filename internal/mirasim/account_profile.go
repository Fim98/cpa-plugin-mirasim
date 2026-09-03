package mirasim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

type accountProfile struct {
	Email           string
	Plan            string
	PlanExpiresAt   *int64
	PlanExpiryKnown bool
}

func (c *Client) fetchAccountProfileLocked(ctx context.Context) (accountProfile, error) {
	request, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, c.storage.AdminURL+"/auth/me", nil)
	if errRequest != nil {
		return accountProfile{}, fmt.Errorf("create Mirasim profile request: %w", errRequest)
	}
	request.Header.Set("Authorization", "Bearer "+c.accessToken)
	client, closeClient, errClient := newPrivateHTTPClient(c.authProxyURL, profileTimeout)
	if errClient != nil {
		return accountProfile{}, errClient
	}
	defer closeClient()
	response, errDo := client.Do(request)
	if errDo != nil {
		return accountProfile{}, fmt.Errorf("read Mirasim account profile: %w", errDo)
	}
	defer func() { _ = response.Body.Close() }()
	body, errRead := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	if errRead != nil {
		return accountProfile{}, fmt.Errorf("read Mirasim account profile: %w", errRead)
	}
	if len(body) > maxErrorBody {
		return accountProfile{}, fmt.Errorf("Mirasim account profile exceeds %d bytes", maxErrorBody)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return accountProfile{}, fmt.Errorf("Mirasim account profile returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Email   string          `json:"email"`
		Plan    string          `json:"plan"`
		PlanExp json.RawMessage `json:"plan_exp"`
	}
	if errDecode := json.Unmarshal(body, &payload); errDecode != nil {
		return accountProfile{}, fmt.Errorf("decode Mirasim account profile: %w", errDecode)
	}
	profile := accountProfile{
		Email: safeProfileValue(payload.Email, 320),
		Plan:  safeProfileValue(payload.Plan, 128),
	}
	if len(payload.PlanExp) > 0 {
		profile.PlanExpiryKnown = true
		if !bytes.Equal(bytes.TrimSpace(payload.PlanExp), []byte("null")) {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(payload.PlanExp))
			decoder.UseNumber()
			if errNumber := decoder.Decode(&value); errNumber == nil {
				if number, ok := value.(json.Number); ok {
					if expiresAt, errInt := number.Int64(); errInt == nil && expiresAt > 0 {
						profile.PlanExpiresAt = &expiresAt
					}
				}
			}
		}
	}
	return profile, nil
}

func safeProfileValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > limit || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func newPrivateHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, func(), error) {
	transport, _, errTransport := proxyutil.BuildHTTPTransport(proxyURL)
	if errTransport != nil {
		return nil, nil, fmt.Errorf("configure Mirasim auth proxy: %w", errTransport)
	}
	client := &http.Client{Timeout: timeout}
	closeClient := func() {}
	if transport != nil {
		client.Transport = transport
		closeClient = transport.CloseIdleConnections
	}
	return client, closeClient, nil
}
