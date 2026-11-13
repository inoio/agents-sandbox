package main

import (
	"slices"
	"strconv"
	"strings"
)

// optionalFlagValueMatchers reports, per flag pflag gives an implicit value via
// NoOptDefVal, whether a following token is that flag's space-form value: a
// token the matcher accepts is the flag's value, any other token is a positional
// argument. The matchers mirror the flags' declared value sets: --notify takes
// on/off/desktop/audio, --open-links takes every boolean spelling
// strconv.ParseBool accepts, exactly as pflag's Bool does.
//
// They are advisory: normalizeOptionalFlagValues rewrites the space form for
// every following token, and these matchers are redundant with that rule. They
// exist only to keep the vocabulary documented in one place beside the rewrite
// and are used by tests that pin it.
var optionalFlagValueMatchers = map[string]func(string) bool{
	flagNotify:    oneOf("on", "off", "desktop", "audio"),
	flagOpenLinks: isBoolLiteral,
}

// oneOf returns a matcher for a fixed value set.
func oneOf(values ...string) func(string) bool {
	return func(token string) bool { return slices.Contains(values, token) }
}

// isBoolLiteral reports whether token is a boolean literal pflag's Bool accepts,
// i.e. one strconv.ParseBool parses (1/0/t/f/T/F/TRUE/FALSE/True/False). Deriving
// the set from ParseBool keeps the rewrite aligned with pflag: a spelling pflag
// would take as the flag's value must be normalised, not left to split into the
// implicit true plus a stray positional.
func isBoolLiteral(token string) bool {
	_, err := strconv.ParseBool(token)
	return err == nil
}

// normalizeOptionalFlagValues rewrites the space form of the optional-value
// flags (--notify <mode>, --open-links <bool>) into their = form, so pflag's
// NoOptDefVal does not swallow the value into the positional args: with the
// space form left alone, --notify off would keep the watcher active and
// forward the word "off" to the agent, and --open-links 0 would leave the
// implicit true in place while forwarding the "0". Any following token that is
// not a flag (does not begin with "-") is taken as the value, so an invalid one
// -- notify bogus, --open-links off -- surfaces the same rejection as its =
// form instead of silently keeping the feature on and leaking the word into the
// args. The = form, a bare flag (whose NoOptDefVal still applies), a following
// flag, and everything after a bare -- are left alone.
func normalizeOptionalFlagValues(args []string) []string {
	normalized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			normalized = append(normalized, args[i:]...)
			break
		}
		flag, _, attached := strings.Cut(arg, "=")
		_, optional := optionalFlagValueMatchers[strings.TrimPrefix(flag, "--")]
		if !attached && optional && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			normalized = append(normalized, flag+"="+args[i+1])
			i++
			continue
		}
		normalized = append(normalized, arg)
	}
	return normalized
}
