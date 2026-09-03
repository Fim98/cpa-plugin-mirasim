# Mirasim Provider Plugin

This plugin adds Mirasim provider support to CLIProxyAPI through its native plugin ABI. It ports the runtime behavior of `mira2api` into the host process: browser OAuth login, CPA-managed auth storage, token refresh, Ed25519 device signatures, encrypted relay metadata, device tickets, dynamic models, Messages/Responses forwarding, protocol translation, streaming, and Mirasim's response-header rate-limit signals.

The implementation follows the current [CLIProxyAPI plugin contract](https://github.com/router-for-me/CLIProxyAPI) and the packaging pattern used by [cpa-plugin-gemini-cli](https://github.com/router-for-me/cpa-plugin-gemini-cli).

## Capabilities

- Supports Management Center browser OAuth and local command-line OAuth with GitHub or Google, matching Mirasim's current sign-in providers.
- Verifies each completed OAuth login by minting a device ticket and reading `GET /v1/models` before returning credentials to CPA.
- Returns one self-contained provider auth JSON after OAuth, including the access token, refresh token, and generated Ed25519 private key; CLIProxyAPI persists it under its configured `auth-dir`, matching its built-in providers and the Gemini CLI plugin.
- Versions that self-contained OAuth JSON with `storage_version`; unversioned self-contained records migrate lazily through CPA's normal refresh save, while path-only credential records remain intentionally unsupported.
- Exposes OAuth tokens plus conventional `expired` and `last_refresh` timestamps in CPA's runtime auth metadata so scheduled and 401-triggered refreshes use the host's per-auth lock, persistence, and retry lifecycle. `RefreshAuth` returns rotated credentials for CPA to save atomically.
- Mints and caches 15-minute device tickets through `POST /v1/device/session`.
- Signs requests with the `mrs-sig-v2` Ed25519 protocol and binds the current bearer credential, client version, metadata, and body.
- Seals normal relay-request signature and session metadata into `x-mirasim-enc` with `mrs-seal-v1` (X25519, HKDF-SHA256, and ChaCha20-Poly1305).
- Removes a leading `mirasim/` model prefix and removes unsupported Claude `output_config` fields.
- Retries one relay HTTP 401 with a fresh device ticket, then delegates credential refresh and request retry to CPA.
- Loads the live model catalog from `GET /v1/models`, publishes both `claude-*` and `gpt-*` entries, and uses the five verified Claude plus three verified GPT models as the static startup fallback.
- Enriches known live and fallback models with family, display name, context/output limits, generation methods, supported parameters, and relay-accurate thinking metadata.
- Accepts and emits CLIProxyAPI's `openai`, `openai-response`, `claude`, `gemini`, and `codex` formats.
- Implements CPA model-suffix thinking controls after protocol translation: Codex `reasoning.effort`, Claude adaptive on/off, and legacy Claude token budgets. Requests the relay cannot represent faithfully return HTTP 400 instead of silently changing effort.
- Preserves streaming SSE and translates tool definitions, tool selection, tool calls, and tool continuations through CLIProxyAPI's built-in translators.
- Exposes the Mirasim rate-limit signals through a read-only plugin management route and ships a companion Management Center adapter for `management.html#/quota`.

## Protocol routing

Mirasim does not currently expose a usable raw Chat Completions upstream. The plugin therefore selects one of the two verified wire protocols:

| Request | Mirasim wire route |
|---|---|
| Any `claude-*` model | `POST /v1/messages` |
| Any `gpt-*` model, including one selected through Claude Code | `POST /v1/responses` using the Codex wire shape |
| An unknown model from a Claude-format client | `POST /v1/messages` |

Codex Responses uses upstream SSE even for a non-streaming downstream request. For non-streaming callers, the plugin collects the terminal `response.completed` or `response.incomplete` event and returns one translated JSON response.

Catalog presence is not proof that every model is currently routable. Relay capacity and accepted request shape remain time-sensitive upstream behavior.

Both model families are registered with CLIProxyAPI. Claude models use Messages; GPT models use the real Codex Responses request shape. The GPT publication decision supersedes the earlier Claude-only rollout boundary; see [ADR 0012](docs/decisions/0012-publish-claude-and-gpt-models.md).

## Thinking controls

CPA-style model suffixes are removed before the request reaches Mirasim. Examples include `gpt-5.6-sol(high)`, `gpt-5.6-terra(8192)`, `claude-sonnet-5(auto)`, and `claude-haiku-4-5(2048)`.

- GPT models sent through Codex Responses receive `reasoning.effort`; numeric budgets are mapped to CPA's named effort thresholds.
- Adaptive Claude models accept `(auto)`, `(high)`, and `(none)`. The relay currently rejects Claude `output_config.effort`, so other named efforts return a clear HTTP 400 rather than silently becoming the default high effort.
- Manual-thinking Claude models receive `thinking.type=enabled` plus `budget_tokens`; the plugin enforces the Anthropic minimum and the `budget_tokens < max_tokens` constraint.

The protocol boundary and rejected alternatives are recorded in [ADR 0010](docs/decisions/0010-apply-thinking-at-the-mirasim-wire-boundary.md).

## Authentication envelope

Mirasim 0.0.272 uses two related authentication flows:

| Request | Bearer credential | Mirasim headers |
|---|---|---|
| `POST /v1/device/session` | Access token | Plain `mrs-sig-v2` signature headers |
| Normal relay request | Device ticket | `x-mirasim-client` plus sealed `x-mirasim-enc` |

Normal requests include generated session, agent, and call metadata in the v2 signature before encryption. Incoming client-supplied `x-mirasim-*` headers are discarded. The signature uses the pathname only; query parameters are forwarded but are not part of the signature or seal associated data.

The bundled relay X25519 public key was reverified against Mirasim 0.0.272. `MIRASIM_SEAL_PUBKEY` can override that public key at process level when the relay rotates it; the value must be standard base64 encoding of exactly 32 bytes. Invalid keys fail closed rather than exposing metadata.

## Requirements

- CLIProxyAPI `v7.2.146` or a compatible plugin ABI/schema release.
- Go 1.26 or later.
- A C compiler supported by Go's `c-shared` build mode.
- A persistent, private, writable CLIProxyAPI `auth-dir`.

Mirasim credentials are obtained only through this plugin's OAuth flow. The plugin does not read a `mira2api` credential directory, DPAPI/AES-encrypted Mirasim state, or `%USERPROFILE%\.mirasim`.

## Build

Linux:

```bash
go test ./...
go vet ./...
go build -trimpath -buildmode=c-shared -o dist/mirasim.so ./cmd/mirasim
```

Windows PowerShell (with a compatible GCC toolchain on `PATH`):

```powershell
go test ./...
go vet ./...
go build -trimpath -buildmode=c-shared -o dist/mirasim.dll ./cmd/mirasim
```

The generated `.h` file is not needed by CLIProxyAPI. Tagged releases are built and packaged by the included GitHub Actions workflow.

## Install and configure

Copy the platform library into CLIProxyAPI's plugin directory. Both unversioned and versioned names are supported, for example:

- `plugins/mirasim.dll`
- `plugins/mirasim-v0.6.0.dll`
- `plugins/linux/amd64/mirasim-v0.6.0.so`

Enable dynamic plugins and configure Mirasim in CLIProxyAPI's `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    mirasim:
      enabled: true
      relay-url: https://relay.mirasim.ai
      admin-url: https://auth.mirasim.ai
      client-version: 0.0.272
      oauth-public-base-url: https://cpa.example.com
```

The endpoint and client-version fields are optional and default to the values shown above. `oauth-public-base-url` is optional only when the browser can reach CPA at the loopback URL generated by the host. Set it to the externally reachable HTTPS origin for a remote or Docker deployment; a path prefix is allowed when a reverse proxy strips that prefix. Non-loopback HTTP callback origins are rejected.

`MIRASIM_RELAY_URL`, `MIRASIM_ADMIN_URL`, `MIRASIM_CLIENT_VERSION`, and `MIRASIM_OAUTH_PUBLIC_BASE_URL` provide process-level defaults; explicit plugin configuration wins. Existing auth records that explicitly contain old endpoint or `client_version` values must also be updated.

## OAuth login

### Management Center

Open the Mirasim OAuth action in Management Center. CPA calls `/v0/management/mirasim-auth-url`, and the returned page lets you choose GitHub or Google. The browser then follows Mirasim's current flow:

1. Mirasim redirects back with `state`, `access_token` (or `token`), and `refresh_token` query parameters.
2. A one-time plugin resource callback validates the 256-bit state and keeps the tokens only in bounded process memory for at most three minutes.
3. CPA's existing `/v0/management/get-auth-status` polling invokes the plugin, which generates an Ed25519 device key and validates the complete token/signature/ticket chain against `GET /v1/models`.
4. After validation, the plugin returns self-contained `StorageJSON`. CPA saves it under `auth-dir` as `mirasim-<account-id>.json`, or `mirasim-<device-fingerprint>.json` when the validated token has no stable account claim.

The generic CPA callback stores authorization codes only, so this plugin uses its own `/v0/resource/plugins/<plugin-id>/oauth/callback` resource for Mirasim's direct-token callback. No CLIProxyAPI core patch is required. The callback immediately redirects the browser to a token-free URL after accepting it.

For a remote Docker installation, the public callback URL must reach the same CPA container through the reverse proxy. Do not set `oauth-public-base-url` to `127.0.0.1` unless the browser and CPA actually run on the same machine.

If the CPA container cannot connect to `auth.mirasim.ai`, configure CPA's top-level `proxy-url`. The plugin remembers that host proxy for private `/auth/refresh` calls while keeping the refresh-token body outside CPA's request-recording bridge.

### Command line

For a local interactive CPA process:

```powershell
.\CLIProxyAPI.exe -config .\config.yaml --mirasim-login `
  --mirasim-login-provider github
```

`google` is also accepted. The plugin starts a random loopback callback, opens the browser unless the host's `--no-browser` flag is set, and offers a pasted-callback fallback after 15 seconds. Prefer Management Center for detached Docker containers.

OAuth produces the following auth shape. Secret values are abbreviated below:

```json
{
  "storage_version": 1,
  "type": "mirasim",
  "access_token": "<access-token>",
  "refresh_token": "<refresh-token>",
  "expired": "2026-09-03T13:00:00Z",
  "last_refresh": "2026-09-03T12:00:00Z",
  "account_id": "<validated-account-id-if-present>",
  "email": "<validated-email-if-present>",
  "device_private_key": "<Ed25519-PKCS8-PEM>",
  "relay_url": "https://relay.mirasim.ai",
  "admin_url": "https://auth.mirasim.ai",
  "client_version": "0.0.272",
  "auth_kind": "oauth"
}
```

There is no directory-path fallback or import path. After upgrading from a path-only release, delete the obsolete Mirasim auth entry and complete OAuth login again.

Multiple Mirasim accounts can coexist in the same CPA `auth-dir`. Repeating OAuth for an account with the same stable claim replaces that account's file; accounts without a stable claim use their generated device identity and therefore receive separate files.

An unversioned self-contained Mirasim OAuth JSON is accepted and normalized to `storage_version: 1` in runtime. CPA writes the normalized form on the next scheduled or 401-triggered refresh. A higher or malformed version is rejected rather than guessed. This migration does not restore the removed directory/import workflow; see [ADR 0011](docs/decisions/0011-version-oauth-storage-and-publish-model-capabilities.md).

## Rate-limit signals

Mirasim does not expose a separate `/quota` or `/usage` JSON API. The plugin refreshes `GET /v1/models` and returns these response headers:

- `anthropic-ratelimit-unified-5h-utilization`
- `anthropic-ratelimit-unified-5h-reset`
- `anthropic-ratelimit-unified-7d-utilization`
- `anthropic-ratelimit-unified-7d-reset`

With CLIProxyAPI's Management API enabled, call:

```text
GET /v0/management/mirasim/quota
GET /v0/management/mirasim/quota?auth_index=<runtime-auth-index>
```

The first form works when exactly one Mirasim auth is loaded. The response includes an `available` flag, the raw header values, parsed UTC reset times, observation time, and model count. If a fresh model response omits the headers, `available` is false and the plugin does not reuse a stale snapshot. These fields are rate-limit signals, not billing usage. The endpoint uses CLIProxyAPI's normal Management API authentication and returns `Cache-Control: no-store`.

### Management Center quota page

The stock Management Center has a compile-time quota-provider registry, so a native plugin route alone cannot inject a card into `management.html#/quota`. Build the companion panel:

```powershell
.\scripts\build-management-center.ps1
```

This produces `dist/management.html` from a pinned upstream Management Center revision. The patched panel recognizes Mirasim auth records, adds a Mirasim tab/card, calls the plugin quota route with the runtime `auth_index`, and displays the 5-hour and 7-day remaining capacity and reset times. It keeps Mirasim separate from native Claude OAuth credentials.

For Docker Compose, persist the custom panel with a read-only bind mount alongside the plugin mount:

```yaml
services:
  cpa:
    volumes:
      - ./plugins:/CLIProxyAPI/plugins
      - ./management.html:/CLIProxyAPI/static/management.html:ro
```

Also disable CLIProxyAPI's upstream Management Center auto-updater so it does not try to replace the pinned custom panel:

```yaml
remote-management:
  disable-auto-update-panel: true
```

The frontend integration and its upgrade boundary are documented in [ADR 0004](docs/decisions/0004-integrate-quota-with-management-center.md).

## Security boundary

- Native plugins are trusted in-process code.
- The CPA auth file is now the source of truth and contains bearer tokens plus the Ed25519 private key. Persist and back up `auth-dir`, restrict access to the CPA service account, and never commit, upload, or attach these files to an issue.
- Relay requests use CLIProxyAPI's host HTTP client so host transport and request lifecycle policies remain active.
- Token refresh uses a private 60-second HTTP client because sending the refresh-token JSON body through the host request logger could persist a long-lived secret. It honors CPA's configured upstream proxy (or standard proxy environment variables when none is configured) without persisting the proxy URL in Mirasim auth JSON.
- Mirasim's OAuth service returns bearer tokens in callback query parameters. CLIProxyAPI masks token-named query values in its own logs; any reverse proxy in front of CPA must also redact or omit callback query strings.
- Incoming `Authorization`, `Proxy-Authorization`, and `X-Api-Key` values are removed before Mirasim authentication headers are injected.
- Incoming `x-mirasim-*` values are removed, and ordinary relay metadata is sent only inside `x-mirasim-enc`.

The provider boundaries are recorded in [ADR 0001](docs/decisions/0001-mirasim-provider-boundaries.md), the v2 authentication design in [ADR 0003](docs/decisions/0003-adopt-mirasim-v2-authentication-envelope.md), the quota-page integration in [ADR 0004](docs/decisions/0004-integrate-quota-with-management-center.md), the OAuth design in [ADR 0005](docs/decisions/0005-implement-mirasim-oauth-login.md), the CPA-managed credential-storage decision in [ADR 0006](docs/decisions/0006-store-credentials-in-cpa-auth-json.md), account-specific validated persistence in [ADR 0009](docs/decisions/0009-validate-oauth-and-name-auths-by-account.md), the thinking boundary in [ADR 0010](docs/decisions/0010-apply-thinking-at-the-mirasim-wire-boundary.md), versioned storage/model metadata in [ADR 0011](docs/decisions/0011-version-oauth-storage-and-publish-model-capabilities.md), and GPT publication in [ADR 0012](docs/decisions/0012-publish-claude-and-gpt-models.md).

## License

This project is licensed under the [MIT License](LICENSE).
