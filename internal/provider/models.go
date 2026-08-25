package provider

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/edgebyte-ai/cliproxyapi-cursor-native-plugin/internal/pb"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"google.golang.org/protobuf/proto"
)

var reasoningLevels = []string{"low", "medium", "high", "xhigh", "max"}

type CursorModel struct {
	ID          string
	DisplayName string
}

type normalizedFamily struct {
	ID            string
	DisplayName   string
	Variants      map[string]string
	DefaultEffort string
}

func (s *Service) StaticModels(context.Context, pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	return pluginapi.ModelResponse{Provider: ProviderID, Models: nil}, nil
}

func (s *Service) ModelsForAuth(ctx context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	storage, err := decodeAuth(req.StorageJSON)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	models, err := s.discoverModels(ctx, storage)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	return pluginapi.ModelResponse{Provider: ProviderID, Models: s.modelInfos(models, storage)}, nil
}

func (s *Service) discoverModels(ctx context.Context, storage AuthStorage) ([]CursorModel, error) {
	cacheKey := storage.ID
	if cacheKey == "" {
		cacheKey = stableAuthID(storage.Label, storage.AccessToken)
	}
	now := s.now()
	s.modelMu.Lock()
	if cached, ok := s.modelCache[cacheKey]; ok && cached.expiresAt.After(now) {
		models := append([]CursorModel(nil), cached.models...)
		s.modelMu.Unlock()
		return models, nil
	}
	s.modelMu.Unlock()

	reqBody, _ := proto.Marshal(&pb.GetUsableModelsRequest{})
	responseBody, _, err := unaryProto(ctx, "/agent.v1.AgentService/GetUsableModels", reqBody, storage.AccessToken, s.Config(), 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("discover Cursor models: %w", err)
	}
	var response pb.GetUsableModelsResponse
	if err := proto.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode Cursor model catalog: %w", err)
	}
	models := make([]CursorModel, 0, len(response.Models))
	seen := make(map[string]struct{}, len(response.Models))
	for _, model := range response.Models {
		id := strings.TrimSpace(model.GetModelId())
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, CursorModel{ID: id, DisplayName: firstNonEmpty(model.GetDisplayName(), model.GetDisplayNameShort(), id)})
	}
	models = filterModels(models, storage)
	if len(models) == 0 {
		return nil, &StatusError{Code: "models_empty", Message: "Cursor account returned no permitted models", HTTPStatus: http.StatusBadGateway}
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	s.modelMu.Lock()
	s.modelCache[cacheKey] = cachedModels{models: append([]CursorModel(nil), models...), expiresAt: now.Add(s.Config().ModelCacheTTL())}
	s.modelMu.Unlock()
	return models, nil
}

func filterModels(models []CursorModel, storage AuthStorage) []CursorModel {
	allowed := make([]string, 0, len(storage.AllowedModels))
	for _, id := range storage.AllowedModels {
		allowed = append(allowed, strings.ToLower(strings.TrimSpace(id)))
	}
	denied := make([]string, 0, len(storage.DeniedModels))
	for _, id := range storage.DeniedModels {
		denied = append(denied, strings.ToLower(strings.TrimSpace(id)))
	}
	out := make([]CursorModel, 0, len(models))
	for _, model := range models {
		key := strings.ToLower(model.ID)
		if len(allowed) > 0 {
			if !matchesAny(key, allowed) {
				continue
			}
		}
		if matchesAny(key, denied) {
			continue
		}
		out = append(out, model)
	}
	return out
}

func matchesAny(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		if matched, err := path.Match(pattern, value); err == nil && matched {
			return true
		}
		if pattern == value {
			return true
		}
	}
	return false
}

func (s *Service) modelInfos(models []CursorModel, storage AuthStorage) []pluginapi.ModelInfo {
	cfg := s.Config()
	created := s.now().Unix()
	out := make([]pluginapi.ModelInfo, 0, len(models))
	if cfg.ModelMode == "raw" || cfg.ModelMode == "both" {
		for _, model := range models {
			out = append(out, pluginModelInfo(cfg.ModelPrefix+model.ID, model.ID, model.DisplayName, nil, created))
		}
	}
	if cfg.ModelMode == "normalized" || cfg.ModelMode == "both" {
		families := normalizedFamilies(models, cfg.DefaultReasoningEffort)
		keys := make([]string, 0, len(families))
		for key := range families {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			family := families[key]
			out = append(out, pluginModelInfo(cfg.ModelPrefix+family.ID, family.ID, family.DisplayName, family, created))
		}
	}
	return out
}

