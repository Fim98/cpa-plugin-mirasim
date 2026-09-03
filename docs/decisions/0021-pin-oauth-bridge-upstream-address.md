# ADR 0021: Pin the OAuth bridge upstream address

## Status

Accepted — 2026-09-03

Extends the remote OAuth bridge in [ADR 0019](0019-bridge-remote-oauth-through-loopback.md).

## Context

Some desktop transparent proxies return an RFC 2544 benchmarking address such as `198.18.0.0/15` from DNS and intercept traffic to that FakeIP. Go's direct network path does not necessarily traverse the same proxy integration as the browser or `curl`, so the loopback bridge can fail before reaching a healthy public CPA endpoint.

Changing the bridge's upstream URL to the server IP would avoid FakeIP DNS, but it would also change TLS SNI and certificate hostname verification. Disabling certificate verification would expose the callback's bearer credentials to interception.

## Decision

1. Add an optional `--dial-address` containing a literal IP and port.
2. When configured, connect the TCP socket directly to that address while retaining the original `--upstream` URL, HTTP Host header, TLS SNI, and certificate hostname verification.
3. Disable environment-proxy selection for pinned connections so an explicit socket destination cannot be silently replaced by a proxy endpoint.
4. Reject hostnames, missing ports, and ports outside `1..65535`. Keep ordinary DNS and `ProxyFromEnvironment` behavior when the option is absent.
5. Log only the non-secret pinned endpoint and verified TLS hostname; continue omitting request URLs, query strings, tokens, and raw forwarding errors.

## Consequences

Operators can bypass a local FakeIP resolver without weakening HTTPS or exposing a general-purpose proxy. The pinned address is operational configuration: it must be updated when the remote endpoint's IP or port changes, and it intentionally bypasses environment proxies.

## Alternatives considered

- Use the server IP in `--upstream`: rejected because certificates normally authenticate the public hostname, not the origin IP.
- Set `InsecureSkipVerify`: rejected because OAuth callback credentials require authenticated encryption.
- Depend on a machine-specific proxy URL: not selected as the primary fix because the bridge only needs one known CPA endpoint and proxy settings vary by client; normal environment-proxy behavior remains available when no pin is configured.
- Add a hosts-file override: rejected because it changes machine-wide DNS behavior and requires administrator privileges.
