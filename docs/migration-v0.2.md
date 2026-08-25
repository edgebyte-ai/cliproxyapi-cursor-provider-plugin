# v0.2 naming migration

v0.2.0 is intentionally incompatible with the experimental v0.1.x naming.

| v0.1.x | v0.2.0 |
|---|---|
| `cliproxyapi-cursor-native` | `cliproxyapi-cursor-provider` |
| `cursor-native` provider/auth type | `cursor-provider` provider/auth type |
| `cliproxyapi-cursor-native.so` | `cliproxyapi-cursor-provider.so` |

The quota group keys do not change:

```text
cursor-native
other-models
```

Before installing v0.2.0, disable and remove the v0.1.x plugin configuration and binary. Change private Cursor auth files from `"type":"cursor-native"` to `"type":"cursor-provider"`, or log in again through `/v0/management/cursor-provider-auth-url`. Never publish or attach those auth files to an issue.

Do not load both plugin versions in one CLIProxyAPI process.
