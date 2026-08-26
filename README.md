# CLIProxyAPI Cursor Provider Plugin

A Go provider plugin for CLIProxyAPI. It registers Cursor accounts as native CLIProxyAPI auth records and talks directly to Cursor's Connect-RPC service.

Current target capabilities:

- multiple Cursor auth records in one CLIProxyAPI instance;
- per-auth model discovery, priority, prefix, allowlist and denylist;
- OpenAI Responses, Chat Completions and Anthropic Messages inputs;
- caller-owned tool calls and real streaming through the plugin host callback;
- normalized `reasoning_effort` model families;
- `resource_exhausted`, reset metadata and retry/cooldown propagation;
- `cursor-native` and `other-models` quota groups through a management route.
- a plugin-owned browser editor for each account's priority, prefix, allow rules, and deny rules.
- configurable exact model ID to catalog display-name mappings.

The plugin is MIT licensed. Credentials belong in CLIProxyAPI's private auth directory and must never be committed.

Naming is intentionally split by responsibility:

- plugin name: `Cursor Provider`;
- plugin ID and binary: `cliproxyapi-cursor-provider`;
- provider/auth type and OAuth alias channel: `cursor` (shown as `Cursor` in Auth Files);
- quota groups: `cursor-native` and `other-models`.

Catalog display names can be customized without changing model IDs or upstream routing:

```yaml
model_display_names:
  cursor-grok-4.5: "Grok 4.5"
  cursor-grok-4.6: "Grok 4.6"
```

The mapping key is the Cursor model ID after effort normalization and before CLIProxyAPI OAuth aliases are applied. Matching is case-insensitive. The Plugin **Edit config** form exposes the field as a JSON object.

## Development

The repository includes an isolated test deployment on `127.0.0.1:18317`. It uses a dedicated Docker Compose project, config, auth directory, plugin directory, and log directory, so it does not touch another CLIProxyAPI deployment.

`internal/pb` is generated from Cursor's extracted wire descriptor. The descriptor has several type names that collide with Go oneof wrapper names, so `scripts/prepare_proto.go` performs deterministic symbol-only renaming before `protoc`; field numbers and protobuf wire bytes are unchanged.

Build and test:

```sh
go test ./...
make build
docker compose -p cursor-provider-plugin-test -f docker-compose.test.yml up -d
```

The external Plugin ABI currently preserves HTTP status but not error response headers. Cursor `resource_exhausted` is therefore mapped to HTTP 429 and the computed retry delay is included in the error message; native `Retry-After` response-header propagation requires a future CLIProxyAPI Plugin ABI field.

## Install and publish

- [Remote Linux deployment](docs/deploy-linux.md)
- [Credential-safe publishing and Plugin Store distribution](docs/publishing.md)

Public Plugin Store registry:

```text
https://raw.githubusercontent.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/main/plugin-store/registry.json
```

Release archives contain only the platform plugin library. Cursor credentials are created later inside the target CLIProxyAPI private `auth-dir` and are never part of the release or registry.
