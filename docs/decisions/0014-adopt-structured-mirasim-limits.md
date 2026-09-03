# ADR 0014: Adopt structured Mirasim limits

## Status

Accepted — 2026-09-03

Supersedes quota-source and fixed-window items 1, 5, and 6 of [ADR 0004](0004-integrate-quota-with-management-center.md).

## Context

The first quota integration predated Mirasim's structured endpoint and inferred two windows from rate-limit headers on `GET /v1/models`. Mirasim 0.0.272 now uses signed `GET /v1/limits` with `x-mirasim-probe: usage`. Its response can contain arbitrary windows, raw budgets and usage, model-scoped limits, paid-plan state, and a degraded-data flag. The official client falls back to a signed one-token Messages probe only when the structured endpoint reports compatibility status 405 or 420.

## Decision

1. Make signed `GET /v1/limits` the authoritative quota source.
2. Accept only well-formed windows with a name and finite `budget` and `used` values. Derive bounded used/remaining percentages and status while preserving raw units.
3. Prefer non-model-scoped windows when deriving the overall quota status, matching the official client.
4. On status 405 or 420, send the official minimal paid-model Messages probe and parse the four legacy unified rate-limit headers even when the probe itself is rate-limited.
5. Keep the management response backward compatible by retaining the legacy `five_hour` and `seven_day` fields for fallback results, while adding a structured `windows` array.
6. Update the companion Management Center patch to render arbitrary structured windows and retain the old fixed-window adapter as a fallback.

## Consequences

The quota card no longer depends on model discovery carrying rate-limit headers. New model-scoped or differently named windows appear without another backend schema change, and operators can inspect raw usage units as well as percentages. The fallback probe can make a minimal billable relay request only when the structured endpoint is unavailable; this matches Mirasim's official behavior.

## Alternatives considered

- Continue reading only `/v1/models`: rejected because it loses current structured fields and can report quota as unavailable when the official client has data.
- Always execute both quota paths: rejected because it creates an unnecessary model request and can consume relay capacity.
- Return the upstream JSON without validation: rejected because the management API needs stable percentages, timestamps, and safe bounded fields.
