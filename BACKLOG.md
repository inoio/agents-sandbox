# Backlog / Ideas

[*] test and dogfood the go binary
[*] enable --auto in opencode invocation
[*] Provide runtime per step in cli output, print elapsed duration for current/active step[ ] add test-run subcommand that ensures all steps until invocation of opencode
[*] consistently name everything after deciding on a project/tool name (home folders, project folders etc.)
[*] think through worktree functionality. Currently they get created, but not merged back at the end
[*] warn on VM running for project and branchSlug, ask to terminate other or exit, default/yes exit 
[*] --test-run flag for skipping opencode execution - for validating by an agent?
[*] refactor cli, subcommands for image rebuilding, ...? maybe remove some flags like --reset-home?
[*] version based on date
[ ] provide easy installation method
  [ ] Download via curl (without auth?) -> https://gitlab.inoio.de/inoio/opencode-msb/-/releases/permalink/latest
[ ] README überarbeiten
  * Installation ins Userverzeichnis ([~/bin,] ~/.local/bin)
  * config file(s) übersicht, beispiele
  * Bedeutungen z.B. env.secret sieht der Agent nicht
  * Flags an opencode übergeben (--) dokumentieren
  * Dependencies (z.B. git, docker)
    
[ ] opencode config dir & merging nach alphabet
[ ] inoio providers ausbauen, anders zur Verfügung stellen.
[ ] shell subcommand user flag (other flags from msb)
[ ] secrets containing @ kann nicht funktionieren -> yaml. Keine CLI-Params...
[ ] msb is not callable / not in path -> after EnsureInstalled, create symlink to ~/.local/bin
[ ] optionale Versionierung von dependencies (opencode, node.js)
[ ] was ist mit den LSP-Servern wenn ein Projekt node braucht?
[ ] docker images mit projekt-slug im namen bauen

[ ] dev doku, design principles usw. (z.B. CLI design aligned with docker,msb)
[ ] support docker in VM
[*] config file for cli settings in .opencode-msb
[ ] make cli output pretty and UX'd

[ ] after docker image import, remove docker built image?

[ ] clean commands for volumes, sandboxes, images. docker images/cache/... - with filters, best same ones. Also readable list output (project name, not number) (labels an docker images für cleanup?)
[ ] think about lifecycle of home volume - it would be nice to keep history, but not across projects. Maybe clean option is enough - fancy: option to interactively edit the home volume.
[ ] rework --tree, more explanations, root command

[ ] git library einbinden?
[ ] cli interaction library instead of prompt.go?
[ ] git doctor nach install dokumentieren/aufrufen
[ ] neben docker auch buildah, podman, ??? supporten?
[ ] user uid,gid aus System bestimmen und setzen bei image build.