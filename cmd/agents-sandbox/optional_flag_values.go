package main

import (
	"slices"
	"strconv"
	"strings"
)

// optionalFlagValueMatchers reports, per flag pflag gives an implicit value via
// NoOptDefVal, whether a following token is that flag's space-form value rather
// than a positional argument. A token a matcher accepts is the flag's value:
// normalizeOptionalFlagValues rewrites it into the = form and the flag's own
// parser validates it, so --notify OFF, --notify false and --open-links off are
// rejected exactly as their = forms. A token no matcher accepts is left as a
// positional argument and forwarded to the agent: pflag never inspects it,
// because --notify/--open-links carry a NoOptDefVal, so pflag takes that
// implicit value and stops at the flag without consuming the token. `run
// --notify fix the bug` therefore keeps the watcher on and forwards "fix the
// bug", and `run --notify bogus` does the same rather than rejecting it. The
// matchers accept the spellings a user is
// likely to write for a value, matched case-insensitively: the notify modes
// on/off/desktop/audio, the natural-language on/off, and every boolean literal
// strconv.ParseBool accepts.
var optionalFlagValueMatchers = map[string]func(string) bool{
	flagNotify:    isNotifyValue,
	flagOpenLinks: isOpenLinksValue,
}

// isNotifyValue reports whether token is a --notify value spelling: a notify
// mode (on/off/desktop/audio) or any boolean literal.
func isNotifyValue(token string) bool {
	return oneOfFold("on", "off", "desktop", "audio")(token) || isBoolLiteral(token)
}

// isOpenLinksValue reports whether token is an --open-links value spelling: the
// natural-language on/off or any boolean literal. on/off are accepted so a user
// writing --open-links off has it validated (and rejected by pflag's Bool)
// rather than forwarded to the agent.
func isOpenLinksValue(token string) bool {
	return oneOfFold("on", "off")(token) || isBoolLiteral(token)
}

// oneOfFold returns a matcher for a fixed value set, compared case-insensitively.
func oneOfFold(values ...string) func(string) bool {
	return func(token string) bool {
		return slices.ContainsFunc(values, func(value string) bool {
			return strings.EqualFold(value, token)
		})
	}
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
// space form left alone, --notify off would keep the watcher active and forward
// the word "off" to the agent, and --open-links 0 would leave the implicit true
// in place while forwarding the "0". Only a following token the flag's matcher
// accepts is taken as the value; a plain word (--notify fix) stays a positional
// argument for the agent, as docs/commands.md documents. The rewrite applies
// only to a --long flag: a dash-less token spelled like the flag (run notify me)
// and the captured value of another flag (-w notify) are left untouched. The =
// form, a bare flag (whose NoOptDefVal still applies), and everything after a
// bare -- are left alone.
func normalizeOptionalFlagValues(args []string) []string {
	normalized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			normalized = append(normalized, args[i:]...)
			break
		}
		if strings.HasPrefix(arg, "--") {
			flag, _, attached := strings.Cut(arg, "=")
			matches, optional := optionalFlagValueMatchers[strings.TrimPrefix(flag, "--")]
			if !attached && optional && i+1 < len(args) && matches(args[i+1]) {
				normalized = append(normalized, arg+"="+args[i+1])
				i++
				continue
			}
		}
		normalized = append(normalized, arg)
	}
	return normalized
}
