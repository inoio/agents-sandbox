// Package view shapes the launcher's sandbox-domain objects for list and report
// output: it implements cliout.Printer for sandboxes, runner images, and home
// volumes, and builds the JSON DTOs for the prune and compact reports. Keeping
// these here leaves cliout the generic renderer that imports no domain package.
package view
