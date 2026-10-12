package builder

import (
	"context"
	"strings"
	"testing"
)

type fakeDockerCLI struct {
	calls   [][]string
	outputs map[string]string
	err     error
}

func (f *fakeDockerCLI) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.err != nil {
		return "", f.err
	}
	return f.outputs[strings.Join(args[:min(2, len(args))], ":")], nil
}

func TestFakeDockerCLIRecordsAndKeysOutputs(t *testing.T) {
	fake := &fakeDockerCLI{outputs: map[string]string{"image:inspect": "abc"}}
	out, err := fake.run(context.Background(), "image", "inspect", "tag")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "abc" {
		t.Errorf("output = %q, want abc", out)
	}
	if len(fake.calls) != 1 || strings.Join(fake.calls[0], " ") != "image inspect tag" {
		t.Errorf("calls = %v, want one [image inspect tag] call", fake.calls)
	}
}

func TestRealDockerCLIReportsCommandOnFailure(t *testing.T) {
	_, err := (&realDockerCLI{}).run(context.Background(), "info", "--bogus")
	if err == nil {
		t.Fatal("want error for an invalid docker invocation")
	}
	if !strings.Contains(err.Error(), "docker info --bogus") {
		t.Errorf("error %q does not name the command", err)
	}
}
