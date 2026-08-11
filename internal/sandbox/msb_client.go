// Package sandbox provides backward-compatible type aliases for the msb
// subpackage. New code should import "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb".
// Existing callers of sandbox.MsbClient, sandbox.MockMsbClient, etc. will
// continue to work without modification.
package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
)

//nolint:revive // 'SandboxFS' stutters as 'sandbox.SandboxFS'
type SandboxFS = msb.SandboxFS

// MsbClient is a re-export of msb.Client for backward compatibility.
type MsbClient = msb.Client

// MockMsbClient is a re-export of msb.MockMsbClient for backward compatibility.
type MockMsbClient = msb.MockMsbClient

// MockRemoveImageCall is a re-export for backward compatibility.
type MockRemoveImageCall = msb.MockRemoveImageCall

//nolint:revive // 'SandboxHandle' stutters as 'sandbox.SandboxHandle'
type SandboxHandle = msb.SandboxHandle

// Sandbox is a re-export of msb.Sandbox for backward compatibility.
type Sandbox = msb.Sandbox

// ShellResult is a re-export of msb.ShellResult for backward compatibility.
type ShellResult = msb.ShellResult

// VolumeHandle is a re-export of msb.VolumeHandle for backward compatibility.
type VolumeHandle = msb.VolumeHandle

// ImageHandle is a re-export of msb.ImageHandle for backward compatibility.
type ImageHandle = msb.ImageHandle

// MockSandboxHandle is a re-export of msb.MockSandboxHandle for backward compatibility.
type MockSandboxHandle = msb.MockSandboxHandle

// MockSandbox is a re-export of msb.MockSandbox for backward compatibility.
type MockSandbox = msb.MockSandbox

// MockVolumeHandle is a re-export of msb.MockVolumeHandle for backward compatibility.
type MockVolumeHandle = msb.MockVolumeHandle

// MockImageHandle is a re-export of msb.MockImageHandle for backward compatibility.
type MockImageHandle = msb.MockImageHandle

// SetNewMsbClient replaces the internal msb client factory for backward compatibility.
// It returns the original factory to restore later.
func SetNewMsbClient(f func() MsbClient) func() MsbClient {
	return msb.ResetGetFn(f)
}

// WithMsbMock re-exports msb.WithMsbMock for backward compatibility.
func WithMsbMock(t *testing.T, mock MsbClient) {
	msb.WithMsbMock(t, mock)
}

// TestResult is a re-export of msb.TestResult for tests.
type TestResult = msb.TestResult

// NewTestResult creates a ShellResult for tests.
func NewTestResult(success bool, exitCode int, stdout, stderr string, stdoutBytes []byte) ShellResult {
	return msb.NewTestResult(success, exitCode, stdout, stderr, stdoutBytes)
}

// SandboxOpts is a re-export of msb.SandboxOpts for tests.
//
//nolint:revive // re-export of msb type name for tests
type SandboxOpts = msb.SandboxOpts

// TestFS is a re-export of msb.TestFS for tests.
type TestFS = msb.TestFS
