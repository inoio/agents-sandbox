---
name: terminal-reset
description: Verify the post-attach terminal reset by hand - which DEC private modes and keyboard protocols each harness TUI enables, how to simulate a TUI kill (not a real OOM), and the per-terminal checklist for the residue a kill leaves. Use when changing session.terminalReset or when it is emitted, adding a harness, or checking a report of a broken terminal after an agent was killed.
---

# Verifying the post-attach terminal reset

When a harness TUI dies without restoring the terminal (an OOM kill, a `kill -9`), the modes it enabled stay set on
the host terminal. The launcher emits `session.terminalReset` after an abnormal attach end (`specs/resources/resource-limit-proposal.md` §2). This guide records which modes the harnesses enable and how to check by hand
what residue a kill leaves in a real terminal emulator, which a pty-based test cannot show.

## Modes the harnesses enable

Measured on a KVM host. The harness smoke suite (`test/harness`) was run with a temporary dump of
`ses.output()` after readiness, and the raw bytes were scanned for `CSI ? Pm h|l`, `CSI > Ps u` (kitty keyboard
push), `CSI ? u` (kitty query) and `CSI > 4 ; Ps m` (xterm modifyOtherKeys).

| Harness | Sets (DEC private modes) | Keyboard |
|---|---|---|
| opencode, opencode2 | 1049, 1000, 1002, 1003, 1006, 2004, 2026, 2027, 2031 | modifyOtherKeys 1; kitty query |
| codex | 1049, 1000, 1002, 1003, 1004, 1006, 1007, 2004, 2026 | kitty push `>7u`; modifyOtherKeys 0 |
| pi | 1049, 1000, 1002, 1003, 1004, 1006, 2004, 2026, 2031 | kitty push `>7u` |
| claude-code | 1004, 2004, 2031 | kitty query |
| oh-my-pi | 2004, 2031, 5522; toggles 7 (autowrap) off and on | modifyOtherKeys 2; kitty query |
| hermes | 2004; toggles 7 (autowrap) off and on | — |

Mode meanings: 1049 alternate screen with saved cursor; 1000/1002/1003 mouse click/button-motion/any-motion
reporting; 1006 SGR mouse encoding; 1007 alternate scroll; 1004 focus in/out events; 2004 bracketed paste; 2026
synchronized output; 2027 grapheme clusters; 2031 color-scheme change notifications; 7 autowrap; 5522 unidentified.

The pty answers no queries, so a harness that only pushes the kitty keyboard protocol after a terminal confirms it
(opencode, claude-code, oh-my-pi query with `CSI ? u`) did not push here; in kitty, Ghostty or WezTerm it likely
does. Repeat the measurement in such a terminal (see below) before trusting the table for the keyboard column.

## Simulating the kill

An OOM kill picks the process with the largest footprint, which is rarely the TUI, so killing the TUI directly is the
deterministic simulation. The effect on the terminal is the same: the TUI gets no chance to restore its modes.

1. Build the launcher on the host: `make build` (produces `./agents-sandbox`).
2. Terminal A, in a project directory: `./agents-sandbox run --agent opencode` (repeat per harness: `codex`, `pi`,
   `claude-code`, ...). Wait for the TUI, move the mouse over it once.
3. Terminal B, same project directory: `./agents-sandbox shell`, then kill the TUI process inside the VM, e.g.
   `pkill -KILL -f 'opencode attach'` (codex: `pkill -KILL -x codex`; others: find it with `ps -ef`). The attach in
   terminal A ends with exit code -1, which is an abnormal end, so the launcher emits the reset.
4. Baseline without any reset: in terminal B on the host, `pkill -KILL -f 'agents-sandbox run'` kills the launcher
   itself. The SDK's termios restore is skipped too, so type `stty sane` and Enter blindly in terminal A first.
   Compare its residue with step 3.
5. Clean exit: quit the TUI normally. No reset is sent (by design); check that the prompt appears below the previous
   output and nothing overwrites it.

Optional end-to-end with a real OOM: `./agents-sandbox run --memory 1G` and run a memory hog from the agent's shell
tool. The kernel line in the post-attach offer names the victim; terminal residue only appears if the victim is the
TUI.

To capture the bytes a harness sends in a real terminal (for the keyboard column), wrap the launcher in
`script -q /tmp/tui.log ./agents-sandbox run --agent opencode`, quit, and scan `/tmp/tui.log` with
`grep -aoE $'\e\\[[?<>=]?[0-9;]*[hlum]' /tmp/tui.log | sort | uniq -c`.

## Checklist after the kill

Run it in each terminal that matters (xterm or GNOME Terminal/VTE, kitty, Ghostty, WezTerm, iTerm2, Terminal.app,
and inside tmux). Bash re-enables bracketed paste at its own prompt, so check paste, focus and keys inside `cat`.

1. Normal screen with the earlier scrollback, cursor visible, default colors.
2. Moving, clicking and scrolling the mouse prints nothing (1000/1002/1003/1006/1007).
3. In `cat`: switching focus away and back prints no `^[[I`/`^[[O` (1004).
4. In `cat`: pasting prints no `^[[200~`/`^[[201~` around the text (2004).
5. In `cat`: Ctrl-C exits; Ctrl/Shift with letters and arrows produce no `^[[99;5u` (kitty keyboard) or
   `^[[27;5;99~` (modifyOtherKeys).
6. A line longer than the window wraps (7).
7. Switching the system between light and dark prints nothing (2031).
8. The reset itself leaves no visible characters in terminals that do not know a sequence it sends.

Record per terminal and harness which items fail. Each failing item names the sequence to add to `terminalReset`:
`?1002l ?1003l ?1007l` (mouse), `?1004l`, `?2004l`, `?2031l`, `?7h`, `CSI > 4 ; 0 m` (modifyOtherKeys) and
`CSI < u` repeated or `CSI < Ps u` (kitty keyboard pop), each checked against item 8.
