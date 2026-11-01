# Conventions

`AGENTS.md` holds the rules that apply on every turn. This file holds the rules for a
particular step of the work, and `AGENTS.md` links here when that step starts.

## Docs first

Start a new feature, or any change a user will notice, by writing the user's view of
it. Write it before the tests and the code: it is the spec they must meet, in the same
way that a failing test is the spec for the code.

### What to write

- Put it in the user guide under `docs/` that covers the topic, such as
  `docs/providers.md`. If no guide covers it, start one at `docs/<topic>.md`.
- Describe what the user does and what they see: the commands they run, the files
  they create, what elencode prints, and what happens when something goes wrong. Show
  it with examples, such as a shell session or a few lines of the transcript.
- Leave out how it is built. Packages, types and the reasons for the design belong in
  the code, in the layout section of `AGENTS.md`, or in a design spec.
- Write in plain language, in the user's terms, and define each term on first use.

For AGENTS.md support, the guide would have been `docs/instructions.md`. It would say
where to put an `AGENTS.md`, which file wins when there are several, where the
startup header lists the files it read, what the notice says when a subdirectory's
file is added mid-session, and that each file is cut at 32 KiB.

### Order of work

1. Write or update the guide.
2. Show it to the user and agree on it before writing any test. Changing a paragraph
   is cheaper than changing code.
3. Work red/green from the guide: each example in it becomes a test where it can.
4. When the code turns out to differ from the guide, change one of them in the same
   change, so that the two never disagree.

The rule is that the change includes the guide, not that the guide's commit comes
first: squashing a branch removes the order of its commits.

### When it does not apply

Changes a user cannot notice need no guide: refactors, internal fixes, tests and
dependency updates. A bug fix that restores the documented behavior needs none either;
one that changes the behavior does. Nor does a change to how something looks that gives
the user nothing new to do or learn, such as a logo, colors, spacing or wording.

### Design specs

A dated design spec in `docs/superpowers/specs/` is optional. Write one when the
design has choices worth recording: research, rejected options or trade-offs. It
comes after the guide and links to it.
