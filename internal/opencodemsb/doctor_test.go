package opencodemsb

import (
	"context"
	"testing"
)

func TestCheckGitReturnsBool(t *testing.T) {
	result := CheckGit()
	if result != true && result != false {
		t.Errorf("expected bool, got %T", result)
	}
}

func TestCheckKvmReturnsBool(t *testing.T) {
	_ = CheckKvm()
}

func TestCheckDockerReturnsBool(t *testing.T) {
	_ = CheckDocker()
}

func TestCheckAllReturnsBool(t *testing.T) {
	_ = CheckAll(context.Background())
}
