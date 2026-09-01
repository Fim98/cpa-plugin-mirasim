# Mirasim Provider Plugin

This plugin adds Mirasim provider support to CLIProxyAPI through its native plugin ABI. It ports the runtime behavior of `mira2api` into the host process: project-local plaintext credentials, token refresh, Ed25519 device signatures, device tickets, dynamic models, Messages/Responses forwarding, protocol translation, streaming, and Mirasim's response-header rate-limit signals.

The implementation follows the current [CLIProxyAPI plugin contract](https://github.com/router-for-me/CLIProxyAPI) and the packaging pattern used by [cpa-plugin-gemini-cli](https://github.com/router-for-me/cpa-plugin-gemini-cli).

## Capabilities

- Imports an existing `.mirasim-credentials` directory without copying tokens or private-key material into CLIProxyAPI auth JSON.
- Refreshes access and rotated refresh tokens through `POST /auth/refresh` and writes them back with restricted file permissions where supported.
- Mints and caches 15-minute device tickets through `POST /v1/device/session`.
- Signs every relay request with the `mrs-sig-v1` Ed25519 protocol.
- Removes a leading `mirasim/` model prefix and removes unsupported Claude `output_config` fields.
- Retries one upstream HTTP 401 with a fresh device ticket.
- Loads the live model catalog from `GET /v1/models`, with a static eight-model fallback for startup discovery.
- Accepts and emits CLIProxyAPI's `openai`, `openai-response`, `claude`, `gemini`, and `codex` formats.
- Preserves streaming SSE and translates tool definitions, tool selection, tool calls, and tool continuations through CLIProxyAPI's built-in translators.
- Exposes the Mirasim rate-limit signals through a read-only plugin management route.

## Protocol routing

Mirasim does not currently expose a usable raw Chat Completions upstream. The plugin therefore selects one of the two verified wire protocols:

| Request | Mirasim wire route |
|---|---|
| Any `claude-*` model | `POST /v1/messages` |
| Any Claude-format client request | `POST /v1/messages` |
| GPT model from OpenAI Chat, Responses, Gemini, or Codex format | `POST /v1/responses` using the Codex wire shape |

Codex Responses uses upstream SSE even for a non-streaming downstream request. For non-streaming callers, the plugin collects the terminal `response.completed` or `response.incomplete` event and returns one translated JSON response.

Catalog presence is not proof that every model is currently routable. Relay capacity and accepted request shape remain time-sensitive upstream behavior.

## Requirements

- CLIProxyAPI `v7.2.146` or a compatible plugin ABI/schema release.
- Go 1.26 or later.
- A C compiler supported by Go's `c-shared` build mode.
- An existing plaintext credential directory containing at least:

  - `refresh-token.txt`
  - `device-private-key.pem`
  - optionally `access-token.txt` (the plugin creates or refreshes it when needed)

The credential directory can be produced by the existing `mira2api` export workflow. This plugin intentionally does not read DPAPI/AES-encrypted Mirasim state and does not fall back to `%USERPROFILE%\.mirasim`.

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
- `plugins/mirasim-v0.1.0.dll`
- `plugins/linux/amd64/mirasim-v0.1.0.so`

Enable dynamic plugins and configure Mirasim in CLIProxyAPI's `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    mirasim:
      enabled: true
      credential-dir: C:\path\to\mira2api\.mirasim-credentials
      relay-url: https://mirasim-relay.mirofish.ai
      admin-url: https://admin.test.mirofish.ai
      client-version: 0.0.146
```

The endpoint and client-version fields are optional and default to the values shown above. `MIRASIM_CREDENTIAL_DIR`, `MIRASIM_RELAY_URL`, `MIRASIM_ADMIN_URL`, and `MIRASIM_CLIENT_VERSION` provide process-level defaults; explicit plugin configuration wins.

Import the directory as a CLIProxyAPI auth record:

```powershell
.\CLIProxyAPI.exe -config .\config.yaml --mirasim-import `
  --mirasim-credential-dir 'C:\path\to\mira2api\.mirasim-credentials'
```

The saved auth JSON contains the resolved directory and public endpoint configuration only:

```json
{
  "type": "mirasim",
  "credential_dir": "C:\\path\\to\\mira2api\\.mirasim-credentials",
  "relay_url": "https://mirasim-relay.mirofish.ai",
  "admin_url": "https://admin.test.mirofish.ai",
  "client_version": "0.0.146"
}
```

No access token, refresh token, or private key is copied into that file.

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

## Security boundary

- Native plugins are trusted in-process code.
- The plaintext credential directory is the source of truth and must never be committed, uploaded, or attached to an issue.
- Relay requests use CLIProxyAPI's host HTTP client so host transport and request lifecycle policies remain active.
- Token refresh uses a private 60-second HTTP client because sending the refresh-token JSON body through the host request logger could persist a long-lived secret. The private client follows standard proxy environment variables, but it cannot use a CLIProxyAPI auth-specific proxy setting.
- Incoming `Authorization`, `Proxy-Authorization`, and `X-Api-Key` values are removed before Mirasim authentication headers are injected.

The architectural rationale and alternatives are recorded in [ADR 0001](docs/decisions/0001-mirasim-provider-boundaries.md).

## License

This project is licensed under the [MIT License](LICENSE).
