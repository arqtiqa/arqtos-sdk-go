# Security policy

## Reporting a vulnerability

**Do not open a public issue.** Use GitHub's private vulnerability reporting on this repository
(**Security → Report a vulnerability**), which opens a channel visible only to the maintainers.

A public issue is a public advisory with no fix available yet — it tells everyone about the weakness at
the moment nothing can be done about it. That is the one outcome this policy exists to prevent.

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
