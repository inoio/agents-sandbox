package msb

import "context"

// SandboxFS is the subset of the sandbox filesystem operations used by the launcher.
type SandboxFS interface {
	Exists(ctx context.Context, path string) (bool, error)
	ReadString(ctx context.Context, path string) (string, error)
	Mkdir(ctx context.Context, path string) error
	Write(ctx context.Context, path string, data []byte) error
	Read(ctx context.Context, path string) ([]byte, error)
	Remove(ctx context.Context, path string) error
}
