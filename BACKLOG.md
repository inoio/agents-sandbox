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
[*] use less tokens for dev
[*] prune does not seem to work
[*] single VM per project with multiple workspaces to work around sqlite's fcntl() locking home dir?
[*] --branch merge deletes repo with changes
[*] Installation ins Userverzeichnis ([~/bin,] ~/.local/bin)
[*] docker images mit projekt-slug im namen bauen
[*] git library einbinden?
[*] Dependencies (z.B. git, docker)
[*] Flags an opencode übergeben (--) dokumentieren
[*] dogfood -b/--branch feature
[*] Deliver example AGENTS.md file with instructions for VM environment, workflow

## Testing

[ ] Test:
* volume migrate, edit, delete
* config changes to root size, tmp size, env, secrets, cpu, mem
*
[ ] secrets containing @ kann nicht funktionieren -> yaml. Keine CLI-Params.

## In Progress

[ ] env.secret[.yaml] im Projekt, wie verstecken?
[ ] default pattern for hiding project files, configurable (*.secret), not checked in?
[ ] Concept for connecting Opencode Desktop to sandboxed server

### README überarbeiten


## 1st prio

[ ] inoio providers ausbauen, anders zur Verfügung stellen.
[ ] opencode config dir & merging nach alphabet
[ ] config file(s): support arbitrary files in VM home
[ ] config file(s) übersicht, beispiele
[ ] config show: list all files that would apply to a VM startup.

[ ] make cli output pretty and UX'd
[ ] OPENCODE_DISABLE_AUTOUPDATE and detect releases, offer vm rebuild
[ ] OPENCODE_EXPERIMENTAL_EXA, OPENCODE_EXPERIMENTAL_BACKGROUND_SUBAGENTS, OPENCODE_EXPERIMENTAL_PARALLEL, OPENCODE_EXPERIMENTAL_SCOUT? 
[ ] remove msb load exec call by spooling to tmp file and using msb SDKs Image.Load 

## 2nd prio

[ ] after docker image import, remove docker built image? Or delete docker layers when deleting msb images? Regular auto-cleanup for this?
[ ] clean commands for volumes, sandboxes, images. docker images/cache/... - with filters, best same ones. Also readable list output (project name, not number) (labels an docker images für cleanup?)
[ ] shell subcommand user flag (other flags from msb)
[ ] optionale Versionierung von dependencies (opencode, node.js)
[ ] dev doku, design principles usw. (z.B. CLI design aligned with docker,msb)

## 3rd prio

[ ] neben docker auch buildah, podman, ??? supporten?
[ ] was ist mit den LSP-Servern wenn ein Projekt node braucht?
[ ] cli interaction library instead of prompt.go?
[ ] testify/mock & mockery statt manuell?
