# 0001 — Record architecture decisions

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

This project makes many non-obvious choices (why three binaries? why Kaniko and
not Docker-in-Docker? why a CRD instead of generating YAML?). A newcomer — or the
author six months from now — needs to know not just *what* was chosen but *why*,
and what was rejected. Code comments explain the "how"; they are the wrong place
for cross-cutting "why" decisions.

## Decision

We keep a log of **Architecture Decision Records** (ADRs) in
`docs/decisions/`, one file per decision, using the lightweight format
popularized by Michael Nygard. Each ADR has: a status, the context that forced a
choice, the decision, and the consequences (good and bad).

ADRs are **append-only**. We never rewrite an accepted ADR. If a later decision
changes an earlier one, we write a new ADR and mark the old one `Superseded by
NNNN`.

## Consequences

- New contributors can read the decision log top-to-bottom to understand the
  shape of the system without archaeology through git history.
- There is a small overhead: a meaningful decision costs a short markdown file.
  We accept that; the alternative is undocumented tribal knowledge.
