# ADR 0012: Publish Claude and GPT models

## Status

Accepted — 2026-09-03

Supersedes [ADR 0002](0002-publish-claude-only-model-catalog.md).

## Context

ADR 0002 intentionally hid GPT models during the first rollout while retaining their executor. Subsequent compatibility work verified the real Codex Responses request shape for Mirasim's three GPT 5.6 models, including normal completion and tool-continuation translation. The user has now authorized GPT publication.

Mirasim's live `GET /v1/models` catalog remains account- and time-dependent. Static startup discovery is still needed before an OAuth-bound live catalog has loaded, but it must not publish unrelated catalog families whose protocol paths have not been implemented.

## Decision

1. Publish model IDs whose trimmed, case-insensitive value starts with either `claude-` or `gpt-` from authenticated live discovery.
2. Add `gpt-5.6-luna`, `gpt-5.6-sol`, and `gpt-5.6-terra` to the static fallback alongside the five established Claude models.
3. Keep the existing routing contract: Claude models and Claude-format client requests use `POST /v1/messages`; GPT requests from OpenAI Chat, Responses, Gemini, or Codex clients use `POST /v1/responses` with the Codex wire shape.
4. Do not expose Chat Completions as Mirasim's upstream GPT protocol. OpenAI Chat clients are translated to Codex Responses inside the plugin.
5. Continue filtering model families other than `claude-*` and `gpt-*` until a concrete protocol path is implemented and verified.
6. Release the expanded catalog as plugin version 0.6.0.

## Consequences

CLIProxyAPI `/v1/models` consumers and ordinary model selectors can now discover both Mirasim Claude and GPT models. GPT requests participate in the same OAuth credential selection, device-ticket authentication, encrypted relay envelope, streaming, translation, refresh, and quota-signal lifecycle as Claude requests.

Static presence remains a startup fallback, not a promise of live capacity. Authenticated discovery uses the current Mirasim catalog, and an execution can still return a time-sensitive upstream capacity error.

Claude-format clients that reject GPT model names locally cannot use those names merely because CPA publishes them. OpenAI Responses, Codex, Gemini, or OpenAI Chat clients can select the GPT entries, subject to their own model-name behavior.

## Alternatives considered

- Keep GPT executable but hidden: superseded by the explicit request to publish it.
- Route GPT through Chat Completions: rejected because Mirasim's verified GPT path is Codex Responses.
- Publish every live catalog entry: rejected because catalog presence alone does not establish a working executor protocol.
