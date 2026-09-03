# ADR 0015: Forward Claude adaptive effort

## Status

Accepted — 2026-09-03

Supersedes items 5 and 7 of [ADR 0010](0010-apply-thinking-at-the-mirasim-wire-boundary.md).

## Context

The plugin previously removed every Claude `output_config` object because an older relay rejected `output_config.effort`. Mirasim 0.0.272 now reads that field in both Claude-to-Chat-Completions and Claude-to-Responses bridges, and its Claude client offers low, medium, high, xhigh, and max effort. Removing the object therefore discarded a supported control and could also destroy unrelated future `output_config` fields.

CLIProxyAPI's native Claude thinking implementation uses the same adaptive shape: `thinking.type=adaptive` plus `output_config.effort` for a named level, and no explicit effort for auto mode.

## Decision

1. Preserve caller-supplied `output_config` when a request has no model-suffix override.
2. For known adaptive Mirasim Claude models, map low, medium, high, xhigh, and max suffixes to `thinking.type=adaptive` and the same `output_config.effort` value.
3. Map auto to adaptive thinking without an explicit effort, and none to disabled thinking.
4. When changing to auto, none, or a manual token budget, remove only `output_config.effort`; preserve other keys and remove the parent object only when it becomes empty.
5. Advertise the named ladder in model metadata. Claude Haiku 4.5 advertises both its manual budget range and the adaptive ladder because the current Mirasim roster marks it adaptive while the Messages route still accepts explicit budgets.
6. Reject named values outside the advertised CPA-compatible ladder instead of forwarding an ambiguous effort.

## Consequences

Claude clients can select the same effort levels through CPA that Mirasim's official client exposes. Direct Claude requests retain their explicit effort and unrelated output configuration. Existing auto, none, and numeric-budget suffixes continue to produce mutually consistent thinking fields.

Mirasim also exposes an `ultra` UI level, but CPA's model-suffix contract does not currently define it; the plugin therefore stops at `max` rather than introducing a private suffix.

## Alternatives considered

- Keep stripping `output_config`: rejected because it no longer matches the current relay or CPA's native Claude implementation.
- Preserve only `effort` and drop the rest of `output_config`: rejected because it mutates caller-owned fields that the plugin does not need to interpret.
- Treat every Claude model ID as adaptive: rejected because unknown or legacy models may still require manual thinking.
