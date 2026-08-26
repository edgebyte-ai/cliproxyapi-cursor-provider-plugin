package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var pluginVersion = "dev"

var prepareCursorStream = func(ctx context.Context, req pluginapi.ExecutorRequest) (*provider.PreparedStream, error) {
	return pluginService.PrepareStream(ctx, req)
}

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
	Name string          `json:"name"`
	JSON json.RawMessage `json:"json"`
}

type accountPolicyResponse struct {
	AuthIndex     string   `json:"auth_index"`
	Name          string   `json:"name"`
	Label         string   `json:"label"`
	Prefix        string   `json:"prefix"`
	Priority      int      `json:"priority"`
	AllowedModels []string `json:"allowed_models"`
	DeniedModels  []string `json:"denied_models"`
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
			"message":          fmt.Sprintf("cursor provider executor completed model=%s upstream=%s format=%s source=%s payload_bytes=%d", req.Model, resolvedModel, req.Format, req.SourceFormat, len(response.Payload)),
			"fields":           map[string]any{"model": req.Model, "format": req.Format, "source_format": req.SourceFormat, "payload_bytes": len(response.Payload), "error": executeErr != nil},
		})
		return response, executeErr
	case pluginabi.MethodExecutorExecuteStream:
		var req rpcExecutorRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		prepared, err := prepareCursorStream(ctx, req.ExecutorRequest)
		if err != nil {
			return nil, err
		}
		go func() {
			streamErr := prepared.Pump(func(payload []byte) error {
				_, emitErr := callHost(pluginabi.MethodHostStreamEmit, hostStreamEmitRequest{StreamID: req.StreamID, Payload: payload})
				return emitErr
			})
			message := ""
			if streamErr != nil {
				message = streamErr.Error()
			}
			_, _ = callHost(pluginabi.MethodHostStreamClose, hostStreamCloseRequest{StreamID: req.StreamID, Error: message})
		}()
		return map[string]any{"headers": prepared.Headers()}, nil
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
			Routes: []rpcManagementRoute{
				{Method: http.MethodGet, Path: "/plugins/cursor-provider/quota", Description: "Cursor quota groups for one auth_index"},
				{Method: http.MethodGet, Path: "/plugins/cursor-provider/account-policy", Description: "Read one Cursor account model policy and priority"},
			},
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
	if req.Method == http.MethodGet && strings.HasSuffix(req.Path, "/plugins/cursor-provider/account-policy") {
		return getAccountPolicy(req)
	}
	if req.Method != http.MethodGet || !strings.HasSuffix(req.Path, "/plugins/cursor-provider/quota") {
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
	storage, ok := decodeCursorAuthStorage(response.JSON)
	if !ok {
		return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":"auth_index is not a Cursor credential"}`)}, nil
	}
	if strings.TrimSpace(storage.AccessToken) == "" {
		return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":"Cursor credential has no access token"}`)}, nil
	}
	quota, err := pluginService.Quota(ctx, storage)
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	body, _ := json.Marshal(quota)
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
}

func getAccountPolicy(req rpcManagementRequest) (pluginapi.ManagementResponse, error) {
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" {
		return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":"auth_index is required"}`)}, nil
	}
	raw, err := callHost(pluginabi.MethodHostAuthGet, hostAuthGetRequest{HostCallbackID: req.HostCallbackID, AuthIndex: authIndex})
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	var current hostAuthGetResponse
	if err := json.Unmarshal(raw, &current); err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	storage, ok := decodeCursorAuthStorage(current.JSON)
	if !ok {
		return pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":"auth_index is not a Cursor credential"}`)}, nil
	}
	body, _ := json.Marshal(newAccountPolicyResponse(authIndex, current.Name, storage))
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	}, nil
}

