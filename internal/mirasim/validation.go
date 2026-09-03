package mirasim

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

const credentialValidationTimeout = 60 * time.Second

// ValidateRemote proves that the OAuth token and generated device identity can
// mint a ticket and read the authenticated model catalog before CPA persists
// the login. CLI login has no host HTTP callback, so it uses an equivalent
// short-lived proxy-aware client for this one validation request.
func (c *Client) ValidateRemote(ctx context.Context, hostClient pluginapi.HostHTTPClient, proxyURL string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if errProxy := c.SetAuthProxy(proxyURL); errProxy != nil {
		return errProxy
	}
	closeClient := func() {}
	if hostClient == nil {
		var errClient error
		hostClient, closeClient, errClient = newValidationHTTPClient(proxyURL)
		if errClient != nil {
			return errClient
		}
	}
	defer closeClient()

	validationCtx, cancelValidation := context.WithTimeout(ctx, credentialValidationTimeout)
	defer cancelValidation()
	if _, errModels := c.ListModels(validationCtx, hostClient); errModels != nil {
		if status, ok := errModels.(interface{ StatusCode() int }); ok && status.StatusCode() > 0 {
			return fmt.Errorf("validate Mirasim OAuth credentials: upstream returned HTTP %d", status.StatusCode())
		}
		return fmt.Errorf("validate Mirasim OAuth credentials: %w", errModels)
	}
	return nil
}

type validationHTTPClient struct {
	client *http.Client
}

func newValidationHTTPClient(proxyURL string) (pluginapi.HostHTTPClient, func(), error) {
	transport, _, errTransport := proxyutil.BuildHTTPTransport(proxyURL)
	if errTransport != nil {
		return nil, nil, fmt.Errorf("configure Mirasim validation proxy: %w", errTransport)
	}
	client := &http.Client{Timeout: credentialValidationTimeout}
	closeClient := func() {}
	if transport != nil {
		client.Transport = transport
		closeClient = transport.CloseIdleConnections
	}
	return validationHTTPClient{client: client}, closeClient, nil
}

func (c validationHTTPClient) Do(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	request, errRequest := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if errRequest != nil {
		return pluginapi.HTTPResponse{}, errRequest
	}
	request.Header = cloneHeader(req.Headers)
	response, errDo := c.client.Do(request)
	if errDo != nil {
		return pluginapi.HTTPResponse{}, errDo
	}
	defer func() { _ = response.Body.Close() }()
	body, errRead := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	if errRead != nil {
		return pluginapi.HTTPResponse{}, errRead
	}
	if len(body) > maxErrorBody {
		return pluginapi.HTTPResponse{}, fmt.Errorf("Mirasim validation response exceeds %d bytes", maxErrorBody)
	}
	return pluginapi.HTTPResponse{StatusCode: response.StatusCode, Headers: cloneHeader(response.Header), Body: body}, nil
}

func (validationHTTPClient) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return pluginapi.HTTPStreamResponse{}, fmt.Errorf("Mirasim credential validation does not support streaming")
}
