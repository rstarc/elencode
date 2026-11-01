# Instructions for the agent

elencode follows the instructions in `AGENTS.md` files, or in `CLAUDE.md` where a directory has no `AGENTS.md`. At startup it reads yours from its config directory, then one from each directory between the project's git root and the working directory. It reads a subdirectory's file the first time the agent touches a file there. It skips the files inside `.git`, `node_modules` and `vendor` directories, which hold other projects' code.