func newAccountPolicyResponse(authIndex, name string, storage provider.AuthStorage) accountPolicyResponse {
	return accountPolicyResponse{
		AuthIndex: authIndex, Name: name, Label: storage.Label,
		Prefix: storage.Prefix, Priority: storage.Priority,
		AllowedModels: append([]string{}, storage.AllowedModels...),
		DeniedModels:  append([]string{}, storage.DeniedModels...),
	}
}

func decodeCursorAuthStorage(raw json.RawMessage) (provider.AuthStorage, bool) {
	var storage provider.AuthStorage
	if err := json.Unmarshal(raw, &storage); err != nil {
		return provider.AuthStorage{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(storage.Type), provider.ProviderID) {
		return provider.AuthStorage{}, false
	}
	return storage, true
}

const cursorQuotaPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cursor Quota</title><style>
body{font:14px system-ui;background:#111827;color:#e5e7eb;margin:0;padding:28px}main{max-width:1060px;margin:auto}h1{margin:0 0 8px}.hint,.field-hint{color:#9ca3af}.hint{margin-bottom:18px}.controls{display:flex;gap:10px;margin-bottom:22px}input,textarea,button{font:inherit;padding:9px 12px;border-radius:8px;border:1px solid #374151;background:#111827;color:#fff;box-sizing:border-box}input{min-width:0}button{cursor:pointer;background:#2563eb}button:disabled{cursor:not-allowed;opacity:.55}.grid{display:grid;gap:16px}.card{background:#1f2937;border:1px solid #374151;border-radius:12px;padding:18px}.title{display:flex;justify-content:space-between;gap:12px;margin-bottom:14px}.identity{display:grid;gap:3px}.identity small{color:#9ca3af}.quota{display:grid;grid-template-columns:160px 1fr 70px;align-items:center;gap:12px;margin:12px 0}.bar{height:10px;background:#374151;border-radius:99px;overflow:hidden}.fill{height:100%;background:#22c55e}.danger{background:#ef4444}.policy{border-top:1px solid #374151;margin-top:18px;padding-top:18px}.policy h2{font-size:16px;margin:0 0 14px}.policy-grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:14px}.field{display:grid;gap:6px}.field.full{grid-column:1/-1}.field input,.field textarea{width:100%}.field textarea{min-height:92px;resize:vertical;line-height:1.45}.policy-actions{display:flex;align-items:center;gap:12px;margin-top:14px}.status{color:#86efac}.error{color:#fca5a5;white-space:pre-wrap}@media(max-width:720px){body{padding:16px}.controls{flex-direction:column}.policy-grid{grid-template-columns:1fr}.quota{grid-template-columns:110px 1fr 58px}}
</style></head><body><main><h1>Cursor Quota</h1><div class="hint">Management key stays in page memory and is sent only to this CLIProxyAPI origin.</div>
<div class="controls"><input id="key" type="password" autocomplete="off" placeholder="Management key"><button id="load">Load quotas</button></div><div id="out" class="grid"></div></main>
<script>
const out=document.getElementById('out'),key=document.getElementById('key'),storageKey='cliproxyapi.cursor-provider.management-key',policyPath='/v0/management/plugins/cursor-provider/account-policy';
let accounts=[];
key.value=sessionStorage.getItem(storageKey)||'';
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
async function api(path,options={}){const headers={Authorization:'Bearer '+key.value,...(options.headers||{})};const r=await fetch(path,{...options,headers,cache:'no-store'});if(r.status===401){sessionStorage.removeItem(storageKey);key.value=''}if(!r.ok)throw new Error('HTTP '+r.status+' '+await r.text());return r.json()}
const rules=v=>(Array.isArray(v)?v:[]).join('\n');
const parseRules=v=>{const seen=new Set;return String(v||'').split(/\r?\n/).map(x=>x.trim().toLowerCase()).filter(x=>x&&!seen.has(x)&&(seen.add(x),true))};
function quotaRows(q){if(q?.error)return '<div class="error">'+esc(q.error)+'</div>';return (q?.quota||[]).map(x=>{const used=Number(x.usedPercent),remain=Number.isFinite(used)?Math.max(0,100-used):0;return '<div class="quota"><span>'+esc(x.key)+'</span><div class="bar"><div class="fill '+(remain<20?'danger':'')+'" style="width:'+remain+'%"></div></div><strong>'+remain.toFixed(1)+'%</strong></div>'}).join('')}
function card(x,i){const a=x.a,p=x.p;if(p?.error)return '<section class="card"><div class="title"><div class="identity"><strong>'+esc(a.label||a.name)+'</strong><small>'+esc(a.name)+'</small></div></div>'+quotaRows(x.q)+'<div class="error">'+esc(p.error)+'</div></section>';return '<section class="card"><div class="title"><div class="identity"><strong>'+esc(p.label||a.label||a.name)+'</strong><small>'+esc(p.name||a.name)+'</small></div><span>P'+esc(p.priority)+'</span></div>'+quotaRows(x.q)+'<div class="policy"><h2>Account policy</h2><div class="policy-grid"><label class="field"><span>Priority</span><input id="priority-'+i+'" type="number" step="1" value="'+esc(p.priority)+'"><span class="field-hint">Higher values are selected first.</span></label><label class="field"><span>Model prefix</span><input id="prefix-'+i+'" value="'+esc(p.prefix)+'"><span class="field-hint">Leave empty for unprefixed model names.</span></label><label class="field"><span>Allowed model rules</span><textarea id="allowed-'+i+'" spellcheck="false">'+esc(rules(p.allowed_models))+'</textarea><span class="field-hint">One rule per line. Empty means all models are allowed before deny rules.</span></label><label class="field"><span>Denied model rules</span><textarea id="denied-'+i+'" spellcheck="false">'+esc(rules(p.denied_models))+'</textarea><span class="field-hint">One rule per line. Deny rules take precedence. * is supported.</span></label></div><div class="policy-actions"><button id="save-'+i+'" onclick="savePolicy('+i+')">Save account policy</button><span id="status-'+i+'" class="status"></span></div></div></section>'}
async function load(){if(!key.value){out.innerHTML='<div class="error">Enter the management key.</div>';return}sessionStorage.setItem(storageKey,key.value);out.innerHTML='Loading…';try{const auth=await api('/v0/management/auth-files'),rows=(auth.files||[]).filter(x=>x.type==='cursor');accounts=await Promise.all(rows.map(async a=>{const query='?auth_index='+encodeURIComponent(a.auth_index);const [q,p]=await Promise.all([api('/v0/management/plugins/cursor-provider/quota'+query).catch(e=>({error:e.message})),api(policyPath+query).catch(e=>({error:e.message}))]);return{a,q,p}}));out.innerHTML=accounts.length?accounts.map(card).join(''):'<div class="error">No Cursor auth files found.</div>'}catch(e){out.innerHTML='<div class="error">'+esc(e.message)+'</div>'}}
async function savePolicy(i){const x=accounts[i],status=document.getElementById('status-'+i),button=document.getElementById('save-'+i),priority=Number(document.getElementById('priority-'+i).value);if(!Number.isSafeInteger(priority)){status.className='error';status.textContent='Priority must be an integer.';return}button.disabled=true;status.className='status';status.textContent='Saving…';try{await api('/v0/management/auth-files/fields',{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:x.p.name,priority,prefix:document.getElementById('prefix-'+i).value,allowed_models:parseRules(document.getElementById('allowed-'+i).value),denied_models:parseRules(document.getElementById('denied-'+i).value)})});status.textContent='Saved. Reloading…';await load()}catch(e){status.className='error';status.textContent=e.message;button.disabled=false}}
document.getElementById('load').onclick=load;if(key.value)load();
</script></body></html>`

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name: "Cursor Provider", Version: pluginVersion, Author: "edgebyte-ai",
			GitHubRepository: "https://github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Enable the Cursor provider."},
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
