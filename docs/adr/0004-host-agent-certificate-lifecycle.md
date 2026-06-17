# 4. Host-agent certificate lifecycle: renewal and revocation

Date: 2026-06-17

## Status

Accepted

## Context

ADR-0003 gave the agent an mTLS identity via token + CSR enrollment but deferred
two lifecycle concerns: client certificates expire, and a decommissioned or
compromised host must be cut off. We need both **without** per-host manual cert
handling, without new dependencies, and without standing up a heavy PKI
(CRL/OCSP) — the platform is the single consumer of these certs.

## Decision

**Renewal — the agent renews itself before expiry.**

- New endpoint `POST /renew`, authenticated by the agent's **current, still-valid
  client certificate over mTLS** (no token). The agent sends a fresh CSR for the
  same key; the platform returns a new certificate.
- The agent checks at startup and on an interval (`RenewCheckInterval`, default
  6h). When the cert is within `RenewBefore` (default 30 days) of expiry it
  renews, swaps the cert in place — its `tls.Config` fetches the live cert per
  handshake via `GetClientCertificate`, so no restart — and rewrites `client.crt`.
- If the agent was offline long enough that the cert already expired, mTLS to
  `/renew` fails; it falls back to **token enrollment** if a token is still
  present, otherwise it reports "re-onboarding required".

**Revocation — a platform-side denylist.** We own both ends, so this is simpler
and sufficient versus CRL/OCSP.

- The platform keeps a set of revoked agent identities. `POST /ingest` and
  `POST /renew` reject a revoked client cert with **403**.
- Admin action `POST /revoke {agent_id}` (token-protected on the mock) adds an
  identity to the set; a real platform does this from its console/DB.

**Agent behavior on revocation (403) — a distinct outcome, not a poison batch.**
The transport classifies `401/403` as `ErrUnauthorized` (about *who we are*, not
the batch content). The agent **halts shipping, keeps all unsent events buffered
on disk, logs "re-onboarding required", and exits** — it discards nothing, and
re-joining is a deliberate operator action. (`400` stays poison → dead-letter;
`408/429/5xx`/network stay retryable.)

## Consequences

Easier: certificates rotate unattended; a compromised/decommissioned host is cut
off immediately and stops shipping **without losing already-collected telemetry**;
no CRL/OCSP infrastructure; still zero third-party dependencies.

Harder: short-lived certs require the platform to be reachable periodically (the
durable buffer covers gaps); revocation is only as strong as the platform's
denylist (fine while the platform is the sole verifier); a revoked agent needs a
deliberate re-onboard (by design).

Explicitly not doing (yet): key rotation on renewal (renewal keeps the existing
key); CRL/OCSP (could replace the denylist if a third party ever needs to verify
agent certs independently).

This extends ADR-0003's contract with `GET /ca`, `POST /renew`, and `POST /revoke`.
