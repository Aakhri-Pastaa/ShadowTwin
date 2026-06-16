# 2. Build a custom Go host agent instead of forking Wazuh

Date: TODO — fill in when you commit this

## Status

Accepted

## Context

The original design referenced the Wazuh agent as a model for telemetry
collection. Two options: fork/embed Wazuh's existing agent, or write a
small purpose-built agent from scratch.

Wazuh's agent is a large C codebase tightly coupled to the Wazuh manager
protocol, and Wazuh is licensed GPLv2 — a strong copyleft license that
would constrain how this project's code can be combined and relicensed
later. We also want full understanding and ownership of every line in the
component that runs with elevated privilege on a user's machine, which is
easier in a codebase we write than one we inherit.

## Decision

Write a small, purpose-built host agent in Go: a single static binary that
drives `osquery` (Apache-2.0) and `auditd`/Sysmon for the actual collection
internals, and owns the shell — config, enrollment, mTLS transport, local
buffering, install. We don't reimplement collection logic that already
exists and is well-tested; we own the parts that are specific to this
project's pipeline.

## Consequences

We give up Wazuh's years of collector maturity and its existing rule
ecosystem. In exchange: no GPLv2 entanglement, a small auditable codebase,
and a cross-platform single binary that's easier to reason about and to
show in a portfolio context. Windows support (Sysmon-based) is deferred to
a later milestone — see `docs/architecture.md` layer 1 phasing.
