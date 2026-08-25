package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct {
    uint32_t abi_version;
    void* host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;
    cliproxy_plugin_free_fn free_buffer;
    cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static void clear_host_api(void) { stored_host = NULL; }
static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) return 1;
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}
static void free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) stored_host->free_buffer(ptr, len);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

var pluginService = provider.New()

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil || uint32(host.abi_version) != pluginabi.ABIVersion {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required", 0, false, 0))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	result, err := dispatch(C.GoString(method), requestBytes)
	if err != nil {
		status := 0
		code := "plugin_error"
		retryable := false
		retryAfter := int64(0)
		if typed, ok := err.(*provider.StatusError); ok {
			status = typed.HTTPStatus
			code = typed.Code
			retryable = typed.Retryable
			retryAfter = providerRetryAfterSeconds(typed)
		}
		writeResponse(response, errorEnvelope(code, err.Error(), status, retryable, retryAfter))
		return 1
	}
	raw, marshalErr := okEnvelope(result)
	if marshalErr != nil {
		writeResponse(response, errorEnvelope("encoding_error", marshalErr.Error(), 500, false, 0))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	pluginService.Shutdown()
	C.clear_host_api()
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

func callHost(method string, payload any) (json.RawMessage, error) {
	request, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var requestPtr unsafe.Pointer
	if len(request) > 0 {
		requestPtr = C.CBytes(request)
		defer C.free(requestPtr)
	}
	var response C.cliproxy_buffer
	rc := C.call_host_api(cMethod, (*C.uint8_t)(requestPtr), C.size_t(len(request)), &response)
	var raw []byte
	if response.ptr != nil && response.len > 0 {
		raw = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("host callback %s returned %d without a response", method, int(rc))
	}
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if !envelope.OK {
		message := "host callback failed"
		if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
			message = envelope.Error.Message
		}
		return nil, fmt.Errorf("%s", message)
	}
	if rc != 0 {
		return nil, fmt.Errorf("host callback %s returned %d", method, int(rc))
	}
	return envelope.Result, nil
}

func okEnvelope(value any) ([]byte, error) {
	result, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: result})
}

func errorEnvelope(code, message string, status int, retryable bool, retryAfter int64) []byte {
	if retryAfter > 0 {
		message = fmt.Sprintf("%s (retry after %ds)", message, retryAfter)
	}
	raw, _ := json.Marshal(pluginabi.Envelope{OK: false, Error: &pluginabi.Error{Code: code, Message: message, HTTPStatus: status, Retryable: retryable}})
	return raw
}

func providerRetryAfterSeconds(err *provider.StatusError) int64 {
	if err == nil || err.RetryAfter <= 0 {
		return 0
	}
	seconds := int64(err.RetryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}
