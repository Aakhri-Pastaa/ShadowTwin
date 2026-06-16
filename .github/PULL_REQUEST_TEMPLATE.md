## What this does

<!-- one or two sentences -->

## Which phase / component

<!-- e.g. Phase 1 — host agent: auth log collector -->

## Checklist

- [ ] Scoped to one component or one clear feature
- [ ] `pre-commit run --all-files` passes locally
- [ ] No secrets, API keys, or `.env` values in the diff
- [ ] If this touches `attacker/`: the scope-lock check is still enforced
- [ ] If this is a non-obvious architectural choice: added an ADR in `docs/adr/`
- [ ] Updated the relevant component README if behavior changed
