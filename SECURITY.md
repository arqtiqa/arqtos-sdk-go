# Security policy

## Reporting a vulnerability

**Do not open a public issue.** Open a private report here:

**<https://github.com/arqtiqa/arqtos-sdk-go/security/advisories/new>**

That form opens a channel visible only to the maintainers. It needs a GitHub account and nothing else —
no access to any private repository, and no relationship with the maintainers. The same page is reachable
as **Security → Report a vulnerability** in this repository's tabs.

A public issue is a public advisory with no fix available yet — it tells everyone about the weakness at
the moment nothing can be done about it. That is the one outcome this policy exists to prevent.

### This is the route for every arqtos surface

Use it for the SDK, for the skills and packs, and for the external berg — not just for this module. This
repository is the world-readable one, and GitHub offers private vulnerability reporting on **public**
repositories only, so this is where the channel can exist. Say in the report which surface you mean.

### If the link does not work for you

Say so **in a public issue on this repository, without the details** — one line is enough: *"I have a
security report and cannot reach the advisory form."* Naming the weakness is what must not be public; the
fact that you have one is not sensitive, and a maintainer will come back with somewhere to send it. That
is strictly better than the alternative it replaces, which is filing the details publicly, or not filing.

## What to include

- what the weakness lets an attacker do, and the conditions required
- affected version or commit
- a reproduction, if you have one — but **report without one** rather than not reporting

**Never include a real credential, token, or secret reference in a report.** If your reproduction
involves one, describe its shape (`op://<vault>/<item>/<field>`) rather than pasting the value.

## Scope

This module is a **contract and SDK**. It resolves credential *references* and carries them across a
connector boundary; it is not itself a secret store. Reports of particular interest:

- a path by which a credential **value** could enter a log, an error, a returned struct, or a model's
  context — the boundary this SDK exists to hold
- a connector able to reach data outside the references it was granted
- a check or gate that reports success without having verified anything

## What to expect

Acknowledgement before triage. You will be told which of three outcomes applies — fixed, tracked, or
working as intended — rather than left to infer it from silence.
