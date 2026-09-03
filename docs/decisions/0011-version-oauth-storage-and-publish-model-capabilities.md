# ADR 0011: Version OAuth storage and publish model capabilities

## Status

Accepted — 2026-09-03

## Context

Mirasim OAuth storage evolved from an unversioned self-contained JSON object. Adding token timing and account identity remained backward compatible, but the plugin had no boundary for rejecting a future incompatible file or normalizing renamed fields. This is distinct from the obsolete path-only credential format, which remains unsupported by decision.

The model provider also returned only generic `chat`, `messages`, and text fields. CLIProxyAPI's plugin contract supports context limits, completion limits, family type, display metadata, supported parameters, and thinking capabilities. Missing values prevented the host from reasoning about model capabilities in the same way it does for built-in and Gemini CLI models.

## Decision

1. Add integer `storage_version: 1` to every Mirasim OAuth JSON payload and runtime metadata.
2. Treat a missing version as the unversioned self-contained OAuth schema and migrate it in memory. Accept the legacy `expiry` timestamp alias as `expired`, preserve unrelated host metadata, and remove obsolete directory fields.
3. Reject malformed, negative, or newer storage versions. Do not read, import, or migrate a `credential_dir`; path-only records still fail validation and require OAuth login.
4. Return current-version `StorageJSON` immediately after parsing. CPA persists it through the existing atomic refresh lifecycle, so no plugin-owned file writer is introduced.
5. Maintain a small metadata table for the eight Mirasim catalog models already verified by this project. Prefer live catalog values for `object`, `created`, and `owned_by`, while filling stable capability fields from the current CLIProxyAPI model definitions and the verified Mirasim wire route.
6. Describe Claude models as Anthropic Messages models and GPT models as OpenAI Responses models. Publish context/output limits, supported parameters, and thinking controls, while keeping input/output modalities at text because other modalities have not been verified through Mirasim.
7. Limit adaptive Claude thinking metadata to the controls the Mirasim relay can faithfully carry: dynamic/default-high and disabled. Keep manual token-budget metadata for Claude Haiku 4.5 and named Codex effort metadata for GPT 5.6 models.

## Consequences

Existing self-contained OAuth records continue to load and enter the current runtime schema without a separate migration command. Their physical files are rewritten in current form on the next CPA-managed refresh. Unknown future versions fail closed instead of being interpreted with old assumptions.

CPA receives useful model family, limit, generation-method, parameter, and thinking data from both static startup discovery and authenticated live discovery. Live catalog membership remains the source of truth for account availability; the static table is fallback metadata, not a claim that every listed model has upstream capacity at all times.

The metadata snapshot can drift as Mirasim or model limits change. Updating it requires source verification and tests, while model IDs outside the known table receive conservative family-level metadata without invented limits.

## Alternatives considered

- Migrate path-only credential directories: rejected by the OAuth-only storage decision in ADR 0006.
- Silently accept any future storage version: rejected because credential and device-key interpretation is security-sensitive.
- Copy all underlying provider modalities and controls: rejected because Mirasim relay support has not been verified for each one.
- Import CLIProxyAPI's internal registry package: unavailable to an external Go module by Go's `internal` package boundary and would couple the plugin to host internals.
