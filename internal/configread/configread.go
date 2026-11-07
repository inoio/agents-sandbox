// Package configread reads launcher config, secret and snippet files under a
// size bound, so a hostile or accidental huge file cannot drive unbounded
// allocation in the parsers that consume it.
package configread

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxBytes is the largest file ReadFile returns. Launcher config, secret and
// snippet files are kilobytes; the bound only rejects a pathological file. The
// parsers' own depth limits do not bound total input size, so this is the site
// where that is capped.
const MaxBytes = 16 << 20

// ErrTooLarge reports a file larger than MaxBytes.
var ErrTooLarge = errors.New("config file exceeds the maximum size")

// ReadFile reads path, returning an error wrapping ErrTooLarge when the file is
// larger than MaxBytes. It reads one byte past the bound so a file exactly
// MaxBytes long is accepted.
func ReadFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// gomutants:disable-next-line INTEGER_INCREMENT reason="one byte past the bound detects an oversized file; reading a second byte past changes nothing because any file larger than MaxBytes is rejected either way"
	data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrTooLarge, MaxBytes)
	}
	return data, nil
}
