# Mirasim Provider Plugin

A native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin for Mirasim, with browser/CLI OAuth, automatic token refresh, dynamic models, streaming, tool calls, and quota reporting. Claude models use Messages; GPT models use Responses. CPA stores credentials in its configured `auth-dir`.

## Requirements

- CLIProxyAPI `v7.3.9` or a later plugin ABI/schema release. The plugin reports plugin schema 6, which a host older than `v7.3.0` refuses to load; stay on plugin `v0.7.x` to keep a `v7.2.x` host.
- For source builds: Go 1.26+ and a C compiler supporting `c-shared`.
- A private, persistent, writable CPA `auth-dir`.

## Build

Linux:

```bash
go test ./...
go vet ./...
go build -trimpath -buildmode=c-shared -o dist/mirasim.so ./cmd/mirasim
```

Windows PowerShell, with GCC on `PATH`:

```powershell
.\scripts\build.ps1 -Version 0.7.1
```

The Windows script runs tests and vet before producing `dist/mirasim.dll`. Generated `.h` files are not needed by CPA.

## Install

Copy the platform library into CPA's `plugins` directory and configure `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    mirasim:
      enabled: true
      # Required for remote browser OAuth; see below.
      oauth-public-base-url: https://cpa.example.com
```

Optional settings are `relay-url` (default `https://relay.mirasim.ai`), `admin-url` (default `https://auth.mirasim.ai`), and `client-version` (default `0.0.310`). Explicit configuration overrides the corresponding `MIRASIM_RELAY_URL`, `MIRASIM_ADMIN_URL`, `MIRASIM_CLIENT_VERSION`, and `MIRASIM_OAUTH_PUBLIC_BASE_URL` environment variables.

The running plugin configuration determines `client-version`, including when loading older OAuth files. Existing tokens and device keys remain valid inputs; CPA persists the updated version on its normal auth save/refresh path. Use `client-version` explicitly if an upstream needs a different version.

## OAuth login

Use the Mirasim OAuth action in Management Center and choose a provider returned by `/auth/oauth/providers`. Discovery failures show a retryable error rather than presenting stale login buttons. For a local interactive CPA process:

```powershell
.\CLIProxyAPI.exe -config .\config.yaml --mirasim-login --mirasim-login-provider github
```

Use `google` for Google login, or another provider ID currently offered by the service. CLI login validates the choice using the same discovery endpoint. CPA's `--no-browser` flag is supported. Credentials are validated and saved by CPA; no external credential-directory import is supported.

For remote deployments, set `oauth-public-base-url` to a browser-reachable callback origin. Mirasim may reject an unregistered public callback and may omit OAuth state. In that case, run the included loopback bridge on the browser's machine:

```powershell
go build -o .\dist\mirasim-oauth-bridge.exe .\cmd\mirasim-oauth-bridge
.\dist\mirasim-oauth-bridge.exe --listen 127.0.0.1:18317 --upstream https://cpa.example.com
```

Set the remote plugin's `oauth-public-base-url` to `http://127.0.0.1:18317`, restart CPA, and repeat login with the bridge running. The bridge forwards only OAuth resource requests and restores the pending login state; it is needed only during login. Its optional `--dial-address <IP:port>` pins the upstream connection while retaining HTTPS hostname verification and bypassing environment proxies.

## Email code login

A Mirasim account with no OAuth provider bound to it cannot use any of the flows above. Sign it in with a mailed code instead:

```powershell
.\CLIProxyAPI.exe -config .\config.yaml --mirasim-login --mirasim-login-email you@example.com
```

Mirasim mails a code and the command prompts for it. Where no prompt can be answered, run the same command once to send the code, then again with `--mirasim-login-code <code>` to complete the login without a prompt. This is the CLI only: plugin resource routes are GET-only and carry no request body, so the browser page cannot accept a code. A response without a refresh token is refused rather than saved, because CPA cannot keep such a credential alive.

## Relay collection and metadata

Set `collect: false` to send the official `x-mirasim-collect: off` signal inside signed/encrypted metadata. Omitted or true follows the relay default. `locale` is optional. Environment defaults are `MIRASIM_COLLECT` and `MIRASIM_LOCALE`; explicit YAML wins. This requests upstream behavior; it does not prove how the service retains data.

Only inference routes carry that metadata. `/v1/models`, `/v1/limits` and `/v1/model-roster` describe the account rather than a conversation, so they are signed with empty metadata and sealed nothing, exactly as the official client sends them: no session, agent, sub-account, locale or collection signal is attached. Each inference call also carries its own `x-mirasim-call` identifier.

