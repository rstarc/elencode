# Sessions

elencode saves context windows as sessions tied to the project directory so that they are persisted on disk 
and can be resumed or revisited at a later point.

A sessions stores the entire context window, it's id, name, creation timestamp, last modified timestamp, number of tokens, and working directory.

## Storage

Sessions are stored in the directory where `elencode` was run, under `.elencode/sessions/${session-id}.json`
Each session is the serialized JSON representation of the entire context window.

## Resuming Sessions

Sessions can be resumed with the `/resume` command. 
The `/resume` command opens a session picker which lists all sessions in the current directory.

## Naming Sessions

When a new session is created, it is automatically assigned a UUIDv7 ID.
Empty sessions (meaning sessions without any messages) are not stored to disk.

The current session can be named using the `/rename` command.


## Commands

| Command | Description |
|---|---|
| `/resume` | Open a session picker to choose and resume a session in the current directory |
| `/rename` | Rename the current session |
| `/session` | Show session information (similiarly to `/config` command) |
| `/new` | Start a new session |

