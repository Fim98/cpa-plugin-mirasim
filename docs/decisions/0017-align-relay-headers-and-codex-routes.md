# ADR 0017: Align relay headers and Codex routes

## Status

Accepted — 2026-09-03

## Context

Mirasim 0.0.272 treats `oauth-2025-04-20` as a direct Anthropic OAuth marker. Its relay route removes that one comma-separated value from `anthropic-beta` while preserving other beta features. Forwarding the marker with Mirasim's device ticket can select the wrong upstream authentication behavior; deleting the entire header would disable unrelated caller-requested features.

The official client also recognizes both native Codex paths and their ChatGPT backend aliases. It rewrites `/backend-api/codex/responses` to `/v1/responses` and `/backend-api/codex/alpha/search` to `/v1/alpha/search` before signing and relaying them.

## Decision

1. Remove only the exact `oauth-2025-04-20` comma-separated value from every outbound `anthropic-beta` header. Trim surrounding whitespace, retain other values in order, and remove the header only when no values remain.
2. Normalize the two ChatGPT backend Codex aliases to their `/v1` relay paths in the raw HTTP executor. Preserve the HTTP method, query parameters, headers, and body.
3. Identify both `/v1/responses` and `/v1/alpha/search` as Codex-agent requests in signed relay metadata.
4. Leave native `/v1` paths and all unrelated paths unchanged.

## Consequences

Claude callers retain prompt-caching, long-context, and future beta tokens without leaking the direct OAuth routing marker into Mirasim's ticket-authenticated relay. Codex clients can forward Responses and alpha-search traffic regardless of whether they use the public or ChatGPT backend path spelling, and the rewritten pathname is consistently used for the URL, signature, and encrypted metadata.

Path normalization is intentionally exact. An unknown `/backend-api/codex/*` route is not guessed or silently rewritten.

## Alternatives considered

- Drop the complete `anthropic-beta` header: rejected because it changes unrelated Claude capabilities.
- Forward `oauth-2025-04-20`: rejected because it is a direct Anthropic OAuth marker, not a relay capability.
- Rewrite every `/backend-api/codex/` prefix mechanically: rejected because Mirasim documents only two corresponding relay paths.
