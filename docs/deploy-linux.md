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
curl -fLO https://github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/releases/download/v0.1.0/cliproxyapi-cursor-provider_0.1.0_linux_amd64.zip
curl -fLO https://github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/releases/download/v0.1.0/checksums.txt
sha256sum -c checksums.txt
unzip cliproxyapi-cursor-provider_0.1.0_linux_amd64.zip
install -m 755 cliproxyapi-cursor-provider.so /opt/cliproxyapi/plugins/linux/amd64/cliproxyapi-cursor-provider.so
```

Use the release's signed-in-place `checksums.txt` as the checksum authority; do not copy a checksum from chat or a third-party page.

## CLIProxyAPI configuration

Add this block to the existing `config.yaml` without changing unrelated providers:

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cliproxyapi-cursor-provider:
      enabled: true
      priority: 100
      provider_id: "cursor"
      model_prefix: ""
      model_mode: "normalized"
      default_reasoning_effort: "high"
      model_display_names:
        cursor-grok-4.5: "Grok 4.5"
        cursor-grok-4.6: "Grok 4.6"
      cursor_base_url: "https://api2.cursor.sh"
      client_version: "cli-2026.08.11-e8db854"
      allowed_native_tools: ["mcp_tool_call"]
      request_timeout_seconds: 300
      transient_retry_count: 1
      transient_retry_delay_ms: 250
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
plugins/linux/amd64/cliproxyapi-cursor-provider.so
```

Restart only the target CLIProxyAPI instance after reviewing the config. Verify the log contains both `plugin loaded` and `plugin registered` for `cliproxyapi-cursor-provider`.

## Add Cursor accounts without copying tokens

Start the plugin login flow through the target CLIProxyAPI Management API:

```sh
curl -sS \
  -H "Authorization: Bearer $MANAGEMENT_KEY" \
  http://127.0.0.1:8317/v0/management/cursor-auth-url
```

Open the returned Cursor URL in your browser. Poll the returned state until it reports `ok`:

```sh
curl -sS \
  -H "Authorization: Bearer $MANAGEMENT_KEY" \
  "http://127.0.0.1:8317/v0/management/get-auth-status?state=$STATE"
```

Repeat for the second Cursor account. The plugin writes separate `0600` auth JSON files under `auth-dir`. Never paste access or refresh tokens into `config.yaml`.

Open **Cursor Quota** from the management sidebar to edit each account's priority, prefix, allowed-model rules, and denied-model rules. The editor has no built-in policy presets and never returns or displays account tokens.

The same operation is available through the plugin's authenticated Management API:

```sh
curl -sS -X PATCH \
  -H "Authorization: Bearer $MANAGEMENT_KEY" \
  -H "Content-Type: application/json" \
  "http://127.0.0.1:8317/v0/management/auth-files/fields" \
  -d '{"name":"cursor-account.json","priority":0,"prefix":"","allowed_models":[],"denied_models":[]}'
```

## Verification

Check models and each account's quota:

```sh
curl -sS -H "Authorization: Bearer $API_KEY" http://127.0.0.1:8317/v1/models
curl -sS -H "Authorization: Bearer $MANAGEMENT_KEY" http://127.0.0.1:8317/v0/management/auth-files
curl -sS -H "Authorization: Bearer $MANAGEMENT_KEY" \
  "http://127.0.0.1:8317/v0/management/plugins/cursor-provider/quota?auth_index=$AUTH_INDEX"
```

The browser quota page is:

```text
/v0/resource/plugins/cliproxyapi-cursor-provider/quota
```

Its management key is stored only in that tab's `sessionStorage`; a 401 clears it, and closing the tab discards it.

## Rollback

Disable `plugins.configs.cliproxyapi-cursor-provider.enabled`, restart CLIProxyAPI, and confirm existing providers still work. Then remove the `.so`. Preserve the Cursor auth files until rollback is accepted; delete them only when you intentionally want to revoke the local integration.