func pluginModelInfo(id, nativeID, display string, family *normalizedFamily, created int64) pluginapi.ModelInfo {
	parameters := []string{"stream", "tools"}
	var thinking *pluginapi.ThinkingSupport
	if family != nil && len(family.Variants) > 0 {
		levels := make([]string, 0, len(family.Variants))
		for _, level := range reasoningLevels {
			if _, ok := family.Variants[level]; ok {
				levels = append(levels, level)
			}
		}
		parameters = append(parameters, "reasoning_effort")
		thinking = &pluginapi.ThinkingSupport{DynamicAllowed: false, Levels: levels}
	}
	return pluginapi.ModelInfo{
		ID: id, Object: "model", Created: created, OwnedBy: "cursor", Type: "chat",
		DisplayName: display, Name: nativeID,
		Description:                "Cursor subscription model through direct Connect-RPC",
		SupportedGenerationMethods: []string{"openai-response", "openai-chat", "claude"},
		SupportedParameters:        parameters,
		SupportedInputModalities:   []string{"TEXT"}, SupportedOutputModalities: []string{"TEXT"},
		Thinking: thinking,
	}
}

func normalizedFamilies(models []CursorModel, preferred string) map[string]*normalizedFamily {
	families := make(map[string]*normalizedFamily)
	for _, model := range models {
		base, effort, ok := splitEffort(model.ID)
		if !ok {
			if _, exists := families[model.ID]; !exists {
				families[model.ID] = &normalizedFamily{ID: model.ID, DisplayName: model.DisplayName}
			}
			continue
		}
		family := families[base]
		if family == nil {
			family = &normalizedFamily{ID: base, DisplayName: stripEffortDisplay(model.DisplayName, effort), Variants: make(map[string]string)}
			families[base] = family
		}
		if family.Variants == nil {
			family.Variants = make(map[string]string)
		}
		family.Variants[effort] = model.ID
	}
	for _, family := range families {
		if len(family.Variants) == 0 {
			continue
		}
		if _, ok := family.Variants[preferred]; ok {
			family.DefaultEffort = preferred
		} else {
			for _, effort := range []string{"high", "medium", "low", "xhigh", "max"} {
				if _, ok := family.Variants[effort]; ok {
					family.DefaultEffort = effort
					break
				}
			}
		}
	}
	return families
}

func splitEffort(id string) (string, string, bool) {
	parts := strings.Split(id, "-")
	for index := len(parts) - 1; index >= 0; index-- {
		candidate := strings.ToLower(parts[index])
		if contains(reasoningLevels, candidate) {
			baseParts := append([]string(nil), parts[:index]...)
			baseParts = append(baseParts, parts[index+1:]...)
			return strings.Join(baseParts, "-"), candidate, true
		}
		if candidate != "thinking" && candidate != "fast" {
			break
		}
	}
	return id, "", false
}

func (s *Service) resolveModel(ctx context.Context, storage AuthStorage, requested, effort string) (string, error) {
	models, err := s.discoverModels(ctx, storage)
	if err != nil {
		return "", err
	}
	cfg := s.Config()
	requested = strings.TrimPrefix(strings.TrimSpace(requested), cfg.ModelPrefix)
	families := normalizedFamilies(models, cfg.DefaultReasoningEffort)
	if family := families[requested]; family != nil && len(family.Variants) == 0 && strings.TrimSpace(effort) != "" && cfg.ModelMode != "raw" {
		return "", &StatusError{Code: "unsupported_reasoning_effort", Message: "reasoning effort is not supported by this Cursor model", HTTPStatus: http.StatusBadRequest}
	}
	for _, model := range models {
		if strings.EqualFold(model.ID, requested) {
			if cfg.ModelMode == "normalized" {
				if _, _, suffixed := splitEffort(model.ID); suffixed {
					return "", &StatusError{Code: "raw_effort_model_disabled", Message: "raw effort-suffixed Cursor model is disabled", HTTPStatus: http.StatusBadRequest}
				}
			}
			return model.ID, nil
		}
	}
	family := families[requested]
	if family == nil || len(family.Variants) == 0 {
		return "", &StatusError{Code: "model_not_found", Message: "Cursor model is not available for this account", HTTPStatus: http.StatusNotFound}
	}
	if effort == "" {
		effort = family.DefaultEffort
	}
	upstreamID, ok := family.Variants[strings.ToLower(effort)]
	if !ok {
		return "", &StatusError{Code: "unsupported_reasoning_effort", Message: "reasoning effort is not supported by this Cursor model", HTTPStatus: http.StatusBadRequest}
	}
	return upstreamID, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func stripEffortDisplay(display, effort string) string {
	return strings.TrimSpace(strings.ReplaceAll(display, " "+effort, ""))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
