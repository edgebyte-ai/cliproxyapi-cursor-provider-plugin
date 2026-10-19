package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
)

type availableModelsResponse struct {
	Models             []availableModel `json:"models"`
	UseModelParameters bool             `json:"useModelParameters"`
}

type availableModel struct {
	Name              string         `json:"name"`
	ClientDisplayName string         `json:"clientDisplayName"`
	SupportsAgent     *bool          `json:"supportsAgent"`
	IsHidden          bool           `json:"isHidden"`
	Variants          []modelVariant `json:"variants"`
}

type modelVariant struct {
	LegacySlug            string           `json:"legacySlug"`
	DisplayName           string           `json:"displayName"`
	IsMaxMode             bool             `json:"isMaxMode"`
	IsDefaultNonMaxConfig bool             `json:"isDefaultNonMaxConfig"`
	ParameterValues       []modelParameter `json:"parameterValues"`
}

type modelParameter struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

func fetchAvailableModels(ctx context.Context, token string, cfg Config, client *http.Client) ([]CursorModel, error) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Connect-Protocol-Version", "1")
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("X-Ghost-Mode", "true")
	headers.Set("X-Cursor-Client-Version", cfg.ClientVersion)
	headers.Set("X-Cursor-Client-Type", "cli")
	var response availableModelsResponse
	_, _, err := doJSONWithClient(ctx, http.MethodPost, strings.TrimRight(cfg.CursorBaseURL, "/")+"/aiserver.v1.AiService/AvailableModels", headers,
		[]byte(`{"useModelParameters":true,"doNotUseMarkdown":true}`), client, &response)
	if err != nil {
		var upstream *UpstreamError
		if errors.As(err, &upstream) {
			return nil, cursorStatusError(upstream.Body, upstream.Status)
		}
		return nil, err
	}
	if !response.UseModelParameters {
		return nil, fmt.Errorf("Cursor did not return a parameterized model catalog")
	}
	return parameterizedModels(response), nil
}

func parameterizedModels(response availableModelsResponse) []CursorModel {
	models := make([]CursorModel, 0)
	seen := make(map[string]int)
	for _, model := range response.Models {
		name := strings.TrimSpace(model.Name)
		if name == "" || model.IsHidden || (model.SupportsAgent != nil && !*model.SupportsAgent) {
			continue
		}
		variants := model.Variants
		if len(variants) == 0 {
			variants = []modelVariant{{LegacySlug: name}}
		}
		for _, variant := range variants {
			// Use the non-max context, as the plugin does not expose a max-mode control.
			if variant.IsMaxMode {
				continue
			}
			id := firstNonEmpty(variant.LegacySlug, name)
			selection := &pb.RequestedModel{ModelId: name}
			for _, parameter := range variant.ParameterValues {
				selection.Parameters = append(selection.Parameters, &pb.Wire_RequestedModel_ModelParameterbytes{
					Id: parameter.ID, Value: parameter.Value,
				})
			}
			item := CursorModel{ID: id, DisplayName: firstNonEmpty(variant.DisplayName, model.ClientDisplayName, name), Selection: selection}
			key := strings.ToLower(id)
			if index, ok := seen[key]; ok {
				// Some models (such as Auto) reuse one slug for several parameter sets.
				if variant.IsDefaultNonMaxConfig {
					models[index] = item
				}
				continue
			}
			seen[key] = len(models)
			models = append(models, item)
		}
	}
	return models
}
