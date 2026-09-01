# ADR 0002: Publish a Claude-only model catalog

## Status

Accepted — 2026-09-01

## Context

Mirasim's upstream `GET /v1/models` catalog can contain both Claude and GPT models. The plugin already implements the Claude Messages and GPT/Codex Responses execution paths, but exposing a model in CLIProxyAPI makes it available for normal client selection before that family is intended for public use.

The current rollout should expose only the Claude family without deleting the dormant GPT/Codex implementation needed for later compatibility work.

## Decision

Filter both plugin model-discovery surfaces to IDs whose trimmed, case-insensitive value starts with `claude-`:

- The static startup fallback contains only the five known Claude models.
- Per-auth discovery filters the live Mirasim catalog before returning it to CLIProxyAPI.
- The executor and its GPT/Codex protocol tests remain in place, but GPT model IDs are not advertised.

This is a catalog-visibility boundary, not an executor authorization boundary. A caller that manually supplies a GPT model ID may still reach the retained execution path.

## Consequences

CLIProxyAPI `/v1/models` consumers and ordinary model selectors see only Claude models from this provider, even when the Mirasim upstream catalog also contains GPT entries.

Restoring GPT visibility later requires changing the explicit model filter and fallback list; it does not require rebuilding the protocol implementation.

## Alternatives considered

- Delete the GPT/Codex executor: rejected because visibility is temporary and the tested implementation is useful for a later rollout.
- Return the full live catalog but remove GPT only from the fallback list: rejected because authenticated discovery would still expose GPT models.
- Reject GPT IDs inside the executor: rejected because the requested boundary is model publication rather than an access-control policy.
