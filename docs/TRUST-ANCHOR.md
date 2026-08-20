# The trust-anchor rule

This document is for the person deciding whether to believe an arqtos evidence
bundle. It is deliberately written so you can apply it without running our
software, and without trusting the party who handed you the bundle.

## The problem

An arqtos ledger is a chain of acts. Every act names its parent, binds its
dependencies by digest and carries signatures over its exact intent. Replaying
that chain answers one question very well:

> Is this tape internally consistent?

It does not answer the question you actually have:

> Is this the right tape?

Those come apart in a specific, realistic way. An administrator who controls the
tenant's keys can mint a **second genesis act**, build a second chain on it, sign
every act in that chain correctly, and hand you a bundle that replays perfectly.
Nothing inside it is wrong. It is simply not the history of the repository you
think you are looking at.

So consistency can never be the answer to a forged genesis, because a forged
chain is consistent by construction. **Self-contained is never
self-authenticating.**

## Where the trusted digest comes from

The first act of every arqtos ledger is a `RepositoryGenesis`, and its identity
is a domain-separated digest of its canonical bytes — a string of the form
`sha256:` followed by 64 hexadecimal characters. Two genesis acts differing in
any field have different identities.

A **trust anchor** is a genesis identity you obtained **out of band**, together
with an honest statement of how you obtained it. The channel matters more than
the value, because the value is only as good as the channel:

| provenance | what it means | independent? |
|---|---|---|
| `tenant_signed` | the tenant published the digest themselves | no — inside the boundary the tenant administers |
| `host_observed` | the code host's own view corroborates it | no — a second party, same boundary |
| `externally_witnessed` | an independent witness attested it | yes |

A digest you read out of the bundle is **not** an anchor. Neither is one the
sender emailed you along with the bundle. An anchor has to reach you by a path
the bundle's author does not control, or it is a copy of their claim rather than
a check on it.

## The rule

Compare the genesis your bundle presents against your anchor. There are four
outcomes and no others.

| case | outcome | what you may say |
|---|---|---|
| you hold **no anchor** at all | **downgraded** | what the tape contains — never that it is the right tape |
| the shown genesis **does not match** your anchor | **refused** | nothing; stop here |
| it matches, but your anchor is **not independently witnessed** | **downgraded** | what the tape contains, relative to a digest inside the tenant's boundary |
| it matches an **externally witnessed** anchor | **accepted** | the claim, naming its root, that root's provenance, its freshness and its observation coverage |

Three things about this table are load-bearing.

**No anchor is a downgrade, not a failure.** Refusing to look at a bundle
because you have no anchor would be useless — most readers start with no anchor.
You can still replay it, still see what it contains, still find internal
contradictions if there are any. What you cannot do is call the result a pass.

**A mismatch is a refusal, and the chain's quality is irrelevant to it.** Do not
weigh a perfectly-replaying chain against a mismatched anchor and conclude the
anchor must be stale. That reasoning is exactly what an alternate genesis is
built to invite.

**An unwitnessed match is still a downgrade.** A tenant-published digest confirms
that the bundle matches what the tenant says. If the tenant is the adversary, it
confirms nothing. This is why only `externally_witnessed` reaches "accepted".

## What "downgraded" has to look like

A downgraded result must be visibly downgraded at the place a human reads it —
not a footnote, and never an exit code of zero with a caveat in a log. In this
SDK the rule is enforced by returning the reason as an error alongside the
verdict, so a caller that checks only the error cannot mistake a downgrade for an
accept.

Every claim, downgraded or not, names four things: the root it is relative to,
that root's provenance, when the root was observed, and the observation coverage
it rests on. A claim missing any of them is not a smaller claim — it is an
unfalsifiable one.

## What this rule does not do

It does not detect a **split view**: a tenant showing two different consistent
histories to two different readers. Replay over a tenant's own tape cannot find
that, and no amount of care with anchors changes it. Detecting it needs an
external witness observing the tenant over time, which is a different mechanism
with a different cost. Until one exists, a result resting on a tenant-signed
anchor is downgraded and says so.

It also does not make a **malicious tenant root** survivable. The posture is
tenant-root-honest: if the root keys are the adversary, this rule tells you which
genesis you are looking at, not that the genesis deserved to exist.

## In this SDK

The rule is implemented in [`verify`](../verify/anchor.go) as `Anchor.Check`,
which returns an `AnchorDecision` — `refused`, `downgraded` or `accepted` — and
the reason. The genesis act itself is
[`contracts.RepositoryGenesis`](../contracts/genesis.go), and its identity is
`RepositoryGenesis.ID()`.
