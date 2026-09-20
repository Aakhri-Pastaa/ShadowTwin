## What this does

<!-- one or two sentences -->

## Which phase / component

<!-- e.g. Phase 1 — host agent: auth log collector -->

## Checklist

- [ ] Scoped to one component or one clear feature
- [ ] `pre-commit run --all-files` passes locally
- [ ] No secrets, API keys, or `.env` values in the diff
- [ ] Scope is still frozen: no new platform component from `docs/archive/`
- [ ] `cd forwarder && python tests/smoke_test.py` passes (Linux)
- [ ] If this is a non-obvious architectural choice: added an ADR in `docs/adr/`
- [ ] Updated the relevant component README if behavior changed
