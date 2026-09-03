# ADR 0010: Apply thinking at the Mirasim wire boundary

## Status

Accepted — 2026-09-03; Claude effort and `output_config` decisions superseded by [ADR 0015](0015-forward-claude-adaptive-effort.md)

## Context

CLIProxyAPI represents reasoning controls as a canonical thinking configuration and lets plugins register a `ThinkingApplier`. The Mirasim executor accepts five client protocols, then translates each request to either Anthropic Messages or Codex Responses. Applying a Claude or Codex field before that translation would write the provider-native field into the wrong client schema.

CPA also permits a final model suffix such as `model(high)`, `model(8192)`, `model(auto)`, or `model(none)`. The plugin previously forwarded that suffix as part of the upstream model ID and did not advertise a thinking capability.

Mirasim has an additional compatibility boundary. Its current Messages relay rejects `output_config.effort`, although current Claude models normally use that field with adaptive thinking. Removing the field is required for successful Claude Code forwarding. Silently mapping `low`, `medium`, `xhigh`, or `max` to the default `high` effort would misrepresent the caller's request.

## Decision

1. Register a Mirasim `ThinkingApplier` through both the Go plugin capability and the C ABI.
2. Parse CPA's final-parenthesized suffix and always remove it from the provider model ID. Recognize `none`, `auto`, `-1`, the CPA named levels, and non-negative numeric budgets.
3. Apply suffix controls only after request translation, when the executor has selected the actual Mirasim wire protocol.
4. On Codex Responses, emit `reasoning.effort`. Convert numeric budgets with the same threshold mapping used by CPA.
5. On Claude models that support adaptive thinking, map `auto` and `high` to `thinking.type=adaptive`, and map `none` to `thinking.type=disabled`. Reject other named efforts with HTTP 400 while the relay cannot forward `output_config.effort`.
6. On manual-thinking Claude models, emit `thinking.type=enabled` with `budget_tokens`, enforce the documented minimum of 1024, and keep the budget below `max_tokens`. Reject fixed budgets for known adaptive-only Claude models.
7. Continue removing `output_config` from every Mirasim Messages request.

## Consequences

Model suffixes no longer corrupt upstream model selection. GPT/Codex callers can use named or numeric reasoning controls, and Claude callers can explicitly select adaptive/default-high thinking, disable thinking, or use valid manual budgets on legacy models.

The plugin fails explicitly when the Mirasim relay cannot faithfully represent a Claude effort rather than silently running at another level. If Mirasim begins accepting `output_config.effort`, the Claude applier and model metadata can be expanded without changing the executor routing boundary.

The standalone `ThinkingApplier` chooses the normal wire family from the model ID. The executor remains authoritative for the exceptional case where a GPT model is intentionally sent through a Claude-format Messages request.

## Alternatives considered

- Apply thinking before translation: rejected because the plugin accepts multiple incompatible source schemas.
- Forward `output_config.effort`: rejected because the current Mirasim Messages relay returns HTTP 400 for that field.
- Convert every adaptive Claude level to a fixed token budget: rejected because current adaptive-only Claude models reject manual `budget_tokens`.
- Ignore unsupported Claude levels: rejected because a request for low effort would silently execute at the default high effort.

## References

- [CLIProxyAPI plugin thinking contract](https://github.com/router-for-me/CLIProxyAPI)
- [Claude thinking API](https://platform.claude.com/docs/en/api/http/messages)
- [Claude adaptive-thinking migration guidance](https://platform.claude.com/docs/en/build-with-claude/extended-thinking)
