package runtime

import (
	"reflect"
	"testing"
)

// TestWithoutReuseHomeDropsTheReuseVariable pins the child-environment rule: a
// child process a test spawns must not inherit the reuse-home variable, or its
// TestMain re-enters the parent's exclusive reuse lock and blocks forever.
func TestWithoutReuseHomeDropsTheReuseVariable(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		reuseRuntimeHomeEnv + "=/tmp/asb-reuse/msb",
		"HOME=/home/dev",
	}

	got := WithoutReuseHome(env)
	want := []string{"PATH=/usr/bin", "HOME=/home/dev"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WithoutReuseHome() = %v, want %v", got, want)
	}
}

// TestWithoutReuseHomeKeepsUnrelatedEntries pins that only the exact variable is
// dropped: a different key sharing the prefix, and an entry with no '=', stay.
func TestWithoutReuseHomeKeepsUnrelatedEntries(t *testing.T) {
	env := []string{
		"NO_EQUALS",
		reuseRuntimeHomeEnv + "_OTHER=keep",
		"MSB_HOME=/tmp/asb-reuse/msb",
	}

	got := WithoutReuseHome(env)
	if !reflect.DeepEqual(got, env) {
		t.Errorf("WithoutReuseHome() = %v, want %v unchanged", got, env)
	}
}
