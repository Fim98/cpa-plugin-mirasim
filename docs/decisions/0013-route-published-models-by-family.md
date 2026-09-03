# ADR 0013: Route published models by family

## Status

Accepted — 2026-09-03

Supersedes routing item 3 of [ADR 0012](0012-publish-claude-and-gpt-models.md).

## Context

The initial GPT publication kept Claude-format requests on the Messages wire even when the selected model ID started with `gpt-`. Mirasim 0.0.272 instead selects its upstream protocol from the model family: GPT models use Codex Responses and Claude models use Anthropic Messages. CLIProxyAPI already provides the Claude-to-Codex translator needed to preserve a Claude client's request and response contract.

## Decision

1. Route every published `gpt-*` model through `POST /v1/responses`, regardless of the downstream client format.
2. Route every published `claude-*` model through `POST /v1/messages`.
3. Use the source format only as a fallback for unknown model families; an unknown model from a Claude-format client remains on Messages.

## Consequences

Claude Code can select a published Mirasim GPT model without causing the plugin to send that model to the Messages upstream. Its request is translated to Codex Responses, and the response is translated back to Claude format. Known model routing now matches the official Mirasim client and is independent of the caller protocol.

## Alternatives considered

- Prefer the downstream client protocol: rejected because it makes the same GPT model use a different Mirasim upstream depending on the caller.
- Reject GPT names from Claude-format clients: rejected because CPA's translator can represent this bridge and Mirasim's official client supports model-family routing.
