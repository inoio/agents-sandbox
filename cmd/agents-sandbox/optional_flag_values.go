package main

import (
	"slices"
	"strings"
)

// optionalFlagValueVocabulary is the space-form value vocabulary of the flags
// pflag gives an implicit value via NoOptDefVal: a token following the flag
// that is in its vocabulary is the flag's space-form value, any other token is
// a positional argument. The vocabularies mirror the flags' declared value
// sets: --notify takes on/off/desktop/audio, --open-links takes true/false.
var optionalFlagValueVocabulary = map[string][]string{
	flagNotify:    {"on", "off", "desktop", "audio"},
	flagOpenLinks: {"true", "false"},
}

// normalizeOptionalFlagValues rewrites the space form of the optional-value
// flags (--notify <mode>, --open-links <bool>) into their = form, so pflag's
// NoOptDefVal does not swallow the value into the positional args: with the
// space form left alone, --notify off would keep the watcher active and
// forward the word "off" to the agent. The = form, a bare flag (whose
// NoOptDefVal still applies), and everything after a bare -- are left alone.
// It runs before Cobra parses; a token outside a flag's vocabulary is left for
// pflag (a rejected value or a positional argument) to handle.
func normalizeOptionalFlagValues(args []string) []string {
	normalized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			normalized = append(normalized, args[i:]...)
			break
		}
		flag, _, attached := strings.Cut(arg, "=")
		values, optional := optionalFlagValueVocabulary[strings.TrimPrefix(flag, "--")]
		if !attached && optional && i+1 < len(args) && slices.Contains(values, args[i+1]) {
			normalized = append(normalized, flag+"="+args[i+1])
			i++
			continue
		}
		normalized = append(normalized, arg)
	}
	return normalized
}
