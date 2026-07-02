# API

API surface for the ingestor / backend (Roadmap Phase 2). Placeholder
until the API layer exists — fill in per-endpoint as they land.

## Conventions (once implemented)

- Document request/response shape, auth, and error format here as each
  endpoint ships — don't let this file lag behind the code.
- The findings store's `status` field lifecycle (per `architecture.md`:
  `new` → `triaged` → `exploit_confirmed` → `fix_recommended` →
  `fix_applied` → `verified`) should be reflected in whatever endpoint
  updates a finding's status, so this doc and the DB schema stay in sync.

## Endpoints

*(none yet — first candidate is likely "ingest a Wazuh alert" / "list
findings" once the ingestor exists)*
