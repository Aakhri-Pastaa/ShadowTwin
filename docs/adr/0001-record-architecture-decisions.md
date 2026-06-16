# 1. Record architecture decisions

Date: TODO — fill in when you commit this

## Status

Accepted

## Context

Two people, working asynchronously, will repeatedly ask "wait, why did we
build it this way?" months after the decision was made. PR descriptions
and chat logs get lost; a dedicated decision log doesn't.

## Decision

We use lightweight Architecture Decision Records (ADRs) in `docs/adr/`,
one file per decision, numbered sequentially, following this template:

```
# N. Short title

Date: YYYY-MM-DD

## Status
Proposed | Accepted | Superseded by ADR-M

## Context
What problem are we solving, what constraints apply.

## Decision
What we decided.

## Consequences
What gets easier, what gets harder, what we're explicitly not doing.
```

A new ADR is added for: choice of datastore/language for a layer, dropping
or merging an architectural layer, any change to the Attacker's scope
model, and any decision a future contributor would reasonably ask "why."
It is not used for routine implementation details — those belong in code
comments or the relevant component README.

## Consequences

Slightly more overhead per significant decision. In exchange, the repo's
history of *why* survives independently of any single person's memory —
useful for the two of us, and legible to anyone evaluating the project
later.
