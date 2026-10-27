package image

import (
	"bytes"
	"regexp"
	"strings"
)

// heredocDirectives are the Dockerfile instructions that may declare a heredoc,
// mirroring BuildKit's parser; ONBUILD can wrap one of them.
var heredocDirectives = map[string]struct{}{
	"RUN":     {},
	"COPY":    {},
	"ADD":     {},
	"ONBUILD": {},
}

// heredocOperator matches a BuildKit heredoc word: an optional file descriptor,
// the operator (with the optional chomping "-"), and the delimiter. It is
// anchored, so a token only counts when the operator starts it — which is how a
// shell arithmetic shift such as $((1<<2)) and a quoted operator such as
// "<<EOF" are excluded, matching BuildKit.
var heredocOperator = regexp.MustCompile(`^(\d*)<<(-?)\s*([^<]*)$`)

// lineContinuations maps a supported escape token to BuildKit's line
// continuation pattern: an escape at end of line that is not itself escaped, or
// a line that is only the escape token.
var lineContinuations = map[byte]*regexp.Regexp{
	'\\': regexp.MustCompile(`([^\\])\\[ \t]*$|^\\[ \t]*$`),
	'`':  regexp.MustCompile("([^`])`[ \t]*$|^`[ \t]*$"),
}

// directiveLine matches a BuildKit parser directive: a leading comment line of
// the form "# key=value", with the key case-insensitive.
var directiveLine = regexp.MustCompile(`^#\s*([a-zA-Z][a-zA-Z0-9]*)\s*=\s*(.+?)\s*$`)

// heredocDelimiter is a heredoc a Dockerfile instruction declares: its delimiter
// word and whether the leading tabs of its body are stripped (the "<<" vs "<<-"
// operator).
type heredocDelimiter struct {
	name  string
	chomp bool
}

// dockerfileLines splits a Dockerfile into lines, keeping each line's trailing
// newline so callers can reassemble the source unchanged.
func dockerfileLines(dockerfile []byte) [][]byte {
	return bytes.SplitAfter(dockerfile, []byte("\n"))
}

// instructionLineIndices returns, in order, the indexes of the lines that begin a
// Dockerfile instruction. Blank lines, comments, continuation lines, and heredoc
// bodies are skipped, so text that merely resembles a keyword — a Python
// "from ... import ..." inside a RUN heredoc, or a continued argument — is not
// read as a stage header (#346).
func instructionLineIndices(lines [][]byte) []int {
	escape := escapeToken(lines)
	var indices []int
	for i := 0; i < len(lines); {
		if isBlankOrComment(lineText(lines[i])) {
			i++
			continue
		}
		indices = append(indices, i)
		i = instructionEnd(lines, i, escape)
	}
	return indices
}

// instructionEnd returns the index of the first line after the instruction that
// begins at start: its continuation lines and any heredoc bodies it declares.
func instructionEnd(lines [][]byte, start int, escape byte) int {
	var logical strings.Builder
	i := start
	first := true
	for i < len(lines) {
		line := lineText(lines[i])
		i++
		if !first && isBlankOrComment(line) {
			// A comment or blank line inside a continuation is ignored.
			continue
		}
		first = false
		if !lineContinues(line, escape) {
			logical.WriteString(line)
			break
		}
		// BuildKit drops the continuation escape and joins the physical lines
		// before it reads the heredoc delimiters, so a delimiter word glued to
		// the escape is read whole.
		logical.WriteString(stripContinuation(line, escape))
	}
	for _, delimiter := range heredocDelimiters(logical.String()) {
		for i < len(lines) {
			line := lineText(lines[i])
			i++
			if heredocBodyEnds(line, delimiter) {
				break
			}
		}
	}
	return i
}

// heredocDelimiters returns the heredocs an instruction declares. Only the
// instructions BuildKit allows may declare one, and a delimiter is recognised
// only when the operator starts the token.
func heredocDelimiters(instructionText string) []heredocDelimiter {
	keyword, _ := instruction(instructionText)
	if _, ok := heredocDirectives[keyword]; !ok {
		return nil
	}
	var delimiters []heredocDelimiter
	for _, word := range instructionWords(instructionText) {
		match := heredocOperator.FindStringSubmatch(word)
		if match == nil {
			continue
		}
		name := unquoteDelimiter(match[3])
		if name == "" {
			continue
		}
		delimiters = append(delimiters, heredocDelimiter{name: name, chomp: match[2] == "-"})
	}
	return delimiters
}

// heredocBodyEnds reports whether line terminates a heredoc body. A chomping
// heredoc ("<<-") allows its delimiter to be indented with tabs.
func heredocBodyEnds(line string, d heredocDelimiter) bool {
	if d.chomp {
		line = strings.TrimLeft(line, "\t")
	}
	return line == d.name
}

