<!--
Thanks for the pull request. The comments in this template are hidden when it renders, so you can leave them or delete them.

TITLE
Use Conventional Commits: `type(scope): what changes`, in lowercase and the imperative.
  fix(server): keep every line of a multiline SSE event
  feat(admin): list audit records by user
  docs(readme): show the Postgres setup
Types: feat, fix, docs, test, refactor, perf, build, ci, chore. Add `!` after the type for a breaking change, as in `feat(config)!: ...`.
The title becomes the commit on main, because pull requests are squash merged.

HOW TO WRITE THE DESCRIPTION
- Write for someone who hasn't read the issue. Lead with what changes for a user, a caller or an operator, then say why.
- Plain English, short sentences, active voice. "Prefix every data line" reads better than "This PR aims to ensure that data lines are prefixed".
- Show before and after when it helps: a request, a config snippet, a log line or an error message, in a code block.
- Name files, functions, flags, config keys and endpoints in backticks.
- State facts. Skip words like robust, seamless, comprehensive or powerful. If you measured something, give the number and how you measured it.
- Use bullets for separate items, not for one thought cut into pieces.
- Keep it as short as the change allows. A one-line fix can have a three-line description.

CHECKS
Workflows from a fork start once a maintainer approves them, so the checks can show as waiting for a while. That's expected.
-->

Closes #

## What changes

<!-- What a user, caller or operator sees differently. Before and after, if it helps. -->

## Why

<!-- The problem this solves. If the issue already explains it, one line and the link is enough. -->

## How it works

<!-- The approach in a few sentences and the main files touched. Mention an alternative you ruled out if a reviewer would likely ask about it. -->

## How I tested it

<!--
The tests you added and what each one proves. For a bug fix, say that the new test fails on main and passes here.
Anything you ran by hand, with the command and the part of the output that matters.
-->

## What I didn't test

<!-- Platforms, providers, clients or setups you couldn't try. "Nothing" is a fine answer. -->

## logbook checks

<!-- Tick what applies and delete the rest. -->

Surfaces this touches: <!-- MCP tools, REST /v1/, admin API /admin/v1/, CLI, or none -->

- [ ] MCP and REST behave the same, since both go through `internal/wire` and `memory.Service`
- [ ] The MCP tools `remember`, `recall`, `verify`, `forget` and `list_memories` keep their names and arguments, or the title has `!` and the description lists the change
- [ ] A test shows one scope still can't read or change another scope's memories
- [ ] Forgotten memories stay hidden from recall, list and `GET /v1/memories/{id}`
- [ ] A store change passes `store/storetest`, so every backend gets the same tests
- [ ] A schema change adds the next numbered migration in `store/sqlite/migrations`
- [ ] Embedding and extraction code is tested with a fake, without network
- [ ] An API change updates `api/openapi.yaml`
- [ ] `LOGBOOK_NOW` is used only in tests and recorded demos
- [ ] A new config key works from YAML and from its `LOGBOOK_` environment variable

## Checklist

- [ ] Tests cover the change, and a bug fix has a test that fails without it
- [ ] Unit tests use no network, and wait on conditions instead of a bare `time.Sleep`
- [ ] `make check` passes (lint, tests and the 80% coverage check)
- [ ] `CHANGELOG.md` has a line under Unreleased, if users will notice the change
- [ ] README and docs are updated, if usage, config or commands changed
- [ ] Nothing breaks for current users, or the title has `!` and the description says what breaks and how to move over