CPA is asked to refresh the access token a quarter hour before it expires, the same headroom the official client gives a slow or briefly failing `/auth/refresh`. The token stays in use throughout that window and is only refused in the last thirty seconds, so a lagging refresh does not fail requests the relay would have served.

Relay calls are bearer-authorized with a device ticket minted at `/v1/device/session`. A relay that answers 404 or 501 there offers no device signing, so the plugin signs and authorizes with the access token itself and stops asking for one minute (404) or fifteen (501), matching the official client. Requests keep working throughout; only the credential inside the signature changes. Other mint failures still back off and surface, so CPA can rotate the credential.

`x-mirasim-account` carries a sub-account only when the access token names one. An account without that claim sends no account header at all, matching the official client; the local identity used for auth file naming and session scoping is never substituted for it. Host `execution_session_id` values produce stable, account-scoped relay session IDs; a host integration may supply `mirasim_turn_id` in executor metadata for task association. Missing task IDs are omitted. Browser-supplied `x-mirasim-*` headers cannot override these values. Repository paths and Git metadata are not collected by the plugin.

## Model metadata

The fallback catalog includes GPT 6 Astra and GPT 5.6 Sol/Terra/Luna. Their fallback context is 1,050,000 tokens for Astra and 872,000 for the GPT 5.6 models, taken from the roster built into the inspected 0.0.310 client rather than from its narrower model-picker list, with a 128,000-token output limit. These are client metadata, not account-tested capacity guarantees. Claude Haiku remains in the catalog; a desktop toggle does not imply upstream removal.

Model membership comes from the account's `/v1/models`, narrowed the way the official client narrows the same response: reserved placeholders and namespaced IDs are dropped, and a dated twin such as `claude-haiku-4-5-20251001` is dropped when the plain `claude-haiku-4-5` is served beside it. A dated ID with no plain counterpart is kept, since it is the only way to reach that model. Only Claude and GPT models are published, because those are the two wires this plugin speaks; the official client hides the other families the relay lists from its own picker, so nothing servable is withheld. A `max_input_tokens` the catalog reports supersedes the static fallback context above, so the published window is the one this account is actually served. Signed `/v1/model-roster` overlays context/output limits and effort when available. Specs are cached per credential in memory for ten minutes; failures retain that credential's last successful specs, otherwise static defaults apply. The cache is not persisted in auth files and resets on reload. CPA has no `autoCompactRatio` in its model metadata, so callers still control compaction thresholds.

Thinking controls are normalized to that form wherever they arrive from. CPA's parenthesized model suffix is validated and applied as before; a client speaking native Claude that puts `thinking` or `output_config.effort` in the payload instead has the amount carried over to the form the model accepts. A request that says nothing about thinking is forwarded untouched, and controls with no equivalent in the target form are left as sent rather than refused.

The roster's `adaptive` flag is also the only thing that selects a Claude model's upstream thinking form: adaptive models take `thinking.type=adaptive` with `output_config.effort`, non-adaptive models take `thinking.type=enabled` with `budget_tokens`, and each model publishes only the bounds its own form accepts. The form is never inferred from the model name. A Claude model with no roster entry — including one released after this build — keeps the effort form, which is what every Claude model on the relay uses. Request paths read only an already cached roster, so a cold or unreachable roster never delays an inference call.

Claude models with a known context of at least one million tokens also publish `[1m]` selector aliases. For example, `claude-sonnet-5[1m](high)` strips both selectors before forwarding the real model ID, retains high effort, and adds `context-1m-2025-08-07` without losing other beta tokens.

The official client's `ultra` means `max` plus client workflow orchestration. The request it puts on the wire is a `max` request, so `ultra` is accepted and sent as `max` wherever it arrives — model suffix, `output_config.effort`, or `reasoning.effort`. CPA's single-request executor still cannot run the surrounding multi-turn workflow, so `ultra` and `max` produce the same single API call here.

Both mounts share one effort ladder: `low`, `medium`, `high`, `xhigh`, `max`, and `ultra`. An effort outside it, including `minimal` and `off`, returns HTTP 400 naming the ladder rather than being forwarded for the relay to reject. The official client's wider list covers agents this plugin does not speak for.

## Codex compaction

CPA Responses compact requests use `/v1/responses/compact`, including the `/backend-api/codex/responses/compact` alias. This path accepts non-streaming Responses input/output and preserves opaque compaction items. Ordinary Responses completions retain their SSE handling.

