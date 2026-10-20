//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestProviderErrorEnvelopePreservesWrappedStatus(t *testing.T) {
	for _, retryable := range []bool{false, true} {
		upstream := &provider.StatusError{
			Code: "cursor_provider_error", Message: "Provider rejected the request (provider HTTP 400)",
			HTTPStatus: http.StatusBadGateway, Retryable: retryable,
		}
		err := fmt.Errorf("run turn: %w", upstream)
		var envelope pluginabi.Envelope
		if errDecode := json.Unmarshal(providerErrorEnvelope(err), &envelope); errDecode != nil {
			t.Fatal(errDecode)
		}
		if envelope.OK || envelope.Error == nil {
			t.Fatalf("unexpected envelope: %#v", envelope)
		}
		if envelope.Error.Code != upstream.Code || envelope.Error.HTTPStatus != upstream.HTTPStatus || envelope.Error.Retryable != retryable || envelope.Error.Message != err.Error() {
			t.Fatalf("wrapped error lost its classification: %#v", envelope.Error)
		}
	}
}
