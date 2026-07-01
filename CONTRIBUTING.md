# Contributing

This is currently a two-person project, but the workflow below is written
to scale cleanly if anyone else joins later.

## Setup

1. Clone the repo and copy the env template:
   ```
   git clone <repo-url>
   cd <repo-name>
   cp .env.example .env   # fill in your own local values, never commit this file
   ```
2. Install `pre-commit` and the hooks once per machine:
   ```
   pip install pre-commit
   pre-commit install
   ```
3. Each component has its own README with language-specific setup
   (Go toolchain for go-agent-v0 — archived, Python venv for the agents, npm
   for frontend).

## Day-to-day workflow

1. `git checkout main && git pull` — start from the latest main.
2. `git checkout -b feat/short-description` — one branch per task.
3. Do the work (Claude Code or by hand). Keep PRs small — one component or
   one feature, not "phase 4 in one PR."
4. Commit using Conventional Commits:
   - `feat: add ingestor mapping for a new Wazuh rule group`
   - `fix: correct CVE-to-CPE join in graph loader`
   - `docs: add ADR for Neo4j choice`
   - `chore:`, `refactor:`, `test:` as appropriate.
5. `git push -u origin feat/short-description`, then open a PR against `main`.
6. Request review from the other person. Don't merge your own PR without a
   review, even on a two-person team — it's the cheapest way to keep shared
   understanding of the codebase as it grows.
7. Squash-merge once approved. Delete the branch.

## Branch naming

`feat/...`, `fix/...`, `docs/...`, `chore/...` — matches the commit prefix
so it's obvious what a branch is for before opening it.

## Before you push

`pre-commit` runs automatically on `git commit`, but you can run it manually
across the whole repo with:
```
pre-commit run --all-files
```
CI re-runs the same checks per component on every PR (see
`.github/workflows/`), scoped by which directory changed.

## Architecture decisions

If a change is more than "implement what we already agreed on" — a new
datastore, a new language, dropping or merging a layer — write a short ADR
in `docs/adr/` before or alongside the PR. See `docs/adr/0001-*.md` for the
format and `docs/adr/0002-*.md` for a worked example.
