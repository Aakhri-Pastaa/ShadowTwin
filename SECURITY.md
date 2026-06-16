# Security policy

This project builds security tooling, including an agent that performs
exploit validation. Two different things live in this file: how to report a
vulnerability *in our code*, and the rules for *using* the offensive
component responsibly.

## Reporting a vulnerability in this project

If you find a security issue in the host agent, the platform code, or the
frontend (e.g. the mTLS handshake, the ingest endpoint, credential
handling), please report it privately rather than opening a public issue:

- Open a [GitHub private security advisory](../../security/advisories/new)
  on this repo, or
- Email <maintainer-email-here> directly.

We'll acknowledge reports within a reasonable timeframe and credit you in
the fix unless you'd rather stay anonymous. This is a small open-source
project run by two people, not a company with an SLA — please be patient.

## Responsible use of the Attacker agent

This project's Attacker agent performs real exploit attempts (recon,
payload delivery, privilege escalation) using established tools (nmap,
sqlmap, Metasploit, etc.) orchestrated by an LLM. It is built to operate
**exclusively** against the bundled lab environment defined in
`lab/scope.yaml`.

- Do not point it at any system you don't own or don't have explicit,
  documented authorization to test.
- Do not remove, weaken, or bypass the scope-lock check in `attacker/`.
  If you're modifying that code, the scope check is the one thing that
  must survive every refactor.
- This project is for research, education, and portfolio purposes. It is
  not a commercial product and carries no warranty of any kind — see
  LICENSE.
- If you build on this code to target real infrastructure, that is your
  own legal responsibility, governed by the laws of your jurisdiction and
  any applicable computer-misuse statutes. We do not support or endorse
  that use.

## Supported versions

Pre-1.0, there are no maintained release branches — only `main`. Security
fixes land there.
