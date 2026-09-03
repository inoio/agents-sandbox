package mounts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

// Guest mount points that opencode-sandbox manages itself. They are fixed by
// the project VM layout and shared by the packages that build or inspect it.
const (
	// VMHomeDir is the sandbox home mount point (a named volume).
	VMHomeDir = "/home/dev"
	// WorkspaceMountPath is the mount point of the host project bind mount.
	WorkspaceMountPath = "/workspace"
	// TmpMountPath is the mount point used for the sandbox tmpfs.
	TmpMountPath = "/tmp"
)

// BindMount maps a host directory into the sandbox at an absolute guest path.
type BindMount struct {
	Source   string `mapstructure:"source"`
	Readonly bool   `mapstructure:"readonly"`
}

// Mounts maps absolute guest target paths to host bind-mount settings.
type Mounts map[string]BindMount

// stringToBindMountHook decodes the short mount form (`target: ~/.m2`) into a
// writable BindMount. The long form (`target: {source, readonly}`) needs no
// hook; mapstructure decodes it via the BindMount struct tags.
func stringToBindMountHook() mapstructure.DecodeHookFuncType {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String || t != reflect.TypeFor[BindMount]() {
			return data, nil
		}
		source, ok := data.(string)
		if !ok {
			return nil, errors.New("bind mount source must be a string")
		}
		return BindMount{Source: source, Readonly: false}, nil
	}
}

// DecodeMounts parses the launcher config's short and long mount forms,
// delegating shape parsing to mapstructure. Unknown fields are ignored
// (lenient); semantic validation is left to ResolveBindMounts.
func DecodeMounts(raw any) (Mounts, error) {
	if raw == nil {
		return Mounts{}, nil
	}
	var mounts Mounts
	//nolint:exhaustruct // DecoderConfig has many optional fields we leave zeroed.
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: stringToBindMountHook(),
		Result:     &mounts,
	})
	if err != nil {
		return nil, err
	}
	if err := decoder.Decode(raw); err != nil {
		return nil, err
	}
	return mounts, nil
}

// managedMountTargets are the guest paths opencode-sandbox mounts itself. A
// configured mount may not replace them, nor shadow a parent of them.
//
//nolint:gochecknoglobals // package-level constant slice
var managedMountTargets = []string{VMHomeDir, WorkspaceMountPath, TmpMountPath}

// exclusiveMountTargets are managed mounts whose contents must stay visible,
// so nesting a configured mount inside them is rejected as well. The home
// volume is absent on purpose: mounting into it (e.g., /home/dev/.m2) is the
// primary use case.
//
//nolint:gochecknoglobals // package-level constant slice
var exclusiveMountTargets = []string{WorkspaceMountPath, TmpMountPath}

// ResolveBindMounts expands host paths and validates configured bind mounts,
// returning them keyed by cleaned guest path.
func ResolveBindMounts(mounts Mounts) (Mounts, error) {
	resolved := make(map[string]BindMount, len(mounts))
	for rawTarget, mount := range mounts {
		target, err := resolveMountTarget(rawTarget)
		if err != nil {
			return nil, err
		}
		source, err := resolveMountSource(mount.Source)
		if err != nil {
			return nil, fmt.Errorf("mount %q: %w", target, err)
		}
		if _, exists := resolved[target]; exists {
			return nil, fmt.Errorf("duplicate mount target %q", target)
		}
		mount.Source = source
		resolved[target] = mount
	}
	return resolved, nil
}

// Fingerprint returns a stable SHA-256 hex digest of the resolved mounts, for
// detecting changes across runs. Mounts are baked into the VM at creation
// time, so a differing fingerprint requires recreating the VM.
func Fingerprint(mounts Mounts) string {
	lines := make([]string, 0, len(mounts))
	for target, mount := range mounts {
		lines = append(lines, fmt.Sprintf("%s=%s,ro=%t", target, mount.Source, mount.Readonly))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// MountTargets returns the configured guest paths in sorted order.
func MountTargets(mounts Mounts) []string {
	targets := make([]string, 0, len(mounts))
	for target := range mounts {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// resolveMountTarget validates a guest mount path and returns it cleaned.
func resolveMountTarget(target string) (string, error) {
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("mount target %q must be an absolute path", target)
	}
	clean := filepath.Clean(target)
	if clean == "/" {
		return "", errors.New(`mount target "/" is not allowed`)
	}
	for _, managed := range managedMountTargets {
		if clean == managed || isWithin(managed, clean) {
			return "", fmt.Errorf("mount target %q conflicts with managed mount %q", target, managed)
		}
	}
	for _, exclusive := range exclusiveMountTargets {
		if isWithin(clean, exclusive) {
			return "", fmt.Errorf("mount target %q would shadow content of managed mount %q", target, exclusive)
		}
	}
	return clean, nil
}

// resolveMountSource expands a host source path and verifies it is a directory.
func resolveMountSource(source string) (string, error) {
	path, err := expandHome(source)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("source %q must be a directory", path)
	}
	return path, nil
}

// isWithin reports whether path lies inside base.
func isWithin(path, base string) bool {
	return strings.HasPrefix(path, base+string(filepath.Separator))
}

func expandHome(path string) (string, error) {
	if path == "" {
		return "", errors.New("source must not be empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("source %q must be absolute or start with ~/", path)
	}
	return filepath.Clean(path), nil
}
