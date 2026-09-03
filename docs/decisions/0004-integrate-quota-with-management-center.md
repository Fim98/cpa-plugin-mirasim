# ADR 0004: Integrate Mirasim quota with Management Center

## Status

Accepted — 2026-09-01; quota-source and fixed-window decisions superseded by [ADR 0014](0014-adopt-structured-mirasim-limits.md)

## Context

The plugin already exposes fresh Mirasim rate-limit signals through `GET /v0/management/mirasim/quota`. CLIProxyAPI's native plugin ABI can register that API route and plugin-owned resource pages, but it cannot inject a provider adapter into the React application served at `management.html`.

The built-in `#/quota` page has a compile-time provider registry. It classifies auth records by provider, invokes a provider-specific fetcher, stores provider-shaped state, and renders provider-specific quota rows. Registering a plugin resource would create a separate `#/plugin-pages/...` page, not the requested quota-page card. CLIProxyAPI's passive `Auth.Quota` observation is also insufficient: the current Management Center quota page actively fetches provider data and does not consume arbitrary plugin quota fields.

## Decision

1. Keep `/v0/management/mirasim/quota` as the authoritative backend. It refreshes signed `GET /v1/models` data, returns only the four known Mirasim rate-limit headers and derived reset timestamps, and remains protected by the normal Management API key.
2. Ship a narrow Management Center patch against pinned upstream commit `e0ee7123dfb5aa89a14ff73ac5a5c3bf4db658e0` plus a reproducible PowerShell build script. Do not vendor the full frontend repository.
3. Add Mirasim as its own quota provider type. Do not relabel the credential as `claude`, because that would route native Claude usage calls through the wrong authentication and executor paths.
4. Fetch quota only when the operator requests a card refresh or page refresh, matching the Management Center's existing upstream-call policy.
5. Treat Mirasim utilization values in the documented ratio form (`0..1`) while tolerating percent-form values (`0..100` or a `%` suffix). Display remaining capacity, and preserve the raw values in the plugin API response.
6. Render the 5-hour and 7-day windows using the existing quota meter, reset countdown, sorting, and timeline contracts. A fresh response without the four headers displays an unavailable/empty state instead of stale or fabricated quota.

## Consequences

Operators deploy two versioned artifacts: the native plugin library and the patched single-file `management.html`. The plugin API remains usable without the patched panel, but Mirasim will not appear on the built-in quota page.

The custom file is mounted at `/CLIProxyAPI/static/management.html`, and deployments set `remote-management.disable-auto-update-panel: true`. This prevents CLIProxyAPI's upstream panel updater from attempting to overwrite the pinned integration while leaving the control panel itself enabled.

Management Center upgrades require rebasing the small patch and rerunning its type check, full test suite, lint, and production build. Pinning makes an upstream contract change fail visibly rather than silently producing a broken panel.

The panel displays rate-limit utilization, not subscription billing or token usage. Management API authentication still gates every refresh, and neither the panel patch nor the plugin response exposes Mirasim credential material.

## Alternatives considered

- Register a plugin resource page: rejected because it appears under `#/plugin-pages/...`, not the requested `#/quota` page.
- Masquerade as a native Claude credential: rejected because provider identity controls executor selection and native Claude usage endpoints.
- Modify only CLIProxyAPI's passive quota provider whitelist: rejected because the current quota page does not dynamically render arbitrary `Auth.Quota` providers and the relevant Mirasim signal comes from signed model discovery.
- Vendor the entire Management Center source: rejected because it would duplicate a fast-moving upstream application for a small provider adapter.
