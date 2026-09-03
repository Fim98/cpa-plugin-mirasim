# ADR 0016: Track the current Mirasim model roster

## Status

Accepted — 2026-09-03

Supersedes item 5 of [ADR 0011](0011-version-oauth-storage-and-publish-model-capabilities.md) and item 2 of [ADR 0012](0012-publish-claude-and-gpt-models.md).

## Context

Mirasim 0.0.272 publishes `claude-fable-5-1` and `claude-opus-4-6` in addition to the five Claude and three GPT models already represented by the plugin. Its bundled definitions give both new Claude models a 1,000,000-token context and 128,000-token output limit. Claude Fable 5.1 is adaptive-only, while Claude Opus 4.6 also supports an explicit thinking budget on the Messages route.

The authenticated `GET /v1/models` response remains the authority for which models an account can use, but CLIProxyAPI also needs a static catalog before OAuth-bound discovery is available.

## Decision

1. Add `claude-fable-5-1` and `claude-opus-4-6` to the static startup fallback.
2. Publish their verified context, output, route, parameter, and thinking capabilities. Use CLIProxyAPI's built-in Claude Opus 4.6 release timestamp; leave Fable 5.1's creation time unset because Mirasim does not publish one.
3. Keep live identity fields authoritative when authenticated discovery supplies `object`, `created`, or `owned_by`.
4. Continue giving unknown live Claude and GPT IDs conservative family metadata rather than guessing limits or capabilities.

## Consequences

Startup discovery now contains seven Claude models and three GPT models, matching the current Mirasim roster. Claude Fable 5.1 exposes adaptive effort; Claude Opus 4.6 exposes adaptive effort and manual thinking budgets.

The fallback is not a capacity guarantee. Live discovery may omit a fallback model for a particular account, and a later Mirasim release can add a model before this static table is updated.

## Alternatives considered

- Publish the two IDs without capability metadata: rejected because CPA clients would not know their context, output, or thinking contract.
- Copy creation timestamps from neighboring models: rejected because those values would be invented rather than source-backed.
- Replace live discovery with the static roster: rejected because availability remains account- and time-dependent.
