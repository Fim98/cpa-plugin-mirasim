# ADR 0001: Mirasim provider boundaries

## Status

Accepted — 2026-08-31. The credential-persistence portions are superseded by [ADR 0006](0006-store-credentials-in-cpa-auth-json.md) as of 2026-09-03.

## Context

`mira2api` proved that the Mirasim relay requires more than a bearer token. Every provider request needs an Ed25519 device signature and a short-lived device ticket minted from a refreshable access token. Its current project-local runtime deliberately reads plaintext credentials from `.mirasim-credentials` and writes token rotations back to that directory.

The relay has two distinct working protocol families:

- Anthropic Messages for Claude models and verified GPT Messages requests.
- The real Codex Responses request shape for GPT models.

Raw Chat Completions is not a working upstream route. Model catalog presence also does not establish route availability. Mirasim's observable limit information is carried by four response headers on `GET /v1/models`; there is no independent quota or usage JSON endpoint.

CLIProxyAPI native plugins execute as trusted in-process code. Provider executors are expected to use the host HTTP client, but the host request recorder can retain request bodies for diagnostics. A token refresh body contains a long-lived refresh token and therefore needs a narrower handling path.

## Decision

1. Store only a resolved credential-directory path and public endpoint settings in CLIProxyAPI auth JSON. Keep access tokens, refresh tokens, and the Ed25519 private key in the existing project-local plaintext directory.
2. Do not add DPAPI/AES export or fallback reads from the user's Mirasim home. Credential export remains a separate, explicit `mira2api` preparation step.
3. Use the CLIProxyAPI host HTTP client for device-ticket minting, model discovery, completions, token counting, and streams.
4. Use a private HTTP client with a 60-second timeout only for `POST /auth/refresh`, preventing the refresh-token body from entering host request logs. Re-read the refresh token from disk immediately before each refresh so another process can rotate it safely.
5. Select the upstream wire route from both model family and source protocol:
   - `claude-*` or Claude source format uses Messages.
   - Other GPT requests use Codex Responses.
6. Use CLIProxyAPI's built-in translators inside the executor for OpenAI Chat, Responses, Claude, Gemini, and Codex input/output. Force Codex upstream requests to stream and aggregate the terminal event for non-streaming callers.
7. Expose a read-only `/v0/management/mirasim/quota` route that refreshes `GET /v1/models` and reports only the four known rate-limit headers plus derived reset timestamps. Label the result as rate-limit state, not billing usage.
8. Remove caller credentials and hop-by-hop headers before adding the Mirasim ticket and signature headers. Retry a single HTTP 401 with a new ticket.

## Consequences

The plugin has no runtime dependency on the Node gateway and participates directly in CLIProxyAPI model selection, translation, streaming, and management routing. It supports real Codex and Claude wire behavior while still accepting the host's other common client formats.

Credential material is not duplicated into auth JSON, but the selected source directory remains plaintext and portable. Any process that can read it can impersonate the Mirasim session. Operators must protect it outside Git as well as inside Git.

The private refresh request follows standard proxy environment variables but cannot inherit an auth-specific proxy configured only inside CLIProxyAPI. This is accepted to keep long-lived refresh-token bodies out of host diagnostic capture. All model traffic continues to use the host transport.

The quota view is intentionally narrow. Missing headers produce an empty snapshot rather than fabricated usage, and a successful model catalog does not imply a successful generation route.

## Alternatives considered

- Keep `mira2api` as a separate local upstream: rejected because it retains another process, port, API-key layer, and failure boundary.
- Store tokens and the private key in CLIProxyAPI auth JSON: rejected because it duplicates plaintext secrets into another persisted and managed surface.
- Send refresh through the host HTTP client: rejected because its diagnostic request capture can include the refresh-token request body.
- Route every request through raw Chat Completions: rejected because that relay route is not currently usable.
- Invent a `/quota` upstream call: rejected because Mirasim exposes only response-header signals on model discovery.
