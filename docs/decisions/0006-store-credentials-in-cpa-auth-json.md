# ADR 0006: Store Mirasim credentials in CPA auth JSON

## Status

Accepted — 2026-09-03

Supersedes the credential-persistence decisions in [ADR 0001](0001-mirasim-provider-boundaries.md) and [ADR 0005](0005-implement-mirasim-oauth-login.md). Their protocol, transport, and OAuth callback decisions remain in force.

## Context

The original plugin kept the access token, refresh token, and Ed25519 private key in a separate `mira2api` plaintext directory. Its CLIProxyAPI auth record stored only `credential_dir` and public endpoint settings. That split source of truth differed from the Gemini CLI plugin and CPA's built-in OAuth providers, complicated container volume management, and prevented the host's normal auth-storage lifecycle from owning rotated credentials.

CLIProxyAPI's native plugin contract defines `AuthData.StorageJSON` as provider-owned persisted auth data. The host merges host metadata into that JSON and saves it under the configured `auth-dir`. `RefreshAuth` can return a replacement `StorageJSON`, allowing refreshed access and refresh tokens to follow the same persistence path.

## Decision

1. Store `access_token`, `refresh_token`, and the Ed25519 PKCS#8 `device_private_key` directly in Mirasim `StorageJSON`, together with the relay URL, authentication URL, and client version.
2. Let CLIProxyAPI persist that payload as `mirasim.json` under `auth-dir`; OAuth login itself performs no credential-file writes.
3. Keep callback tokens only in bounded process memory until CPA polls the completed login. Return them only in the successful `AuthData.StorageJSON`, never in polling metadata, browser responses, or errors.
4. Remove `credential-dir`, `MIRASIM_CREDENTIAL_DIR`, `--mirasim-import`, and `--mirasim-credential-dir`. Do not read or migrate path-only records; upgrading users complete OAuth again.
5. Read credentials from the in-memory storage inside the Mirasim client. On token rotation, update that storage; `RefreshAuth` returns the latest values for CPA to save.
6. Preserve unknown host-owned JSON fields while replacing canonical Mirasim credential fields and removing `credential_dir`. Key pooled clients by a fingerprint derived from the device public key plus public endpoint settings, never by a bearer token.
7. Continue sending `POST /auth/refresh` through the private, proxy-aware HTTP client so its long-lived refresh-token request body does not enter CPA's host request recorder.

## Consequences

Mirasim now follows the same single-auth-file ownership model as the Gemini CLI plugin and CPA's built-in OAuth providers. A deployment needs only a persistent plugin directory and CPA `auth-dir`; it has no second credential-directory mount.

The CPA auth file now contains all material required to impersonate the Mirasim session. Operators must restrict, persist, and back up `auth-dir` accordingly. Moving that auth file to another compatible CPA instance also moves the Mirasim identity.

Path-only records are intentionally unsupported. Upgrading users delete the obsolete Mirasim auth entry and log in again; OAuth creates a new device key without depending on an external key.

Request-time emergency refreshes update the pooled client immediately. The scheduled host refresh is the persistence boundary exposed by the plugin ABI; a process crash before that boundary can leave the last on-disk refresh token stale. Normal `NextRefreshAfter` scheduling refreshes ahead of access-token expiry and returns the rotated token to CPA.

## Alternatives considered

- Keep path-only auth records or an import compatibility layer: rejected because the requested scope is OAuth-only and a second credential source can diverge after refresh-token rotation.
- Store only the refresh token and regenerate the device key: rejected because the Mirasim device identity depends on the Ed25519 key and must survive restarts.
- Send refresh through CPA's host HTTP bridge: rejected because diagnostic request capture may include the long-lived refresh token.
