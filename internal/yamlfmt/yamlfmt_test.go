package yamlfmt

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapErrIncludesLine(t *testing.T) {
	err := WrapErr(
		"/path/home.yaml",
		errors.New("yaml: line 3, column 7: mapping values are not allowed in this context"),
	)
	for _, want := range []string{"/path/home.yaml", "invalid YAML", "line 3", "column 7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestWrapErrWithoutLine(t *testing.T) {
	err := WrapErr("/path/config.yaml", errors.New("yaml: mapping values are not allowed"))
	if !strings.Contains(err.Error(), "invalid YAML") {
		t.Errorf("expected friendly prefix, got %q", err)
	}
}

func TestWrapErrRetainsCause(t *testing.T) {
	cause := errors.New("yaml: line 2: did not find expected node content")
	if !errors.Is(WrapErr("/x", cause), cause) {
		t.Error("expected %w to preserve the wrapped cause")
	}
}
