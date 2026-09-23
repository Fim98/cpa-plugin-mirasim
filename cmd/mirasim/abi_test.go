package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestABIRegisterReportsCapabilities(t *testing.T) {
	defer MirasimPluginShutdown()
	raw, errRegister := handleABIMethod(context.Background(), pluginabi.MethodPluginRegister, []byte(`{"config_yaml":"cGx1Z2luczoge30K"}`))
	if errRegister != nil {
		t.Fatalf("register error = %v", errRegister)
	}
	var envelope pluginabi.Envelope
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil {
		t.Fatalf("decode register envelope: %v", errDecode)
	}
	if !envelope.OK {
		t.Fatalf("register envelope = %s", raw)
	}
	var registration abiRegistration
	if errDecode := json.Unmarshal(envelope.Result, &registration); errDecode != nil {
		t.Fatalf("decode registration: %v", errDecode)
	}
	if registration.SchemaVersion != pluginabi.SchemaVersion || !registration.Capabilities.Executor || !registration.Capabilities.ThinkingApplier || !registration.Capabilities.QuotaProvider {
		t.Fatalf("registration = %#v", registration)
	}
	// The plugin registers no HTTP routes at all, so the host must never mount
	// anything for it under the unauthenticated static-asset prefix.
	if registration.Capabilities.ManagementAPI {
		t.Fatalf("management_api = true, want false: %#v", registration.Capabilities)
	}

	raw, errThinking := handleABIMethod(context.Background(), pluginabi.MethodThinkingApply, []byte(`{"model":{"ID":"gpt-5.6-sol"},"config":{"Mode":"level","Level":"high"},"body":"e30="}`))
	if errThinking != nil {
		t.Fatalf("thinking apply error = %v", errThinking)
	}
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil || !envelope.OK {
		t.Fatalf("thinking envelope = %s, error = %v", raw, errDecode)
	}

	raw, errModels := handleABIMethod(context.Background(), pluginabi.MethodModelStatic, []byte(`{}`))
	if errModels != nil {
		t.Fatalf("static models error = %v", errModels)
	}
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil || !envelope.OK {
		t.Fatalf("static models envelope = %s, error = %v", raw, errDecode)
	}
	var modelResponse pluginapi.ModelResponse
	if errDecode := json.Unmarshal(envelope.Result, &modelResponse); errDecode != nil {
		t.Fatalf("decode static models: %v", errDecode)
	}
	// Models are bound to an OAuth credential, so the static path publishes
	// none of them and model.for_auth carries the catalog instead.
	if modelResponse.Provider != "mirasim" || len(modelResponse.Models) != 0 {
		t.Fatalf("static models = %#v", modelResponse)
	}
}

func TestABIQuotaProviderAnswersDescribeAndIdentifier(t *testing.T) {
	defer MirasimPluginShutdown()
	if _, errRegister := handleABIMethod(context.Background(), pluginabi.MethodPluginRegister, []byte(`{}`)); errRegister != nil {
		t.Fatal(errRegister)
	}

	var envelope pluginabi.Envelope
	raw, errIdentifier := handleABIMethod(context.Background(), pluginabi.MethodQuotaIdentifier, nil)
	if errIdentifier != nil {
		t.Fatalf("quota identifier error = %v", errIdentifier)
	}
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil || !envelope.OK {
		t.Fatalf("quota identifier envelope = %s, error = %v", raw, errDecode)
	}
	var identifier abiIdentifierResponse
	if errDecode := json.Unmarshal(envelope.Result, &identifier); errDecode != nil || identifier.Identifier != "mirasim" {
		t.Fatalf("identifier = %#v, error = %v", identifier, errDecode)
	}

	raw, errDescribe := handleABIMethod(context.Background(), pluginabi.MethodQuotaDescribe, []byte(`{}`))
	if errDescribe != nil {
		t.Fatalf("quota describe error = %v", errDescribe)
	}
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil || !envelope.OK {
		t.Fatalf("quota describe envelope = %s, error = %v", raw, errDecode)
	}
	var describe pluginapi.QuotaDescribeResponse
	if errDecode := json.Unmarshal(envelope.Result, &describe); errDecode != nil {
		t.Fatalf("decode quota describe: %v", errDecode)
	}
	if describe.SupportsReset || len(describe.SupportedProviders) != 1 || describe.SupportedProviders[0] != "mirasim" {
		t.Fatalf("describe = %#v", describe)
	}

	// Reset must answer over the ABI instead of failing the call, so the page
	// can say the account has no reset route.
	raw, errReset := handleABIMethod(context.Background(), pluginabi.MethodQuotaReset, []byte(`{}`))
	if errReset != nil {
		t.Fatalf("quota reset error = %v", errReset)
	}
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil || !envelope.OK {
		t.Fatalf("quota reset envelope = %s, error = %v", raw, errDecode)
	}
	var reset pluginapi.QuotaResetResponse
	if errDecode := json.Unmarshal(envelope.Result, &reset); errDecode != nil || reset.Success {
		t.Fatalf("reset = %#v, error = %v", reset, errDecode)
	}
}

func TestABIUnknownMethodReturnsErrorEnvelope(t *testing.T) {
	defer MirasimPluginShutdown()
	if _, errRegister := handleABIMethod(context.Background(), pluginabi.MethodPluginRegister, []byte(`{}`)); errRegister != nil {
		t.Fatal(errRegister)
	}
	// The retired management methods must fall through to the same envelope as
	// any other unknown method: a host that still calls them gets an answer
	// rather than a crashed plugin.
	for _, method := range []string{"unknown.method", pluginabi.MethodManagementRegister, pluginabi.MethodManagementHandle} {
		raw, errCall := handleABIMethod(context.Background(), method, nil)
		if errCall != nil {
			t.Fatalf("%s returned transport error: %v", method, errCall)
		}
		var envelope pluginabi.Envelope
		if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil {
			t.Fatal(errDecode)
		}
		if envelope.OK || envelope.Error == nil || envelope.Error.Code != "unknown_method" {
			t.Fatalf("%s envelope = %s", method, raw)
		}
	}
}

// CPA maps http_status onto the client-visible error. A status carried by a
// wrapped cause must still reach it, or a 429 is reported as a 500 and the
// caller retries a limit it should be backing off from.
func TestABIErrorEnvelopeCarriesAWrappedStatus(t *testing.T) {
	wrapped := fmt.Errorf("mint Mirasim device ticket: %w", pluginabi.NewError("rate_limited", "slow down", http.StatusTooManyRequests))
	var envelope pluginabi.Envelope
	if errDecode := json.Unmarshal(abiErrorEnvelopeFromError("plugin_error", wrapped), &envelope); errDecode != nil {
		t.Fatal(errDecode)
	}
	if envelope.OK || envelope.Error == nil {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope.Error.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("http_status = %d", envelope.Error.HTTPStatus)
	}
}

func TestABIErrorEnvelopeLeavesAStatuslessErrorAtZero(t *testing.T) {
	var envelope pluginabi.Envelope
	if errDecode := json.Unmarshal(abiErrorEnvelopeFromError("plugin_error", errors.New("boom")), &envelope); errDecode != nil {
		t.Fatal(errDecode)
	}
	if envelope.Error == nil || envelope.Error.HTTPStatus != 0 {
		t.Fatalf("error = %#v", envelope.Error)
	}
}
