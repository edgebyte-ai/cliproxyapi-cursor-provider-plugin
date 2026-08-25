# Publishing without credentials

## Public artifacts

Publish only:

- source files tracked by Git;
- the compiled platform `.so` inside a ZIP;
- `checksums.txt`;
- release notes and documentation;
- a Plugin Store registry entry.

Never publish `.runtime`, CLIProxyAPI `auth-dir`, Cursor state databases, access tokens, refresh tokens, management keys, API keys, private hostnames, Tailscale addresses, or production `config.yaml` files.

The repository CI runs tests, the race detector, `go vet`, a Linux plugin build, and a tracked-file credential scan.

## Release workflow

Push a semantic tag such as `v0.2.0`. The release workflow builds the Linux amd64 plugin in GitHub Actions, places only `cliproxyapi-cursor-native.so` at the ZIP root, creates `checksums.txt`, and uploads both artifacts to the GitHub release. Building in CI prevents a local auth directory from entering the archive.

## Third-party Plugin Store

This repository publishes a registry at:

```text
https://raw.githubusercontent.com/edgebyte-ai/cliproxyapi-cursor-native-plugin/main/plugin-store/registry.json
```

Users add it to CLIProxyAPI:

```yaml
plugins:
  enabled: true
  dir: "plugins"
  store-sources:
    - "https://raw.githubusercontent.com/edgebyte-ai/cliproxyapi-cursor-native-plugin/main/plugin-store/registry.json"
```

They can then install and update the plugin through CLIProxyAPI's Plugin Store UI. Credentials are created later by the target CLIProxyAPI browser-login flow and are never downloaded from the store.

## Official Plugin Store

The official store is `router-for-me/CLIProxyAPI-Plugins-Store`. Fork that repository, add the same public metadata and pinned artifact information to `registry.json`, validate the JSON against CLIProxyAPI's current registry schema, and submit a pull request. Keep the plugin marked experimental until the project has broader compatibility evidence.
