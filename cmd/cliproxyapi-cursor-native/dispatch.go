package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/edgebyte-ai/cliproxyapi-cursor-native-plugin/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const pluginVersion = "0.1.0"

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats"`
	ManagementAPI         bool                         `json:"management_api"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

type rpcAuthLoginStartRequest struct {
	pluginapi.AuthLoginStartRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcAuthLoginPollRequest struct {
	pluginapi.AuthLoginPollRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcAuthRefreshRequest struct {
	pluginapi.AuthRefreshRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcAuthModelRequest struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcManagementRequest struct {
	pluginapi.ManagementRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcManagementRegistrationResponse struct {
	Routes    []rpcManagementRoute `json:"routes,omitempty"`
	Resources []rpcResourceRoute   `json:"resources,omitempty"`
}

type rpcManagementRoute struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type rpcResourceRoute struct {
	Path        string `json:"path"`
	Menu        string `json:"menu,omitempty"`
	Description string `json:"description,omitempty"`
}

type hostStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
}

type hostStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

type hostAuthGetRequest struct {
	HostCallbackID string `json:"host_callback_id,omitempty"`
	AuthIndex      string `json:"auth_index"`
}

type hostAuthGetResponse struct {
	JSON json.RawMessage `json:"json"`
}

func dispatch(method string, request []byte) (any, error) {
	ctx := context.Background()
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		var req lifecycleRequest
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, err
			}
		}
		if err := pluginService.Configure(req.ConfigYAML); err != nil {
			return nil, err
		}
		return pluginRegistration(), nil
	case pluginabi.MethodAuthIdentifier, pluginabi.MethodExecutorIdentifier:
		return identifierResponse{Identifier: provider.ProviderID}, nil
	case pluginabi.MethodAuthParse:
		var req pluginapi.AuthParseRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.ParseAuth(ctx, req)
	case pluginabi.MethodAuthLoginStart:
		var req rpcAuthLoginStartRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.StartLogin(ctx, req.AuthLoginStartRequest)
	case pluginabi.MethodAuthLoginPoll:
		var req rpcAuthLoginPollRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.PollLogin(ctx, req.AuthLoginPollRequest)
	case pluginabi.MethodAuthRefresh:
		var req rpcAuthRefreshRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.RefreshAuth(ctx, req.AuthRefreshRequest)
	case pluginabi.MethodModelStatic:
		var req pluginapi.StaticModelRequest
		_ = json.Unmarshal(request, &req)
		return pluginService.StaticModels(ctx, req)
	case pluginabi.MethodModelForAuth:
		var req rpcAuthModelRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.ModelsForAuth(ctx, req.AuthModelRequest)
	case pluginabi.MethodExecutorExecute:
		var req rpcExecutorRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		response, executeErr := pluginService.Execute(ctx, req.ExecutorRequest)
		resolvedModel, _ := response.Metadata["cursor_model"].(string)
		_, _ = callHost(pluginabi.MethodHostLog, map[string]any{
			"host_callback_id": req.HostCallbackID,
			"level":            "debug",
			"message":          fmt.Sprintf("cursor native executor completed model=%s upstream=%s format=%s source=%s payload_bytes=%d", req.Model, resolvedModel, req.Format, req.SourceFormat, len(response.Payload)),
			"fields":           map[string]any{"model": req.Model, "format": req.Format, "source_format": req.SourceFormat, "payload_bytes": len(response.Payload), "error": executeErr != nil},
		})
		return response, executeErr
	case pluginabi.MethodExecutorExecuteStream:
		var req rpcExecutorRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		headers := http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}}
		go func() {
			_, streamErr := pluginService.ExecuteStream(context.Background(), req.ExecutorRequest, func(payload []byte) error {
				_, emitErr := callHost(pluginabi.MethodHostStreamEmit, hostStreamEmitRequest{StreamID: req.StreamID, Payload: payload})
				return emitErr
			})
			message := ""
			if streamErr != nil {
				message = streamErr.Error()
			}
			_, _ = callHost(pluginabi.MethodHostStreamClose, hostStreamCloseRequest{StreamID: req.StreamID, Error: message})
		}()
		return map[string]any{"headers": headers}, nil
	case pluginabi.MethodExecutorCountTokens:
		var req rpcExecutorRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.CountTokens(ctx, req.ExecutorRequest)
	case pluginabi.MethodExecutorHTTPRequest:
		return nil, &provider.StatusError{Code: "unsupported_http", Message: "arbitrary Cursor HTTP proxying is disabled", HTTPStatus: http.StatusForbidden}
	case pluginabi.MethodManagementRegister:
		return rpcManagementRegistrationResponse{
			Routes:    []rpcManagementRoute{{Method: http.MethodGet, Path: "/plugins/cursor-native/quota", Description: "Cursor native quota groups for one auth_index"}},
			Resources: []rpcResourceRoute{{Path: "/quota", Menu: "Cursor Quota", Description: "Cursor account quota groups"}},
		}, nil
	case pluginabi.MethodManagementHandle:
		var req rpcManagementRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return handleManagement(ctx, req)
	default:
		return nil, &provider.StatusError{Code: "unknown_method", Message: "unknown plugin method: " + method, HTTPStatus: http.StatusNotImplemented}
	}
}

func handleManagement(ctx context.Context, req rpcManagementRequest) (pluginapi.ManagementResponse, error) {
	if req.Method == http.MethodGet && strings.Contains(req.Path, "/resource/plugins/") && strings.HasSuffix(req.Path, "/quota") {
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers: http.Header{
				"Content-Type":            []string{"text/html; charset=utf-8"},
				"Cache-Control":           []string{"no-store"},
				"Content-Security-Policy": []string{"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'"},
				"X-Content-Type-Options":  []string{"nosniff"},
				"Referrer-Policy":         []string{"no-referrer"},
			},
			Body: []byte(cursorQuotaPage),
		}, nil
	}
	if req.Method != http.MethodGet || !strings.HasSuffix(req.Path, "/plugins/cursor-native/quota") {
		return pluginapi.ManagementResponse{StatusCode: http.StatusNotFound, Body: []byte(`{"error":"not found"}`)}, nil
	}
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" {
		return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":"auth_index is required"}`)}, nil
	}
	raw, err := callHost(pluginabi.MethodHostAuthGet, hostAuthGetRequest{HostCallbackID: req.HostCallbackID, AuthIndex: authIndex})
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	var response hostAuthGetResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	var storage provider.AuthStorage
	if err := json.Unmarshal(response.JSON, &storage); err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	quota, err := pluginService.Quota(ctx, storage)
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	body, _ := json.Marshal(quota)
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
}

const cursorQuotaPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cursor Quota</title><style>
body{font:14px system-ui;background:#111827;color:#e5e7eb;margin:0;padding:28px}main{max-width:960px;margin:auto}h1{margin:0 0 8px}.hint{color:#9ca3af;margin-bottom:18px}.controls{display:flex;gap:10px;margin-bottom:22px}input,button{font:inherit;padding:9px 12px;border-radius:8px;border:1px solid #374151;background:#1f2937;color:#fff}input{flex:1}button{cursor:pointer;background:#2563eb}.grid{display:grid;gap:16px}.card{background:#1f2937;border:1px solid #374151;border-radius:12px;padding:18px}.title{display:flex;justify-content:space-between;margin-bottom:14px}.quota{display:grid;grid-template-columns:160px 1fr 70px;align-items:center;gap:12px;margin:12px 0}.bar{height:10px;background:#374151;border-radius:99px;overflow:hidden}.fill{height:100%;background:#22c55e}.danger{background:#ef4444}.error{color:#fca5a5;white-space:pre-wrap}
</style></head><body><main><h1>Cursor Quota</h1><div class="hint">Management key stays in page memory and is sent only to this CLIProxyAPI origin.</div>
<div class="controls"><input id="key" type="password" autocomplete="off" placeholder="Management key"><button id="load">Load quotas</button></div><div id="out" class="grid"></div></main>
<script>
const out=document.getElementById('out'),key=document.getElementById('key');
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
async function api(path){const r=await fetch(path,{headers:{Authorization:'Bearer '+key.value},cache:'no-store'});if(!r.ok)throw new Error('HTTP '+r.status+' '+await r.text());return r.json()}
document.getElementById('load').onclick=async()=>{out.innerHTML='Loading…';try{const auth=await api('/v0/management/auth-files');const rows=(auth.files||[]).filter(x=>x.type==='cursor-native');const results=await Promise.all(rows.map(async a=>{try{return{a,q:await api('/v0/management/plugins/cursor-native/quota?auth_index='+encodeURIComponent(a.auth_index))}}catch(e){return{a,e}}}));out.innerHTML=results.map(({a,q,e})=>{if(e)return '<section class="card"><div class="title"><strong>'+esc(a.label||a.name)+'</strong></div><div class="error">'+esc(e.message)+'</div></section>';return '<section class="card"><div class="title"><strong>'+esc(a.label||a.name)+'</strong><span>P'+esc(a.priority||0)+'</span></div>'+q.quota.map(x=>{const used=Number(x.usedPercent);const remain=Number.isFinite(used)?Math.max(0,100-used):0;return '<div class="quota"><span>'+esc(x.key)+'</span><div class="bar"><div class="fill '+(remain<20?'danger':'')+'" style="width:'+remain+'%"></div></div><strong>'+remain.toFixed(1)+'%</strong></div>'}).join('')+'</section>'}).join('')}catch(e){out.innerHTML='<div class="error">'+esc(e.message)+'</div>'}}
</script></body></html>`

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name: "Cursor Native Provider", Version: pluginVersion, Author: "edgebyte-ai",
			GitHubRepository: "https://github.com/edgebyte-ai/cliproxyapi-cursor-native-plugin",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Enable the Cursor native provider."},
				{Name: "model_prefix", Type: pluginapi.ConfigFieldTypeString, Description: "Prefix added to Cursor model IDs."},
				{Name: "model_mode", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"raw", "normalized", "both"}, Description: "Cursor model catalog compatibility mode."},
				{Name: "default_reasoning_effort", Type: pluginapi.ConfigFieldTypeString, Description: "Default effort for normalized model families."},
			},
		},
		Capabilities: registrationCapability{
			ModelProvider: true, AuthProvider: true, Executor: true,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeOAuth,
			ExecutorInputFormats:  []string{"openai", "openai-response", "claude"},
			ExecutorOutputFormats: []string{"openai", "openai-response", "claude"},
			ManagementAPI:         true,
		},
	}
}
