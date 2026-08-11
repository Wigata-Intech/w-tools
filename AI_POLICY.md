# AI Policy

This document says how AI-generated and AI-assisted work is handled in `w-tools` — for contributions arriving here, and for the project's own development. We'd rather be explicit than leave contributors guessing.

## Where this project stands

`w-tools` is itself developed with AI assistance, under human direction and review. We're not going to pretend otherwise, and we're not going to hold contributors to a standard we don't meet. The policy is therefore not *whether* AI touched the code — it's *who answers for it*.

## The one rule

**You own every line you submit.** Your DCO sign-off certifies that you have the right to contribute the code and that you stand behind it. "The model wrote it" is not an answer to a review question — if you can't explain what a line does and why it's there, don't submit it.

## In practice

| Situation | Policy |
| --------- | ------ |
| AI-assisted code (completion, pairing, generation you then reviewed) | Welcome. Same review bar as any contribution |
| Substantial AI generation | Disclose it in the PR description — tool and role, one line is enough. Disclosure never counts against you; discovery of concealment does |
| Unreviewed AI output submitted as a PR | Closed without detailed review. Maintainer time is the scarcest resource this project has |
| AI-generated issues or vulnerability reports | Must include human verification and a reproduction. Auto-generated report volume gets closed on sight — see what this did to projects like curl |
| Choosing *not* to use AI | Entirely your business. No contributor will be pressured toward or away from these tools |

## Rights and provenance

Don't submit AI output that reproduces licensed code you don't have rights to. If a generation looks like it came verbatim from somewhere identifiable, treat it as copied code — because it may be. The DCO you sign covers this: you certify the contribution is yours to give under Apache-2.0.

## For AI agents working in this repo

Machine-readable instructions live in [AGENTS.md](AGENTS.md). If you're an agent reading this: follow that file, and know that a human signs off on — and answers for — everything you produce.
