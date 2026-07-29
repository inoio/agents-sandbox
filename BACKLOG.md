# Backlog / Ideas

## Done

[*] test and dogfood the go binary
[*] enable --auto in opencode invocation
[*] Provide runtime per step in cli output, print elapsed duration for current/active step[ ] add test-run subcommand that ensures all steps until invocation of opencode
[*] consistently name everything after deciding on a project/tool name (home folders, project folders etc.)
[*] think through worktree functionality. Currently they get created, but not merged back at the end
[*] warn on VM running for project and branchSlug, ask to terminate other or exit, default/yes exit 
[*] --test-run flag for skipping opencode execution - for validating by an agent?
[*] refactor cli, subcommands for image rebuilding, ...? maybe remove some flags like --reset-home?
[*] version based on date
[*] provide easy installation method
  [*] Download via curl (without auth?) -> https://gitlab.inoio.de/inoio/opencode-msb/-/releases/permalink/latest
[*] config file for cli settings in .opencode-msb
[*] git doctor nach install dokumentieren/aufrufen
[*] install golangci-lint globally instead of to user home dir
[*] make tmpfs size configurable and raise from 512m to 2GB (go builds broke with "no space left on device")
[*] think about lifecycle of home volume - it would be nice to keep history, but not across projects. Maybe clean option is enough - fancy: option to interactively edit the home volume.
[*] !!! NEVER mount a home volume twice. For 2 sessions in 1 repo, warn the user that it'll be an ephemeral session, not recording opencode session history. Start from a fresh home copy from root
[*] msb is not callable / not in path -> after EnsureInstalled, create symlink to ~/.local/bin
[*] user uid,gid aus System bestimmen und setzen bei image build.
[*] rework --tree, more explanations, root command
[*] support docker in VM

## In Progress

[ ] use less tokens for dev
[ ] single VM per project with multiple workspaces to work around sqlite's fcntl() locking home dir?
[ ] --branch merge deletes repo with changes

### README überarbeiten

[ ] Installation ins Userverzeichnis ([~/bin,] ~/.local/bin)
[ ] config file(s) übersicht, beispiele
[ ] Bedeutungen z.B. env.secret sieht der Agent nicht
[ ] Flags an opencode übergeben (--) dokumentieren
[ ] Dependencies (z.B. git, docker)

## 1st prio

[ ] make cli output pretty and UX'd
[ ] secrets containing @ kann nicht funktionieren -> yaml. Keine CLI-Params...
[ ] docker images mit projekt-slug im namen bauen
[ ] Deliver example AGENTS.md file with instructions for workflow, especially git (don't merge, tool does) 
[ ] OPENCODE_DISABLE_AUTOUPDATE, OPENCODE_EXPERIMENTAL_EXA, OPENCODE_EXPERIMENTAL_BACKGROUND_SUBAGENTS, OPENCODE_EXPERIMENTAL_PARALLEL, OPENCODE_EXPERIMENTAL_SCOUT? 

## 2nd prio

[ ] after docker image import, remove docker built image? Or delete docker layers when deleting msb images? Regular auto-cleanup for this?
[ ] inoio providers ausbauen, anders zur Verfügung stellen.
[ ] clean commands for volumes, sandboxes, images. docker images/cache/... - with filters, best same ones. Also readable list output (project name, not number) (labels an docker images für cleanup?)
[ ] shell subcommand user flag (other flags from msb)
[ ] optionale Versionierung von dependencies (opencode, node.js)
[ ] dev doku, design principles usw. (z.B. CLI design aligned with docker,msb)

## 3rd prio

[ ] neben docker auch buildah, podman, ??? supporten?
[ ] opencode config dir & merging nach alphabet
[ ] was ist mit den LSP-Servern wenn ein Projekt node braucht?
[ ] git library einbinden?
[ ] cli interaction library instead of prompt.go?
