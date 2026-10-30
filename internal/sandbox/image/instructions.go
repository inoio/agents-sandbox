package image

import "strings"

// fromInstruction is the FROM Dockerfile instruction keyword, upper-cased.
const fromInstruction = "FROM"

// dockerfileInstructions is the set of Dockerfile instruction keywords that
// instruction recognises. Matching is case-insensitive: Docker accepts any
// casing for an instruction keyword.
var dockerfileInstructions = map[string]struct{}{
	"ADD":           {},
	"ARG":           {},
	"CMD":           {},
	"COPY":          {},
	"ENTRYPOINT":    {},
	"ENV":           {},
	"EXPOSE":        {},
	fromInstruction: {},
	"HEALTHCHECK":   {},
	"LABEL":         {},
	"MAINTAINER":    {},
	"ONBUILD":       {},
	"RUN":           {},
	"SHELL":         {},
	"STOPSIGNAL":    {},
	"USER":          {},
	"VOLUME":        {},
	"WORKDIR":       {},
}

// instruction splits a Dockerfile line into its upper-cased instruction keyword
// and the rest of the line. The keyword is recognised only when the line's first
// whitespace-delimited token names a known instruction and is followed by
// whitespace, so "FROMAGE x" is not read as FROM. Leading whitespace is ignored;
// blank lines, comments, and other text yield an empty keyword.
func instruction(line string) (string, string) {
	trimmed := strings.TrimLeft(line, " \t")
	keywordEnd := strings.IndexAny(trimmed, " \t")
	if keywordEnd < 0 {
		return "", ""
	}
	keyword := strings.ToUpper(trimmed[:keywordEnd])
	if _, known := dockerfileInstructions[keyword]; !known {
		return "", ""
	}
	return keyword, strings.TrimLeft(trimmed[keywordEnd+1:], " \t")
}
