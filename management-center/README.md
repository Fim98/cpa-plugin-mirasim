# Management Center quota integration

CLIProxyAPI native plugins can register Management API and resource routes, but the built-in `management.html#/quota` page uses a compile-time provider-adapter registry. The plugin therefore ships a narrow patch against a pinned upstream Management Center commit instead of vendoring the full frontend repository.

Build the single-file panel from the repository root:

```powershell
.\scripts\build-management-center.ps1
```

The command fetches upstream commit `e0ee7123dfb5aa89a14ff73ac5a5c3bf4db658e0`, applies `patches/e0ee712-mirasim-quota.patch`, runs the frontend type check and test suite, and writes `dist/management.html`. If Bun is not installed, the script runs pinned Bun `1.3.14` through `npx`.

The patched panel:

- recognizes enabled `mirasim` auth records as quota-capable;
- adds a separate Mirasim tab and card on `#/quota`;
- calls `GET /v0/management/mirasim/quota?auth_index=...` through the existing Management API session;
- renders the 5-hour and 7-day utilization/reset signals without treating them as billing usage;
- keeps Mirasim separate from native Claude OAuth credentials.

The patch is intentionally pinned. Rebase and re-run the full Management Center verification before changing the upstream commit.

Deploy the generated file at `/CLIProxyAPI/static/management.html` and set
`remote-management.disable-auto-update-panel: true` in CLIProxyAPI. The latter
prevents the upstream panel updater from trying to replace the pinned custom
asset.
