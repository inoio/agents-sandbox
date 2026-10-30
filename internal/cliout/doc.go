// Package cliout is the generic command-output core: it resolves the output mode
// from the --format/--quiet flags and renders lists and report payloads as
// tables, identifiers, or JSON. It deliberately imports no domain package, so any
// package may use it; the domain Printer implementations live in the launcher's
// view package. It holds no cobra wiring, so formatters are tested without
// building commands.
package cliout