// instructionWords splits an instruction into whitespace-separated words the way
// BuildKit's heredoc scan does: quotes and escapes are kept raw, so a quoted run
// (including any spaces) stays one word and an escaped quote does not close it.
// The escape token is a backslash, matching BuildKit, whose heredoc word split
// always uses one regardless of a "# escape=" directive.
func instructionWords(text string) []string {
	var words []string
	var current strings.Builder
	for i := 0; i < len(text); {
		switch text[i] {
		case ' ', '\t', '\n', '\r':
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
			i++
		case '\'':
			run, ok := scanSingleQuoted(text[i:])
			if !ok {
				return nil // BuildKit's lexer errors here; its caller then finds no heredoc
			}
			current.WriteString(text[i : i+run])
			i += run
		case '"':
			run, ok := scanDoubleQuoted(text[i:])
			if !ok {
				return nil
			}
			current.WriteString(text[i : i+run])
			i += run
		case '\\':
			// Outside quotes the escape token keeps the next byte raw, so an
			// escaped quote does not start a quoted run.
			current.WriteByte('\\')
			i++
			if i < len(text) {
				current.WriteByte(text[i])
				i++
			}
		default:
			current.WriteByte(text[i])
			i++
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// scanSingleQuoted returns the length of the single-quoted run starting at
// text[0] ("'"), through its closing quote. A single-quoted shell string has no
// escapes, so the closing quote is the next one; when there is none, ok is false.
func scanSingleQuoted(text string) (int, bool) {
	for i := 1; i < len(text); i++ {
		if text[i] == '\'' {
			return i + 1, true
		}
	}
	return 0, false
}

// scanDoubleQuoted returns the length of the double-quoted run starting at
// text[0] (`"`), through its closing quote; when there is none, ok is false.
// Inside the run a backslash escapes only a double quote, a dollar sign, or
// another backslash, so an escaped quote does not close the run — the same rule
// as BuildKit's shell lexer.
func scanDoubleQuoted(text string) (int, bool) {
	for i := 1; i < len(text); {
		switch text[i] {
		case '"':
			return i + 1, true
		case '\\':
			if i+1 < len(text) && isDoublyEscapeable(text[i+1]) {
				i += 2
			} else {
				i++
			}
		default:
			i++
		}
	}
	return 0, false
}

// escapeToken returns the escape and line-continuation token for the Dockerfile.
// BuildKit honors a "# escape=" directive only among the leading directive
// comment lines: parsing stops at the first line that is not a directive-shaped
// comment (a plain comment, a blank line, or an instruction), so a later
// "# escape=" is not read.
func escapeToken(lines [][]byte) byte {
	for _, raw := range lines {
		line := strings.TrimLeft(lineText(raw), " \t")
		match := directiveLine.FindStringSubmatch(line)
		if match == nil {
			break
		}
		if strings.EqualFold(match[1], "escape") {
			if match[2] == "`" {
				return '`'
			}
			return '\\'
		}
	}
	return '\\'
}

// lineContinues reports whether line ends with the escape token as an unescaped
// line continuation.
func lineContinues(line string, escape byte) bool {
	pattern, ok := lineContinuations[escape]
	return ok && pattern.MatchString(line)
}

// stripContinuation removes a line's trailing continuation escape, mirroring
// BuildKit's trimContinuationCharacter: the escape and any trailing whitespace
// are dropped, and a line that is only the escape becomes empty.
func stripContinuation(line string, escape byte) string {
	return strings.TrimSuffix(strings.TrimRight(line, " \t"), string(escape))
}

// unquoteDelimiter removes the quoting from a heredoc delimiter word the way
// BuildKit's shell lexer does: a quoted run keeps the other quote character
// literally, so <<'E"OF' names the delimiter E"OF rather than EOF, and an
// unquoted escape token is consumed.
func unquoteDelimiter(word string) string {
	var result strings.Builder
	for i := 0; i < len(word); {
		switch word[i] {
		case '\'':
			i++
			for i < len(word) && word[i] != '\'' {
				result.WriteByte(word[i])
				i++
			}
			i++ // skip the closing quote
		case '"':
			i++
			for i < len(word) && word[i] != '"' {
				if word[i] == '\\' && i+1 < len(word) && isDoublyEscapeable(word[i+1]) {
					i++ // drop the escape, keep the escaped character
				}
				result.WriteByte(word[i])
				i++
			}
			i++ // skip the closing quote
		case '\\':
			if i+1 < len(word) {
				result.WriteByte(word[i+1])
				i += 2
				continue
			}
			i++ // a trailing escape is dropped
		default:
			result.WriteByte(word[i])
			i++
		}
	}
	return result.String()
}

// isDoublyEscapeable reports whether a byte is one of the characters a
// backslash escapes inside a double-quoted shell string.
func isDoublyEscapeable(c byte) bool {
	return c == '"' || c == '\\' || c == '$'
}

// lineText returns a line without its trailing newline or carriage return.
func lineText(raw []byte) string {
	return strings.TrimRight(string(raw), "\r\n")
}

// isBlankOrComment reports whether a line is empty or a comment.
func isBlankOrComment(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}
