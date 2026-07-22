# Backlog / Ideas

[ ] test and dogfood the go binary
[ ] config file for cli settings in .opencode-msb
[ ] consistently name everything after deciding on a project/tool name (home folders, project folders etc.)
[ ] refactor cli, subcommands for image rebuilding, ...? maybe remove some flags like --reset-home?
[ ] think through worktree functionality. Currently they get created, but not merged back at the end
[ ] enable --auto in opencode invocation
[ ] make cli output pretty and UX'd
[ ] add test-run subcommand that ensures all steps until invocation of opencode
[ ] think about lifecycle of home volume - it would be nice to keep history, but not across projects
[ ] Provide runtime per step in cli output, print elapsed duration for current/active step