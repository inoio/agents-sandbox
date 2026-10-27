package image

import (
	"slices"
	"testing"
)

func instructionIndexes(t *testing.T, dockerfile string) []int {
	t.Helper()
	return instructionLineIndices(dockerfileLines([]byte(dockerfile)))
}

func TestInstructionLineIndicesSkipsHeredocBody(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN python3 - <<'PY'\n"+
			"from pathlib import Path\n"+
			"print(Path.cwd())\n"+
			"PY\n"+
			"RUN echo done\n")
	want := []int{0, 1, 5}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesSkipsContinuationLines(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"FROM not-a-stage\n"+
			"FROM alpine\n")
	want := []int{0, 1, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesArithmeticShiftIsNotHeredoc(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN echo $((1<<2))\n"+
			"FROM alpine\n")
	want := []int{0, 1, 2}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesChompHeredocTabTerminator(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN <<-EOF\n"+
			"\techo hi\n"+
			"\tEOF\n"+
			"FROM alpine\n")
	want := []int{0, 1, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesQuotedDelimiterWithSpace(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN python3 - <<'PY DOC'\n"+
			"from pathlib import Path\n"+
			"PY DOC\n"+
			"FROM alpine\n")
	want := []int{0, 1, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesHeredocAfterContinuation(t *testing.T) {
	// A delimiter word glued to the line-continuation escape still names the
	// heredoc, so its body is skipped and the later FROM is found.
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN python3 - <<'PY'\\\n"+
			"    && echo hi\n"+
			"print(1)\n"+
			"PY\n"+
			"FROM alpine\n")
	want := []int{0, 1, 5}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesLiteralQuoteInDelimiter(t *testing.T) {
	// The double quote inside the single-quoted delimiter is literal, so the
	// body ends at E"OF and the later FROM is found.
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN cat <<'E\"OF'\n"+
			"body\n"+
			"E\"OF\n"+
			"FROM alpine\n")
	want := []int{0, 1, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesEscapedQuoteDoesNotHideStages(t *testing.T) {
	// An escaped quote inside a double-quoted RUN argument must not open a
	// phantom heredoc that swallows the later stage (#376 review).
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN echo \"a \\\" <<EOF\"\n"+
			"FROM alpine\n")
	want := []int{0, 1, 2}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesEscapeDirectiveSwitchesContinuation(t *testing.T) {
	got := instructionIndexes(t,
		"# escape=`\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one\\\n"+ // a literal backslash under backtick escape
			"FROM alpine\n")
	want := []int{1, 2, 3} // line 0 is the (skipped) directive comment
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesEscapeDirectiveAfterSyntax(t *testing.T) {
	got := instructionIndexes(t,
		"# syntax=docker/dockerfile:1\n"+
			"# escape=`\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one\\\n"+
			"FROM alpine\n")
	want := []int{2, 3, 4} // the leading directive block is honored, not only line 0
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesEscapeDirectiveSpacesAndIndent(t *testing.T) {
	got := instructionIndexes(t,
		"  # escape = `\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one\\\n"+
			"FROM alpine\n")
	want := []int{1, 2, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesExplicitBackslashEscape(t *testing.T) {
	got := instructionIndexes(t,
		"# escape=\\\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"FROM alpine\n")
	want := []int{1, 2}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesNonDirectiveCommentStopsEscape(t *testing.T) {
	// A plain comment before the directive stops BuildKit from reading it, so the
	// default escape applies and the continuation swallows the following FROM.
	got := instructionIndexes(t,
		"# note\n"+
			"# escape=`\n"+
			"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"FROM alpine\n")
	want := []int{2, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestInstructionLineIndicesSkipsCommentInsideContinuation(t *testing.T) {
	got := instructionIndexes(t,
		"FROM debian:trixie-slim\n"+
			"RUN echo one \\\n"+
			"# comment\n"+
			"echo two\n"+
			"FROM alpine\n")
	want := []int{0, 1, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("instruction starts = %v, want %v", got, want)
	}
}

func TestHeredocDelimiters(t *testing.T) {
	cases := []struct {
		line string
		want []heredocDelimiter
	}{
		{"RUN <<EOF", []heredocDelimiter{{name: "EOF"}}},
		{"RUN <<-EOF", []heredocDelimiter{{name: "EOF", chomp: true}}},
		{"RUN python3 - <<'PY'", []heredocDelimiter{{name: "PY"}}},
		{"RUN python3 - <<'PY DOC'", []heredocDelimiter{{name: "PY DOC"}}},
		{"COPY <<\"FILE\" /dest", []heredocDelimiter{{name: "FILE"}}},
		{"RUN cat <<'E\"OF'", []heredocDelimiter{{name: `E"OF`}}},  // a quote inside single quotes is literal
		{"RUN cat <<\"E'OF\"", []heredocDelimiter{{name: `E'OF`}}}, // and vice versa
		{"RUN cat <<E\\OF", []heredocDelimiter{{name: "EOF"}}},     // an unquoted escape is consumed
		{"RUN cat <<\"A\\\"B\"", []heredocDelimiter{{name: `A"B`}}},
		{"RUN cat <<\"A\\$B\"", []heredocDelimiter{{name: "A$B"}}},
		{"RUN cat <<\"A\\B\"", []heredocDelimiter{{name: `A\B`}}}, // a backslash before any other byte is literal
		{"RUN cat <<EOF\\", []heredocDelimiter{{name: "EOF"}}},    // a trailing escape is dropped
		{"RUN echo $((1<<2))", nil},
		{"RUN echo hi", nil},
		{
			"RUN cat<<EOF",
			nil,
		}, // matches BuildKit: the operator must start the word
		{"RUN echo \"<<EOF\"", nil},                                  // a quoted operator is not a heredoc
		{"RUN echo \"a \\\" <<EOF\"", nil},                           // an escaped quote keeps the run open; no heredoc
		{"RUN echo a\\\"b <<EOF", []heredocDelimiter{{name: "EOF"}}}, // outside quotes the operator is seen
		{
			"RUN python3 - <<'PY",
			nil,
		}, // an unterminated quote yields no heredoc, as BuildKit's lexer errors
		{"RUN echo \"a", nil},    // likewise for a double quote
		{"RUN echo << bar", nil}, // a spaced operator has an empty delimiter name
		{"FROM <<EOF", nil},      // only RUN/COPY/ADD/ONBUILD may contain a heredoc
	}
	for _, tc := range cases {
		got := heredocDelimiters(tc.line)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("heredocDelimiters(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