## Quota and client validation

The plugin registers as a CPA quota provider, so a Mirasim credential reports
`supports_quota` and a stock Management Center renders it on its own quota page.
The same data is available over the Management API:

```text
POST /v0/management/quota/fetch                 {"auth_index": "<runtime-auth-index>"}
GET  /v0/management/plugins/mirasim/quota?auth_index=<runtime-auth-index>
```

Account-wide windows and model-scoped ones such as `7d_fable` are grouped separately, so one spent model does not read as a spent account. Resetting is reported as unsupported because Mirasim publishes limits and offers no route that clears them.

The plugin's own route remains available:

```text
GET /v0/management/mirasim/quota?auth_index=<runtime-auth-index>
```

The `auth_index` parameter can be omitted there when exactly one Mirasim account is loaded. Quotas come only from `GET /v1/limits`. HTTP 404/405 reports unavailable data; quota checks never trigger inference. Utilization is rounded once to one decimal and then saturates at 99%, matching the official client. Arbitrary windows, including `7d_fable`, are supported. For quota cards in Management Center, build the companion panel with `.\scripts\build-management-center.ps1`; see [panel setup](management-center/README.md) for deployment. The plugin ZIP does not include this panel or the OAuth bridge.

Validate inference with an actual Claude Code or Codex client and correlate the result with CPA logs. A minimal hand-written Messages request can fail even when the real client works. Model catalog presence does not guarantee upstream capacity.

## GitHub Releases

The [workflow](.github/workflows/build.yml), based on [cpa-plugin-gemini-cli](https://github.com/router-for-me/cpa-plugin-gemini-cli), runs tests and vet, then builds Linux/macOS/Windows on amd64 and arm64, plus FreeBSD on amd64.

Push a dotted numeric tag such as `v0.7.1` to GitHub to publish a release. Prerelease/build suffixes are rejected. Release assets are `mirasim_<version>_<os>_<arch>.zip` and `checksums.txt`; each ZIP contains one root-level `mirasim.so`, `mirasim.dylib`, or `mirasim.dll`. All seven archives and their SHA-256 hashes are checked before uploading.

Pull requests and manual branch runs produce Actions artifacts only. Tag runs publish or update the corresponding release using the automatic `GITHUB_TOKEN`; no personal access token is required. Keep the workflow matrix and `PLATFORMS` in `scripts/plugin_store.py` aligned when changing targets.

## Plugin store

[registry.json](registry.json) targets the planned repository `KIDA-MNESIA/cpa-plugin-mirasim`, with author `KIDA-MNESIA` and plugin ID `mirasim`. It omits a fixed version so CPA resolves updates from the latest release. This does not mean the plugin is already officially listed.

After publishing the repository and a successful release, test installation using this additional CPA store source:

```yaml
plugins:
  enabled: true
  dir: plugins
  store-sources:
    - https://raw.githubusercontent.com/KIDA-MNESIA/cpa-plugin-mirasim/main/registry.json
```

Confirm GitHub's **Latest** release is the intended published version, verify its assets, then install, enable, and test OAuth and a real client request. Packaging checks do not prove ABI or runtime compatibility.

Generate submission files with Python 3.11+, using the actual published tag:

```powershell
python scripts/plugin_store.py prepare-submission --repository https://github.com/KIDA-MNESIA/cpa-plugin-mirasim --author KIDA-MNESIA --tag v0.7.1
```

This writes `dist/store/registry.json` and `dist/store/store-pr.md`. Verify the draft's links and record actual test results. Fork [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store), check for a duplicate ID, and append `plugins[0]` to its registry without replacing existing entries. Submit that registry change and the verified PR description. Later updates normally need only a new latest release. If the repository or author changes, regenerate and update the root registry.

Local release checks:

```powershell
python -m unittest discover -s scripts -p test_plugin_store.py -v
python scripts/plugin_store.py verify-release --tag v0.7.1 --directory dist/release
```

Place all seven ZIPs and their `.zip.sha256` sidecars in `dist/release` for the last command; it generates `checksums.txt`.

## Security and license

Native plugins run inside CPA. Protect `auth-dir`: it contains bearer tokens and private keys. Redact OAuth callback query strings in reverse-proxy logs.

Licensed under the [MIT License](LICENSE).

开源技术和开发者交流，欢迎访问 [Linux DO](https://linux.do/)。
