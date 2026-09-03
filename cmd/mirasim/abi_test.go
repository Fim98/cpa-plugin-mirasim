package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestABIRegisterAndManagementRoute(t *testing.T) {
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
	if registration.SchemaVersion != pluginabi.SchemaVersion || !registration.Capabilities.ManagementAPI || !registration.Capabilities.Executor || !registration.Capabilities.ThinkingApplier {
		t.Fatalf("registration = %#v", registration)
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
	if len(modelResponse.Models) != 8 {
		t.Fatalf("static models = %#v", modelResponse.Models)
	}
	for _, model := range modelResponse.Models {
		id := strings.ToLower(model.ID)
		if !strings.HasPrefix(id, "claude-") && !strings.HasPrefix(id, "gpt-") {
			t.Fatalf("unsupported model exposed through ABI: %#v", model)
		}
	}

	raw, errManagement := handleABIMethod(context.Background(), pluginabi.MethodManagementRegister, []byte(`{}`))
	if errManagement != nil {
		t.Fatalf("management register error = %v", errManagement)
	}
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil || !envelope.OK {
		t.Fatalf("management envelope = %s, error = %v", raw, errDecode)
	}
	var management abiManagementRegistration
	if errDecode := json.Unmarshal(envelope.Result, &management); errDecode != nil {
		t.Fatalf("decode management registration: %v", errDecode)
	}
	if len(management.Routes) != 1 || management.Routes[0].Method != "GET" || management.Routes[0].Path != "/mirasim/quota" {
		t.Fatalf("management routes = %#v", management.Routes)
	}
	if len(management.Resources) != 2 || management.Resources[0].Path != "/oauth/start" || management.Resources[1].Path != "/oauth/callback" {
		t.Fatalf("management resources = %#v", management.Resources)
	}
}

func TestABIUnknownMethodReturnsErrorEnvelope(t *testing.T) {
	defer MirasimPluginShutdown()
	if _, errRegister := handleABIMethod(context.Background(), pluginabi.MethodPluginRegister, []byte(`{}`)); errRegister != nil {
		t.Fatal(errRegister)
	}
	raw, errCall := handleABIMethod(context.Background(), "unknown.method", nil)
	if errCall != nil {
		t.Fatalf("unknown method returned transport error: %v", errCall)
	}
	var envelope pluginabi.Envelope
	if errDecode := json.Unmarshal(raw, &envelope); errDecode != nil {
		t.Fatal(errDecode)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "unknown_method" {
		t.Fatalf("envelope = %s", raw)
	}
}
