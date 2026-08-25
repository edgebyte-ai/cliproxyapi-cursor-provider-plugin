# Deploy on a remote Linux CLIProxyAPI host

This plugin runs inside CLIProxyAPI. Use CLIProxyAPI `v7.2.141` or newer with the Plugin ABI enabled. The current release artifact supports Linux amd64.

## Security boundary

The release ZIP, `.so`, repository, image, and plugin configuration contain no Cursor credentials. Browser login writes each account to CLIProxyAPI's configured private `auth-dir` on the target machine. Do not copy that directory into this repository, a Docker image, a public backup, or a support bundle.

Recommended permissions:

```sh
install -d -m 700 /opt/cliproxyapi/plugins/linux/amd64
install -d -m 700 /opt/cliproxyapi/auths
```

## Manual installation

Download into a temporary directory and verify the published SHA-256:

```sh
tmp_dir=$(mktemp -d)
cd "$tmp_dir"
curl -fLO https://github.com/edgebyte-ai/cliproxyapi-cursor-native-plugin/releases/download/v0.1.1/cliproxyapi-cursor-native_0.1.1_linux_amd64.zip
curl -fLO https://github.com/edgebyte-ai/cliproxyapi-cursor-native-plugin/releases/download/v0.1.1/checksums.txt
sha256sum -c checksums.txt
unzip cliproxyapi-cursor-native_0.1.1_linux_amd64.zip
install -m 755 cliproxyapi-cursor-native.so /opt/cliproxyapi/plugins/linux/amd64/cliproxyapi-cursor-native.so
```

Expected v0.1.1 ZIP checksum:

```text
24bff7dd3437052769bfceb6b0789137d9d058736c0ae8e1b81fe2a872ec4393
```

## CLIProxyAPI configuration

Add this block to the existing `config.yaml` without changing unrelated providers:

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cliproxyapi-cursor-native:
      enabled: true
      priority: 100
      provider_id: "cursor-native"
      model_prefix: ""
      model_mode: "normalized"
      default_reasoning_effort: "high"
      cursor_base_url: "https://api2.cursor.sh"
      client_version: "cli-2026.08.11-e8db854"
      allowed_native_tools: ["mcp_tool_call"]
      request_timeout_seconds: 300
      model_cache_ttl_seconds: 600
```

For Docker, mount the plugin and auth roots separately:

```yaml
services:
  cliproxyapi:
    volumes:
      - /opt/cliproxyapi/plugins:/CLIProxyAPI/plugins:ro
      - /opt/cliproxyapi/auths:/root/.cli-proxy-api
```

`plugins.dir` is relative to CLIProxyAPI's working directory. The mounted directory must contain:

```text
plugins/linux/amd64/cliproxyapi-cursor-native.so
```

Restart only the target CLIProxyAPI instance after reviewing the config. Verify the log contains both `plugin loaded` and `plugin registered` for `cliproxyapi-cursor-native`.

## Add Cursor accounts without copying tokens

Start the plugin login flow through the target CLIProxyAPI Management API:

```sh
curl -sS \
  -H "Authorization: Bearer $MANAGEMENT_KEY" \
  http://127.0.0.1:8317/v0/management/cursor-native-auth-url
```

Open the returned Cursor URL in your browser. Poll the returned state until it reports `ok`:

```sh
curl -sS \
  -H "Authorization: Bearer $MANAGEMENT_KEY" \
  "http://127.0.0.1:8317/v0/management/get-auth-status?state=$STATE"
```

Repeat for the second Cursor account. The plugin writes separate `0600` auth JSON files under `auth-dir`. Never paste access or refresh tokens into `config.yaml`.

Set each account's policy through the plugin's authenticated Management API. This updates the private source auth file without returning or logging its tokens:

```sh
curl -sS -X PATCH \
  -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  "http://127.0.0.1:8317/v0/management/plugins/cursor-native/account-policy?auth_index=$AUTH_INDEX" \
  -d '{"priority":10,"prefix":"","allowed_models":["cursor-grok-*","composer-*","*fable*"],"denied_models":["*-fast"]}'
```

Recommended account policy for this deployment:

- Cursor Account 1: priority 10; allow `cursor-grok-*`, `composer-*`, `*fable*`; deny `*-fast`.
- Cursor Account 2: priority 0; deny `gpt-*`, `cursor-gpt-*`, `openai-*`, `*-fast`.

## Verification

Check models and each account's quota:

```sh
curl -sS -H "Authorization: Bearer $API_KEY" http://127.0.0.1:8317/v1/models
curl -sS -H "Authorization: Bearer $MANAGEMENT_KEY" http://127.0.0.1:8317/v0/management/auth-files
curl -sS -H "Authorization: Bearer $MANAGEMENT_KEY" \
  "http://127.0.0.1:8317/v0/management/plugins/cursor-native/quota?auth_index=$AUTH_INDEX"
```

The browser quota page is:

```text
/v0/resource/plugins/cliproxyapi-cursor-native/quota
```

Its management key is stored only in that tab's `sessionStorage`; a 401 clears it, and closing the tab discards it.

## Rollback

Disable `plugins.configs.cliproxyapi-cursor-native.enabled`, restart CLIProxyAPI, and confirm existing providers still work. Then remove the `.so`. Preserve the Cursor auth files until rollback is accepted; delete them only when you intentionally want to revoke the local integration.
