# Managing your own opencode2 session from inside the VM

How an agent inside an agents-sandbox VM drives its own opencode2 session and finds its skills.
`opencode2` is on `PATH` (installed at `/opt/agents-sandbox/bin/opencode2`).

## Compact your own context

There is no `compact` tool in the tool catalog. Request a compaction through the API:

```sh
opencode2 api session.compact --param sessionID="$OPENCODE_SESSION_ID" -d '{}'
```

Delivery is `steer`: it fires at the next step boundary, so issue it as your last action. Exit 0 with a
`"type":"compaction"` body means the request was admitted; the generated summary replaces the conversation
afterwards. `$OPENCODE_SESSION_ID` is exported in the agent shell.

## Two servers, one database

A VM commonly runs two `opencode2 serve` processes: the one the TUI attached to (started with
`--hostname`/`--port`) and the CLI's managed background service (`serve --service`). They share the SQLite
database at `~/.local/share/opencode/opencode.db`, so `$OPENCODE_SESSION_ID` and `session list` resolve against
either. Bare CLI commands talk to the background service; pass `--server http://127.0.0.1:<port>` to target a
specific one. `opencode2 service status` prints the background-service URL; `opencode2 api get /api/info` prints
`{version,pid,urls,capabilities}` for whichever server answered.

## Skill content is cached per location

opencode2 reads each skill's `SKILL.md` into per-location memory and serves that cached copy. A **new** skill is
registered without a reload, but **editing** an existing skill file does not change what the server serves until
it re-reads: `opencode2 api get /api/skill`, and a session loading the skill, keep returning the old text.
`opencode2 reload` refreshes it — it re-reads config, then tears down and restarts the location, aborting
in-flight tool calls with `Interaction cancelled because the location shut down`. A server restart does the same.
So when a skill you just edited "didn't change", reload before re-testing; a session that loaded the old content
keeps it until then.

Skills are scoped by location: `GET /api/skill?location[directory]=<dir>` returns the global skills plus that
directory's `.agents/skills`. The bare `/api/skill` reports the *server's own* location, so a project skill can
be missing from it while still available to a session in that project — scope the query before concluding a
skill is absent.
