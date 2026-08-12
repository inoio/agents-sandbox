package sandbox

import "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"

// Re-exported options module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb continues to compile without changing its import paths.

type ReapPolicy = options.ReapPolicy
type WorktreeSpec = options.WorktreeSpec
type RunOptions = options.RunOptions
