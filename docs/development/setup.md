# Setup

Turn on the repository's git hooks once per clone:

```
make hooks
```

This runs `githooks/commit-msg` on every commit, which rejects a message that does not
follow the format in `AGENTS.md` under "Commits". The hook needs
[cocogitto](https://docs.cocogitto.io) (`cog`) and refuses every commit without it. The
flake's development shell provides it, so commit from inside it:

```
nix develop
```

[direnv](https://direnv.net) with `use flake` in an `.envrc` enters the shell
automatically, including for agents that run `git commit` themselves.

Pull requests are checked by the Commits workflow as well: every commit, and the title.
A commit the hook missed still fails there.
