# opencode-msb Go Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite the Python `inoio-sandbox` launcher in Go using the microsandbox Go SDK, producing a `opencode-msb` binary that is a drop-in replacement for the Python CLI.

**Architecture:** Go module `github.com/inoio/opencode-msb` at repo root. Entrypoint at `cmd/opencode-msb/main.go`, internal packages under `internal/opencodemsb/`. Uses the microsandbox Go SDK for sandbox lifecycle/volumes/secrets/FS/image-cache, the moby Docker client for image build/save/inspect, and `os/exec` for git worktree operations (go-git lacks linked-worktree support).

**Tech Stack:** Go 1.26, `github.com/spf13/cobra`, `github.com/titanous/json5`, `github.com/superradcompany/microsandbox/sdk/go`, `github.com/docker/docker/client`

## Global Constraints

- Module path: `github.com/inoio/opencode-msb`
- Go 1.26+ required
- Binary name and command name: `opencode-msb`
- State directory: `~/.local/share/opencode-msb/`
- Image tag prefix: `opencode-msb/runner`
- Python source under `src/inoio_sandbox/` remains untouched for parity diffing
- Embedded data files (`data/Dockerfile`, `data/provider-config.json`) are byte-identical copies of `src/inoio_sandbox/data/`
- Test command: `go test ./...`
- Lint commands: `go vet ./...`, `gofmt -l .`, `golangci-lint run`
- No comments in code unless code is not self-explanatory (per AGENTS.md)
- TDD: write failing test first, then implement
- **Use `microsandbox.EnsureInstalled(ctx)` before any sandbox creation call** — not `IsInstalled()`. `EnsureInstalled` downloads the runtime if missing; `IsInstalled` only checks. Call it at startup in `doctor` and before `CreateSandbox` in `runner`.

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | Module definition |
| `cmd/opencode-msb/main.go` | Entrypoint, calls `opencodemsb.Execute()` |
| `internal/opencodemsb/data.go` | `//go:embed` for Dockerfile + provider-config.json |
| `internal/opencodemsb/data/Dockerfile` | Copy of Python Dockerfile |
| `internal/opencodemsb/data/provider-config.json` | Copy of Python provider config |
| `internal/opencodemsb/log.go` | Colored stderr output (Info/Warn/Error/Timing) |
| `internal/opencodemsb/log_test.go` | Tests for log |
| `internal/opencodemsb/sysinfo.go` | CPU count + `/proc/meminfo` reader |
| `internal/opencodemsb/sysinfo_test.go` | Tests for sysinfo |
| `internal/opencodemsb/config.go` | JSON5 config merge |
| `internal/opencodemsb/config_test.go` | Tests for config |
| `internal/opencodemsb/secrets.go` | Env-var → SDK SecretEntry mapping |
| `internal/opencodemsb/secrets_test.go` | Tests for secrets |
| `internal/opencodemsb/worktree.go` | Git worktree operations (os/exec) |
| `internal/opencodemsb/worktree_test.go` | Tests for worktree |
| `internal/opencodemsb/doctor.go` | Preflight checks |
| `internal/opencodemsb/doctor_test.go` | Tests for doctor |
| `internal/opencodemsb/image.go` | Docker build (moby) + SDK image cache load |
| `internal/opencodemsb/image_test.go` | Tests for image |
| `internal/opencodemsb/volumes.go` | SDK volumes + host-dir fallback + prefill |
| `internal/opencodemsb/volumes_test.go` | Tests for volumes |
| `internal/opencodemsb/cmd.go` | Cobra root + doctor/run subcommands + timing |
| `internal/opencodemsb/cmd_test.go` | Tests for cmd |
| `internal/opencodemsb/runner.go` | Main orchestration flow |
| `internal/opencodemsb/runner_test.go` | Tests for runner |

---

### Task 1: Module scaffold + embedded data

**Files:**
- Create: `go.mod`
- Create: `cmd/opencode-msb/main.go`
- Create: `internal/opencodemsb/data.go`
- Create: `internal/opencodemsb/data/Dockerfile`
- Create: `internal/opencodemsb/data/provider-config.json`
- Create: `internal/opencodemsb/cmd.go` (stub)

**Interfaces:**
- Produces: `opencodemsb.EmbeddedDockerfile` (var `[]byte`), `opencodemsb.EmbeddedProviderConfig` (var `[]byte`), `opencodemsb.Execute() error`

- [ ] **Step 1: Initialize the Go module**

Run:
```bash
go mod init github.com/inoio/opencode-msb
```
Expected: creates `go.mod` with `module github.com/inoio/opencode-msb` and `go 1.26`.

- [ ] **Step 2: Copy data files from Python source**

```bash
mkdir -p internal/opencodemsb/data
cp src/inoio_sandbox/data/Dockerfile internal/opencodemsb/data/Dockerfile
cp src/inoio_sandbox/data/provider-config.json internal/opencodemsb/data/provider-config.json
```

- [ ] **Step 3: Create the embed file**

Create `internal/opencodemsb/data.go`:

```go
package opencodemsb

import _ "embed"

//go:embed data/Dockerfile
var EmbeddedDockerfile []byte

//go:embed data/provider-config.json
var EmbeddedProviderConfig []byte
```

- [ ] **Step 4: Create main.go**

Create `cmd/opencode-msb/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/inoio/opencode-msb/internal/opencodemsb"
)

func main() {
	if err := opencodemsb.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Create a temporary stub Execute function**

Create `internal/opencodemsb/cmd.go` (temporary stub, replaced in Task 10):

```go
package opencodemsb

func Execute() error {
	return nil
}
```

- [ ] **Step 6: Verify it builds**

Run: `go build ./cmd/opencode-msb`
Expected: builds successfully.

- [ ] **Step 7: Commit**

```bash
git add go.mod cmd/ internal/opencodemsb/data.go internal/opencodemsb/data/ internal/opencodemsb/cmd.go
git commit -m "feat: scaffold Go module with embedded data files"
```

---

### Task 2: log.go — colored stderr output

**Files:**
- Create: `internal/opencodemsb/log.go`
- Create: `internal/opencodemsb/log_test.go`

**Interfaces:**
- Produces: `logger` struct, `newLogger(w io.Writer, color bool) *logger`, `(*logger) Info(msg string)`, `(*logger) Warn(msg string)`, `(*logger) Error(msg string)`, `(*logger) Timing(label string, elapsed time.Duration)`
- Note: package-level `warn(msg)` and `errorMsg(msg)` helpers are added in Task 5 (secrets) and Task 7 (doctor) respectively, using a shared `logOut` variable. Task 2 only creates the `logger` type.

- [ ] **Step 1: Write the failing test**

Create `internal/opencodemsb/log_test.go`:

```go
package opencodemsb

import (
	"bytes"
	"strings"
	"testing"
)

func TestInfoWritesWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, false)
	l.Info("hello")
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI codes when color disabled, got %q", out)
	}
}

func TestWarnWritesWithYellow(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, true)
	l.Warn("danger")
	out := buf.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected output to contain 'danger', got %q", out)
	}
	if !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected yellow ANSI code, got %q", out)
	}
}

func TestErrorWritesWithRed(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, true)
	l.Error("boom")
	out := buf.String()
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI code, got %q", out)
	}
}

func TestTimingFormatsDuration(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, false)
	l.Timing("preflight", 1250000000)
	out := buf.String()
	if !strings.Contains(out, "[timing] preflight: 1.250s") {
		t.Errorf("expected timing line, got %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestInfo -v`
Expected: FAIL — `newLogger` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/opencodemsb/log.go`:

```go
package opencodemsb

import (
	"fmt"
	"io"
	"time"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
)

type logger struct {
	w     io.Writer
	color bool
}

func newLogger(w io.Writer, color bool) *logger {
	return &logger{w: w, color: color}
}

func (l *logger) write(color, msg string) {
	if l.color {
		fmt.Fprintf(l.w, "%s%s%s\n", color, msg, ansiReset)
	} else {
		fmt.Fprintln(l.w, msg)
	}
}

func (l *logger) Info(msg string)  { l.write("", msg) }
func (l *logger) Warn(msg string)  { l.write(ansiYellow, msg) }
func (l *logger) Error(msg string) { l.write(ansiRed, msg) }

func (l *logger) Timing(label string, elapsed time.Duration) {
	fmt.Fprintf(l.w, "[timing] %s: %.3fs\n", label, elapsed.Seconds())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS for all log tests.

- [ ] **Step 5: Commit**

```bash
git add internal/opencodemsb/log.go internal/opencodemsb/log_test.go
git commit -m "feat: add colored stderr logger"
```

---

### Task 3: sysinfo.go — host resources

**Files:**
- Create: `internal/opencodemsb/sysinfo.go`
- Create: `internal/opencodemsb/sysinfo_test.go`

**Interfaces:**
- Produces: `NumCPUs() uint8`, `TotalMemoryGiB() int`, `parseMemInfo(data []byte) (totalKB int, ok bool)`

- [ ] **Step 1: Write the failing test**

Create `internal/opencodemsb/sysinfo_test.go`:

```go
package opencodemsb

import (
	"testing"
)

func TestParseMemInfo(t *testing.T) {
	data := []byte("MemTotal:       16384000 kB\nMemFree:          123456 kB\nMemAvailable:    8000000 kB\n")
	totalKB, ok := parseMemInfo(data)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestParseMemInfoMissing(t *testing.T) {
	data := []byte("MemFree: 123 kB\n")
	_, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal missing")
	}
}

func TestParseMemInfoEmpty(t *testing.T) {
	_, ok := parseMemInfo(nil)
	if ok {
		t.Error("expected ok=false for empty input")
	}
}

func TestNumCPUsAtLeastOne(t *testing.T) {
	cpus := NumCPUs()
	if cpus < 1 {
		t.Errorf("expected at least 1 CPU, got %d", cpus)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestParseMemInfo -v`
Expected: FAIL — `parseMemInfo` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/opencodemsb/sysinfo.go`:

```go
package opencodemsb

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

func NumCPUs() uint8 {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	if n > 255 {
		n = 255
	}
	return uint8(n)
}

func parseMemInfo(data []byte) (totalKB int, ok bool) {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, false
		}
		return kb, true
	}
	return 0, false
}

func TotalMemoryGiB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	totalKB, ok := parseMemInfo(data)
	if !ok {
		return 0
	}
	return totalKB / (1024 * 1024)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/opencodemsb/sysinfo.go internal/opencodemsb/sysinfo_test.go
git commit -m "feat: add system info (CPU count + meminfo reader)"
```

---

### Task 4: config.go — JSON5 config merge

**Files:**
- Create: `internal/opencodemsb/config.go`
- Create: `internal/opencodemsb/config_test.go`

**Interfaces:**
- Produces: `LoadProviderConfig(data []byte) (map[string]any, error)`, `DeepMerge(base, override map[string]any) map[string]any`, `BuildMergedConfig(userDir, projectDir string, providerConfig map[string]any) (map[string][]byte, error)`
- Consumes: `EmbeddedProviderConfig` (from Task 1)

- [ ] **Step 1: Add json5 dependency**

Run:
```bash
go get github.com/titanous/json5
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `internal/opencodemsb/config_test.go`:

```go
package opencodemsb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProviderConfigParsesJSON5(t *testing.T) {
	cfg, err := LoadProviderConfig(EmbeddedProviderConfig)
	if err != nil {
		t.Fatalf("LoadProviderConfig failed: %v", err)
	}
	litellm, ok := cfg["provider"].(map[string]any)["litellm"].(map[string]any)
	if !ok {
		t.Fatal("expected provider.litellm to exist")
	}
	if litellm["name"] != "LiteLLM" {
		t.Errorf("expected name=LiteLLM, got %v", litellm["name"])
	}
}

func TestDeepMergeNested(t *testing.T) {
	base := map[string]any{
		"a": map[string]any{"x": 1, "y": 2},
		"b": "keep",
	}
	override := map[string]any{
		"a": map[string]any{"y": 99, "z": 3},
		"c": "new",
	}
	result := DeepMerge(base, override)
	a := result["a"].(map[string]any)
	if a["x"] != 1 {
		t.Errorf("expected x=1 (from base), got %v", a["x"])
	}
	if a["y"] != 99 {
		t.Errorf("expected y=99 (overridden), got %v", a["y"])
	}
	if a["z"] != 3 {
		t.Errorf("expected z=3 (new), got %v", a["z"])
	}
	if result["b"] != "keep" {
		t.Errorf("expected b='keep', got %v", result["b"])
	}
	if result["c"] != "new" {
		t.Errorf("expected c='new', got %v", result["c"])
	}
}

func TestBuildMergedConfigCreatesOpencodeJsoncWhenAbsent(t *testing.T) {
	providerCfg := map[string]any{
		"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}},
	}
	files, err := BuildMergedConfig("", "", providerCfg)
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	data, ok := files["opencode.jsonc"]
	if !ok {
		t.Fatal("expected opencode.jsonc to be created")
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("opencode.jsonc is not valid JSON: %v", err)
	}
	if _, ok := parsed["provider"]; !ok {
		t.Error("expected provider key in opencode.jsonc")
	}
}

func TestBuildMergedConfigMergesUserOpencodeJsonc(t *testing.T) {
	tmp := t.TempDir()
	userCfg := map[string]any{"theme": "dark"}
	userBytes, _ := json.Marshal(userCfg)
	os.WriteFile(filepath.Join(tmp, "opencode.jsonc"), userBytes, 0o644)

	providerCfg := map[string]any{
		"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}},
	}
	files, err := BuildMergedConfig(tmp, "", providerCfg)
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	data := files["opencode.jsonc"]
	var parsed map[string]any
	json.Unmarshal(data, &parsed)
	if parsed["theme"] != "dark" {
		t.Errorf("expected theme=dark to be preserved, got %v", parsed["theme"])
	}
	if _, ok := parsed["provider"]; !ok {
		t.Error("expected provider to be merged in")
	}
}

func TestBuildMergedConfigCopiesNonJsonFiles(t *testing.T) {
	userDir := t.TempDir()
	os.WriteFile(filepath.Join(userDir, "instructions.txt"), []byte("hello"), 0o644)

	files, err := BuildMergedConfig(userDir, "", map[string]any{})
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	if string(files["instructions.txt"]) != "hello" {
		t.Errorf("expected instructions.txt content 'hello', got %q", files["instructions.txt"])
	}
}

func TestBuildMergedConfigProjectDirOverridesUserDir(t *testing.T) {
	userDir := t.TempDir()
	projectDir := t.TempDir()
	userBytes, _ := json.Marshal(map[string]any{"val": "user"})
	projBytes, _ := json.Marshal(map[string]any{"val": "project"})
	os.WriteFile(filepath.Join(userDir, "opencode.jsonc"), userBytes, 0o644)
	os.WriteFile(filepath.Join(projectDir, "opencode.jsonc"), projBytes, 0o644)

	files, err := BuildMergedConfig(userDir, projectDir, map[string]any{})
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal(files["opencode.jsonc"], &parsed)
	if parsed["val"] != "project" {
		t.Errorf("expected project override, got %v", parsed["val"])
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestLoad -v`
Expected: FAIL — `LoadProviderConfig` undefined.

- [ ] **Step 4: Write minimal implementation**

Create `internal/opencodemsb/config.go`:

```go
package opencodemsb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	json5 "github.com/titanous/json5"
)

func LoadProviderConfig(data []byte) (map[string]any, error) {
	var cfg map[string]any
	if err := json5.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func DeepMerge(base, override map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		if existing, ok := result[k]; ok {
			if existingMap, ok := existing.(map[string]any); ok {
				if overrideMap, ok := v.(map[string]any); ok {
					result[k] = DeepMerge(existingMap, overrideMap)
					continue
				}
			}
		}
		result[k] = v
	}
	return result
}

func isJSONFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".json" || ext == ".jsonc"
}

func scanJSONFiles(dirs ...string) map[string]map[string]any {
	files := make(map[string]map[string]any)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if entry.IsDir() || !isJSONFile(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var cfg map[string]any
			if err := json5.Unmarshal(data, &cfg); err != nil {
				continue
			}
			name := entry.Name()
			files[name] = DeepMerge(files[name], cfg)
		}
	}
	return files
}

func scanOtherFiles(dirs ...string) map[string][]byte {
	files := make(map[string][]byte)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if entry.IsDir() || isJSONFile(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			files[entry.Name()] = data
		}
	}
	return files
}

func BuildMergedConfig(userDir, projectDir string, providerConfig map[string]any) (map[string][]byte, error) {
	jsonFiles := scanJSONFiles(userDir, projectDir)
	otherFiles := scanOtherFiles(userDir, projectDir)

	providerBranch := map[string]any{
		"provider": providerConfig["provider"],
	}

	result := make(map[string][]byte)
	for name, cfg := range jsonFiles {
		var merged map[string]any
		if name == "opencode.jsonc" || name == "opencode.json" {
			merged = DeepMerge(cfg, providerBranch)
		} else {
			merged = cfg
		}
		data, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return nil, err
		}
		result[name] = data
	}

	if _, hasJsonc := result["opencode.jsonc"]; !hasJsonc {
		if _, hasJson := result["opencode.json"]; !hasJson {
			data, err := json.MarshalIndent(providerBranch, "", "  ")
			if err != nil {
				return nil, err
			}
			result["opencode.jsonc"] = data
		}
	}

	for name, data := range otherFiles {
		result[name] = data
	}

	return result, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/opencodemsb/config.go internal/opencodemsb/config_test.go
git commit -m "feat: add JSON5 config merge with provider injection"
```

---

### Task 5: secrets.go — env-var → SDK secrets

**Files:**
- Create: `internal/opencodemsb/secrets.go`
- Create: `internal/opencodemsb/secrets_test.go`

**Interfaces:**
- Produces: `SecretMap` (var `map[string]string`), `BuildSecrets() []microsandbox.SecretEntry`, plus package-level helpers `warn(msg string)` and `errorMsg(msg string)` and `logOut`/`logMu` (shared logger state used by all packages)
- Consumes: `github.com/superradcompany/microsandbox/sdk/go`

- [ ] **Step 1: Add microsandbox SDK dependency**

Run:
```bash
go get github.com/superradcompany/microsandbox/sdk/go
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `internal/opencodemsb/secrets_test.go`:

```go
package opencodemsb

import (
	"os"
	"testing"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

func TestSecretMapContents(t *testing.T) {
	if SecretMap["LITELLM_API_KEY"] != "litellm.inoio.de" {
		t.Errorf("expected LITELLM_API_KEY -> litellm.inoio.de, got %v", SecretMap["LITELLM_API_KEY"])
	}
	if SecretMap["GITHUB_TOKEN"] != "github.com" {
		t.Errorf("expected GITHUB_TOKEN -> github.com, got %v", SecretMap["GITHUB_TOKEN"])
	}
}

func TestBuildSecretsSkipsEmptyEnv(t *testing.T) {
	os.Unsetenv("LITELLM_API_KEY")
	os.Unsetenv("GITHUB_TOKEN")
	secrets := BuildSecrets()
	if len(secrets) != 0 {
		t.Errorf("expected 0 secrets when no env vars set, got %d", len(secrets))
	}
}

func TestBuildSecretsCreatesEntryForSetEnv(t *testing.T) {
	os.Setenv("LITELLM_API_KEY", "sk-test-123")
	os.Unsetenv("GITHUB_TOKEN")
	defer os.Unsetenv("LITELLM_API_KEY")

	secrets := BuildSecrets()
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].EnvVar != "LITELLM_API_KEY" {
		t.Errorf("expected EnvVar=LITELLM_API_KEY, got %q", secrets[0].EnvVar)
	}
	if secrets[0].Value != "sk-test-123" {
		t.Errorf("expected Value=sk-test-123, got %q", secrets[0].Value)
	}
	if len(secrets[0].AllowHosts) != 1 || secrets[0].AllowHosts[0] != "litellm.inoio.de" {
		t.Errorf("expected AllowHosts=[litellm.inoio.de], got %v", secrets[0].AllowHosts)
	}
}

func TestBuildSecretsCreatesMultipleEntries(t *testing.T) {
	os.Setenv("LITELLM_API_KEY", "key1")
	os.Setenv("GITHUB_TOKEN", "ghp_test")
	defer os.Unsetenv("LITELLM_API_KEY")
	defer os.Unsetenv("GITHUB_TOKEN")

	secrets := BuildSecrets()
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(secrets))
	}
}

func TestSecretEntryMatchesSDKFactory(t *testing.T) {
	entry := m.Secret.Env("TEST_VAR", "val", m.SecretEnvOptions{AllowHosts: []string{"example.com"}})
	if entry.EnvVar != "TEST_VAR" {
		t.Errorf("expected EnvVar=TEST_VAR, got %q", entry.EnvVar)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestSecretMap -v`
Expected: FAIL — `SecretMap` undefined.

- [ ] **Step 4: Write minimal implementation**

Create `internal/opencodemsb/secrets.go`:

```go
package opencodemsb

import (
	"os"
	"sync"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

var SecretMap = map[string]string{
	"LITELLM_API_KEY": "litellm.inoio.de",
	"GITHUB_TOKEN":    "github.com",
}

var (
	logMu  sync.Mutex
	logOut = newLogger(os.Stderr, false)
)

func warn(msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	logOut.Warn(msg)
}

func errorMsg(msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	logOut.Error(msg)
}

func BuildSecrets() []m.SecretEntry {
	var secrets []m.SecretEntry
	for envVar, host := range SecretMap {
		value := os.Getenv(envVar)
		if value == "" {
			warn(envVar + " not set; related provider/API may fail.")
			continue
		}
		secrets = append(secrets, m.Secret.Env(
			envVar,
			value,
			m.SecretEnvOptions{AllowHosts: []string{host}},
		))
	}
	return secrets
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/opencodemsb/secrets.go internal/opencodemsb/secrets_test.go
git commit -m "feat: add secret env-var to SDK SecretEntry mapping"
```

---

### Task 6: worktree.go — git worktree operations

**Files:**
- Create: `internal/opencodemsb/worktree.go`
- Create: `internal/opencodemsb/worktree_test.go`

**Interfaces:**
- Produces: `ProjectSlug() string`, `BranchName(cwd string) (string, error)`, `CurrentWorktreePath(cwd string) (string, error)`, `EnsureWorktree(repoRoot, stateDir, projectSlug, branch string) (string, error)`, `RemoveWorktree(path string) error`, `WorktreePath(stateDir, projectSlug, branch string) string`, `BranchSlug(branch string) string`

- [ ] **Step 1: Write the failing test**

Create `internal/opencodemsb/worktree_test.go`:

```go
package opencodemsb

import (
	"path/filepath"
	"testing"
)

func TestBranchSlugReplacesSlashes(t *testing.T) {
	got := BranchSlug("feature/foo/bar")
	if got != "feature-foo-bar" {
		t.Errorf("expected 'feature-foo-bar', got %q", got)
	}
}

func TestBranchSlugNoChange(t *testing.T) {
	got := BranchSlug("main")
	if got != "main" {
		t.Errorf("expected 'main', got %q", got)
	}
}

func TestWorktreePathConstruction(t *testing.T) {
	got := WorktreePath("/tmp/state", "p-abc123", "main")
	expected := filepath.Join("/tmp/state", "worktrees", "p-abc123", "main")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestWorktreePathWithBranchSlug(t *testing.T) {
	got := WorktreePath("/tmp/state", "p-abc", BranchSlug("feat/x"))
	expected := filepath.Join("/tmp/state", "worktrees", "p-abc", "feat-x")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestBranchSlug -v`
Expected: FAIL — `BranchSlug` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/opencodemsb/worktree.go`:

```go
package opencodemsb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ProjectSlug() string {
	commonDir, err := gitCommonDir(".")
	if err != nil || commonDir == "" {
		cwd, _ := filepath.Abs(".")
		warn("not inside a git repo; using CWD hash as project slug.")
		h := sha256.Sum256([]byte(cwd))
		return "p-" + hex.EncodeToString(h[:])[:8]
	}
	abs, _ := filepath.Abs(commonDir)
	h := sha256.Sum256([]byte(abs))
	return "p-" + hex.EncodeToString(h[:])[:8]
}

func BranchSlug(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

func gitCommonDir(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Abs(p)
}

func BranchName(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("unable to determine current git branch from %s: %w", cwd, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func CurrentWorktreePath(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(string(out)))
}

func WorktreePath(stateDir, projectSlug, branch string) string {
	return filepath.Join(stateDir, "worktrees", projectSlug, branch)
}

func isGitWorktree(path string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = path
	return cmd.Run() == nil
}

func EnsureWorktree(repoRoot, stateDir, projectSlug, branch string) (string, error) {
	target := WorktreePath(stateDir, projectSlug, BranchSlug(branch))
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		if isGitWorktree(target) {
			return target, nil
		}
		os.RemoveAll(target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create worktree parent dir: %w", err)
	}
	cmd := exec.Command("git", "worktree", "add", target, branch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add failed: %w: %s", err, string(out))
	}
	return target, nil
}

func RemoveWorktree(path string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	return cmd.Run()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/opencodemsb/worktree.go internal/opencodemsb/worktree_test.go
git commit -m "feat: add git worktree operations"
```

---

### Task 7: doctor.go — preflight checks

**Files:**
- Create: `internal/opencodemsb/doctor.go`
- Create: `internal/opencodemsb/doctor_test.go`

**Interfaces:**
- Produces: `CheckAll(ctx context.Context) bool`, `CheckMsb(ctx context.Context) bool`, `CheckDocker() bool`, `CheckKvm() bool`, `CheckGit() bool`
- Consumes: `errorMsg(msg string)` from Task 5, `microsandbox.EnsureInstalled(ctx)` from SDK (NOT `IsInstalled()`)

- [ ] **Step 1: Write the failing test**

Create `internal/opencodemsb/doctor_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestCheckGit -v`
Expected: FAIL — `CheckGit` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/opencodemsb/doctor.go`:

```go
package opencodemsb

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

func CheckMsb(ctx context.Context) bool {
	if err := m.EnsureInstalled(ctx); err != nil {
		errorMsg("msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox")
		return false
	}
	return true
}

func CheckDocker() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		errorMsg("docker not found. Install Docker or Podman with docker-compatible CLI.")
		return false
	}
	return true
}

func CheckKvm() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		errorMsg("/dev/kvm not found. Load kvm module and ensure user is in the kvm group.")
		return false
	}
	return true
}

func CheckGit() bool {
	if _, err := exec.LookPath("git"); err != nil {
		errorMsg("git not found. Install git via your system package manager.")
		return false
	}
	return true
}

func CheckAll(ctx context.Context) bool {
	return CheckMsb(ctx) && CheckDocker() && CheckKvm() && CheckGit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/opencodemsb/doctor.go internal/opencodemsb/doctor_test.go
git commit -m "feat: add preflight doctor checks"
```

---

### Task 8: image.go — docker build + SDK image cache

**Files:**
- Create: `internal/opencodemsb/image.go`
- Create: `internal/opencodemsb/image_test.go`

**Interfaces:**
- Produces: `BaseTag` (const), `ReferencesBase(dockerfile []byte) bool`, `EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error)`, `ImageTag(digest string) string`
- Consumes: `EmbeddedDockerfile` from Task 1, moby Docker client, microsandbox SDK Image cache

- [ ] **Step 1: Add docker client dependency**

Run:
```bash
go get github.com/docker/docker/client
go get github.com/docker/docker/api/types/image
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `internal/opencodemsb/image_test.go`:

```go
package opencodemsb

import (
	"context"
	"testing"
)

func TestReferencesBaseDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner:base\nRUN echo hi\n")
	if !ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=true for Dockerfile with base FROM")
	}
}

func TestReferencesBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for non-base Dockerfile")
	}
}

func TestReferencesBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner:base\nFROM debian:trixie-slim\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for commented FROM")
	}
}

func TestImageTag(t *testing.T) {
	got := ImageTag("sha256:abc123def456")
	expected := "opencode-msb/runner:sha256-abc123def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestEnsureImageReturnsErrorWithoutDocker(t *testing.T) {
	_, _, err := EnsureImage(context.Background(), EmbeddedDockerfile, true)
	if err == nil {
		t.Error("expected error when Docker is not available")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestReferencesBase -v`
Expected: FAIL — `ReferencesBase` undefined.

- [ ] **Step 4: Write minimal implementation**

Create `internal/opencodemsb/image.go`:

```go
package opencodemsb

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	m "github.com/superradcompany/microsandbox/sdk/go"
)

const BaseTag = "opencode-msb/runner:base"

func ReferencesBase(dockerfile []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, BaseTag) {
			return true
		}
	}
	return false
}

func ImageTag(digest string) string {
	short := strings.TrimPrefix(digest, "sha256:")
	if len(short) > 12 {
		short = short[:12]
	}
	return "opencode-msb/runner:sha256-" + short
}

func EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", "", fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	buildTag := "opencode-msb/runner:latest"
	buildResp, err := cli.ImageBuild(ctx, bytes.NewReader(dockerfile), image.BuildOptions{
		Tags:   []string{buildTag},
		Remove: true,
	})
	if err != nil {
		return "", "", fmt.Errorf("docker build: %w", err)
	}
	defer buildResp.Body.Close()
	io.Copy(io.Discard, buildResp.Body)

	inspect, _, err := cli.ImageInspectWithRaw(ctx, buildTag)
	if err != nil {
		return "", "", fmt.Errorf("docker inspect: %w", err)
	}
	imageDigest = inspect.ID
	imageRef = ImageTag(imageDigest)

	_, cacheErr := m.Image.Get(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, nil
	}

	tmpFile, err := os.CreateTemp("", "opencode-msb-image-*.tar")
	if err != nil {
		return "", "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	rc, err := cli.ImageSave(ctx, []string{buildTag})
	if err != nil {
		return "", "", fmt.Errorf("docker save: %w", err)
	}
	out, err := os.Create(tmpPath)
	if err != nil {
		rc.Close()
		return "", "", fmt.Errorf("open temp file for write: %w", err)
	}
	_, err = io.Copy(out, rc)
	rc.Close()
	out.Close()
	if err != nil {
		return "", "", fmt.Errorf("save image to temp file: %w", err)
	}

	if _, err := m.Image.Load(ctx, tmpPath, imageRef); err != nil {
		return "", "", fmt.Errorf("msb image load: %w", err)
	}

	return imageRef, imageDigest, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS for `TestReferencesBase`, `TestImageTag`, `TestImageTag`. `TestEnsureImageReturnsErrorWithoutDocker` passes because Docker isn't available.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/opencodemsb/image.go internal/opencodemsb/image_test.go
git commit -m "feat: add docker build + SDK image cache with content-digest idempotency"
```

---

### Task 9: volumes.go — SDK volumes + fallback

**Files:**
- Create: `internal/opencodemsb/volumes.go`
- Create: `internal/opencodemsb/volumes_test.go`

**Interfaces:**
- Produces: `HomeVolumeName(projectSlug, imageDigest string) string`, `VolumeManager` struct, `NewVolumeManager(fallback bool, stateDir string) *VolumeManager`, `(*VolumeManager).EnsureHome(ctx context.Context, projectSlug, imageDigest, imageTag string, reset bool) (volumeRef string, err error)`
- Consumes: microsandbox SDK (CreateVolume, RemoveVolume, CreateSandbox, Mount), `warn(msg string)` from Task 5

- [ ] **Step 1: Write the failing test**

Create `internal/opencodemsb/volumes_test.go`:

```go
package opencodemsb

import (
	"path/filepath"
	"testing"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("p-abc123", "sha256-def456")
	expected := "p-abc123-opencode-home-sha256-def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestFallbackHomePath(t *testing.T) {
	vm := NewVolumeManager(true, "/tmp/state")
	got := vm.fallbackHomePath("p-abc", "sha256-def")
	expected := filepath.Join("/tmp/state", "state", "p-abc", "home", "sha256-def")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestNewVolumeManager(t *testing.T) {
	vm := NewVolumeManager(true, "/tmp/state")
	if !vm.fallback {
		t.Error("expected fallback=true")
	}
	if vm.stateDir != "/tmp/state" {
		t.Errorf("expected stateDir=/tmp/state, got %q", vm.stateDir)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestHomeVolumeName -v`
Expected: FAIL — `HomeVolumeName` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/opencodemsb/volumes.go`:

```go
package opencodemsb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

func HomeVolumeName(projectSlug, imageDigest string) string {
	return projectSlug + "-opencode-home-" + imageDigest
}

type VolumeManager struct {
	fallback bool
	stateDir string
}

func NewVolumeManager(fallback bool, stateDir string) *VolumeManager {
	return &VolumeManager{fallback: fallback, stateDir: stateDir}
}

func (vm *VolumeManager) fallbackHomePath(projectSlug, imageDigest string) string {
	return filepath.Join(vm.stateDir, "state", projectSlug, "home", imageDigest)
}

func (vm *VolumeManager) EnsureHome(ctx context.Context, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
	name := HomeVolumeName(projectSlug, imageDigest)

	if vm.fallback {
		return vm.ensureFallbackHome(ctx, name, projectSlug, imageDigest, imageTag, reset)
	}

	if reset {
		_ = m.RemoveVolume(ctx, name)
	}

	_, err := m.GetVolume(ctx, name)
	if err == nil {
		return name, nil
	}

	vol, err := m.CreateVolume(ctx, name,
		m.WithVolumeKind(m.VolumeKindDir),
	)
	if err != nil {
		warn("msb volume creation failed; using host-directory fallback.")
		vm.fallback = true
		return vm.ensureFallbackHome(ctx, name, projectSlug, imageDigest, imageTag, reset)
	}

	if err := vm.prefillVolume(ctx, name, imageTag); err != nil {
		return "", fmt.Errorf("prefill volume %s: %w", name, err)
	}

	return name, nil
}

func (vm *VolumeManager) ensureFallbackHome(ctx context.Context, name, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
	path := vm.fallbackHomePath(projectSlug, imageDigest)
	if reset {
		os.RemoveAll(path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create fallback home dir: %w", err)
	}
	entries, _ := os.ReadDir(path)
	if len(entries) == 0 {
		if err := vm.prefillFallback(ctx, path, imageTag); err != nil {
			return "", fmt.Errorf("prefill fallback home: %w", err)
		}
	}
	return path, nil
}

func (vm *VolumeManager) prefillVolume(ctx context.Context, volumeName, imageTag string) error {
	return vm.prefill(ctx, volumeName, imageTag, false)
}

func (vm *VolumeManager) prefillFallback(ctx context.Context, hostPath, imageTag string) error {
	return vm.prefill(ctx, hostPath, imageTag, true)
}

func (vm *VolumeManager) prefill(ctx context.Context, ref, imageTag string, isBind bool) error {
	prefillName := fmt.Sprintf("opencode-msb-prefill-%d", time.Now().UnixNano())

	var mountConfig m.MountConfig
	if isBind {
		mountConfig = m.Mount.Bind(ref, m.MountOptions{})
	} else {
		mountConfig = m.Mount.Named(ref, m.MountOptions{})
	}

	sb, err := m.CreateSandbox(ctx, prefillName,
		m.WithImage(imageTag),
		m.WithMounts(map[string]m.MountConfig{
			"/mnt/home": mountConfig,
		}),
		m.WithReplace(),
	)
	if err != nil {
		return fmt.Errorf("create prefill sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = m.RemoveSandbox(context.Background(), prefillName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c", "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home"})
	if err != nil {
		return fmt.Errorf("prefill cp: %w", err)
	}
	if !out.Success() {
		return fmt.Errorf("prefill cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/opencodemsb/volumes.go internal/opencodemsb/volumes_test.go
git commit -m "feat: add SDK volume management with host-dir fallback"
```

---

### Task 10: cmd.go — cobra commands + timing

**Files:**
- Modify: `internal/opencodemsb/cmd.go` (replace stub from Task 1)
- Create: `internal/opencodemsb/cmd_test.go`

**Interfaces:**
- Produces: `Execute() error`, `RunOptions` struct, `newTiming(enabled bool) (tick func(string), summary func())`, `setLogOutput(w io.Writer)` (for testing)
- Consumes: All prior tasks, `github.com/spf13/cobra`

- [ ] **Step 1: Add cobra dependency**

Run:
```bash
go get github.com/spf13/cobra
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `internal/opencodemsb/cmd_test.go`:

```go
package opencodemsb

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNewTimingDisabled(t *testing.T) {
	var buf bytes.Buffer
	setLogOutput(&buf)
	tick, summary := newTiming(false)
	tick("phase1")
	tick("phase2")
	summary()
	if buf.Len() > 0 {
		t.Errorf("expected no output when timing disabled, got %q", buf.String())
	}
}

func TestNewTimingEnabled(t *testing.T) {
	var buf bytes.Buffer
	setLogOutput(&buf)
	tick, summary := newTiming(true)
	tick("phase1")
	time.Sleep(1 * time.Millisecond)
	tick("phase2")
	summary()
	out := buf.String()
	if !strings.Contains(out, "[timing] phase1") {
		t.Errorf("expected timing output for phase1, got %q", out)
	}
	if !strings.Contains(out, "[timing] phase2") {
		t.Errorf("expected timing output for phase2, got %q", out)
	}
	if !strings.Contains(out, "[timing] total launcher overhead") {
		t.Errorf("expected total timing output, got %q", out)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestNewTiming -v`
Expected: FAIL — `newTiming` undefined.

- [ ] **Step 4: Write minimal implementation**

Replace `internal/opencodemsb/cmd.go`:

```go
package opencodemsb

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var version = "dev"

var (
	stateDir      string
	userConfigDir string
)

func init() {
	home, _ := os.UserHomeDir()
	stateDir = filepath.Join(home, ".local", "share", "opencode-msb")
	userConfigDir = filepath.Join(home, ".config", "inoio-sandbox", "opencode")
}

func setLogOutput(w io.Writer) {
	logMu.Lock()
	defer logMu.Unlock()
	logOut = newLogger(w, false)
}

func newTiming(enabled bool) (func(string), func()) {
	start := time.Now()
	var phases []struct {
		label   string
		elapsed time.Duration
	}

	tick := func(label string) {
		now := time.Now()
		elapsed := now.Sub(start)
		start = now
		phases = append(phases, struct {
			label   string
			elapsed time.Duration
		}{label, elapsed})
		if enabled {
			fmt.Fprintf(os.Stderr, "[timing] %s: %.3fs\n", label, elapsed.Seconds())
		}
	}

	summary := func() {
		if !enabled {
			return
		}
		var total time.Duration
		for _, p := range phases {
			total += p.elapsed
		}
		fmt.Fprintf(os.Stderr, "[timing] total launcher overhead: %.3fs\n", total.Seconds())
	}

	return tick, summary
}

type RunOptions struct {
	Worktree       string
	ImageRebuild   bool
	VolumeFallback bool
	ResetHome      bool
	CPUs           uint8
	Memory         string
	Timing         bool
	Args           []string
}

func Execute() error {
	root := &cobra.Command{
		Use:     "opencode-msb",
		Short:   "Run opencode inside an ephemeral microsandbox VM",
		Version: version,
	}

	root.AddCommand(buildDoctorCmd())
	root.AddCommand(buildRunCmd())

	args := os.Args[1:]
	if len(args) == 0 || (args[0] != "doctor" && args[0] != "run" && args[0] != "help" && args[0] != "--help" && args[0] != "-h" && args[0] != "--version" && args[0] != "-v" && !strings.HasPrefix(args[0], "-")) {
		opts := parseRunFlags(args)
		return runCommand(opts)
	}

	return root.Execute()
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !CheckAll(cmd.Context()) {
				return fmt.Errorf("preflight failed")
			}
			fmt.Fprintln(os.Stderr, "doctor: all checks passed")
			return nil
		},
	}
}

func buildRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] [ARGS...]",
		Short: "Run opencode in a microsandbox VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := RunOptions{Args: args}
			opts.Worktree, _ = cmd.Flags().GetString("worktree")
			opts.ImageRebuild, _ = cmd.Flags().GetBool("image-rebuild")
			opts.VolumeFallback, _ = cmd.Flags().GetBool("volume-fallback")
			opts.ResetHome, _ = cmd.Flags().GetBool("reset-home")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.Timing, _ = cmd.Flags().GetBool("timing")
			return runCommand(opts)
		},
	}

	cmd.Flags().String("worktree", "", "Worktree name")
	cmd.Flags().Bool("image-rebuild", false, "Force image rebuild")
	cmd.Flags().Bool("volume-fallback", false, "Use host directories instead of msb volumes")
	cmd.Flags().Bool("reset-home", false, "Recreate the project home volume")
	cmd.Flags().Uint8("cpus", 0, "Number of CPUs (default: all)")
	cmd.Flags().String("memory", "4G", "Memory limit (default: 4G)")
	cmd.Flags().Bool("timing", false, "Print per-phase launcher timing to stderr")

	return cmd
}

func parseRunFlags(args []string) RunOptions {
	opts := RunOptions{Memory: "4G"}
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--worktree" || arg == "-w":
			if i+1 < len(args) {
				opts.Worktree = args[i+1]
				i += 2
				continue
			}
		case arg == "--image-rebuild":
			opts.ImageRebuild = true
		case arg == "--volume-fallback":
			opts.VolumeFallback = true
		case arg == "--reset-home":
			opts.ResetHome = true
		case arg == "--timing":
			opts.Timing = true
		case arg == "--cpus" && i+1 < len(args):
			var cpus uint8
			fmt.Sscanf(args[i+1], "%d", &cpus)
			opts.CPUs = cpus
			i += 2
			continue
		case arg == "--memory" && i+1 < len(args):
			opts.Memory = args[i+1]
			i += 2
			continue
		case !strings.HasPrefix(arg, "-"):
			opts.Args = append(opts.Args, arg)
		}
		i++
	}
	return opts
}
```

- [ ] **Step 5: Add a stub runCommand for now**

Add to the bottom of `cmd.go` (will be replaced in Task 11):

```go
func runCommand(opts RunOptions) error {
	return fmt.Errorf("run command not yet implemented")
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS for all cmd tests.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/opencodemsb/cmd.go internal/opencodemsb/cmd_test.go
git commit -m "feat: add cobra CLI structure with doctor and run subcommands"
```

---

### Task 11: runner.go — main orchestration flow

**Files:**
- Create: `internal/opencodemsb/runner.go`
- Create: `internal/opencodemsb/runner_test.go`
- Modify: `internal/opencodemsb/cmd.go` (replace `runCommand` stub)

**Interfaces:**
- Produces: `runCommand(opts RunOptions) error`, `parseMemory(spec string) uint32`, `sandboxName(projectSlug, branchSlug string) string`, `buildEnvMap(envExtra []string) map[string]string`, `readSandboxEnv() []string`, `resolveDockerfile() []byte`
- Consumes: All prior tasks, `microsandbox.EnsureInstalled(ctx)` before `CreateSandbox`, `microsandbox.CreateSandbox` with options, `sb.FS()`, `sb.Attach`

- [ ] **Step 1: Write the failing test**

Create `internal/opencodemsb/runner_test.go`:

```go
package opencodemsb

import (
	"testing"
)

func TestParseMemoryGigabytes(t *testing.T) {
	got := parseMemory("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryMegabytes(t *testing.T) {
	got := parseMemory("512M")
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryPlainNumber(t *testing.T) {
	got := parseMemory("2048")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryLowercase(t *testing.T) {
	got := parseMemory("2g")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestSandboxNameTruncation(t *testing.T) {
	got := sandboxName("p-abcdef", "feat-very-long-branch-name-that-exceeds-the-limit-and-more")
	if len(got) > 128 {
		t.Errorf("expected name <= 128 bytes, got %d", len(got))
	}
}

func TestBuildEnvMap(t *testing.T) {
	envExtra := []string{"FOO=bar", "BAZ=qux"}
	got := buildEnvMap(envExtra)
	if got["HOME"] != "/home/dev" {
		t.Errorf("expected HOME=/home/dev, got %q", got["HOME"])
	}
	if got["NODE_ENV"] != "development" {
		t.Errorf("expected NODE_ENV=development, got %q", got["NODE_ENV"])
	}
	if got["SANDBOX_USER"] != "dev" {
		t.Errorf("expected SANDBOX_USER=dev, got %q", got["SANDBOX_USER"])
	}
	if got["SHELL"] != "/bin/bash" {
		t.Errorf("expected SHELL=/bin/bash, got %q", got["SHELL"])
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}
}

func TestReadSandboxEnvMissing(t *testing.T) {
	env := readSandboxEnv()
	if len(env) != 0 {
		t.Errorf("expected 0 env vars when .sandbox/env missing, got %d", len(env))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/opencodemsb/ -run TestParseMemory -v`
Expected: FAIL — `parseMemory` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/opencodemsb/runner.go`:

```go
package opencodemsb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

var vmEnv = []string{
	"HOME=/home/dev",
	"NODE_ENV=development",
	"SANDBOX_USER=dev",
	"SHELL=/bin/bash",
}

func parseMemory(spec string) uint32 {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 4096
	}
	multiplier := uint32(1)
	last := spec[len(spec)-1]
	rest := spec
	switch last {
	case 'g', 'G':
		multiplier = 1024
		rest = spec[:len(spec)-1]
	case 'm', 'M':
		multiplier = 1
		rest = spec[:len(spec)-1]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 4096
	}
	return uint32(n) * multiplier
}

func sandboxName(projectSlug, branchSlug string) string {
	name := "opencode-msb-" + projectSlug + "-" + branchSlug
	if len(name) > 128 {
		name = name[:128]
	}
	return name
}

func buildEnvMap(envExtra []string) map[string]string {
	env := make(map[string]string)
	for _, e := range vmEnv {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	for _, e := range envExtra {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

func readSandboxEnv() []string {
	data, err := os.ReadFile(".sandbox/env")
	if err != nil {
		return nil
	}
	var env []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			env = append(env, line)
		}
	}
	return env
}

func resolveDockerfile() []byte {
	if data, err := os.ReadFile(".sandbox/Dockerfile"); err == nil {
		return data
	}
	return EmbeddedDockerfile
}

func envrcFiles(worktreePath string) []string {
	entries, err := os.ReadDir(worktreePath)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".envrc") {
			files = append(files, entry.Name())
		}
	}
	return files
}

func runCommand(opts RunOptions) error {
	ctx := context.Background()
	tick, summary := newTiming(opts.Timing)
	defer summary()

	if !CheckAll(ctx) {
		return fmt.Errorf("preflight failed")
	}
	tick("preflight")

	projectSlug := ProjectSlug()
	branch := opts.Worktree
	if branch == "" {
		var err error
		branch, err = BranchName(".")
		if err != nil {
			return fmt.Errorf("unable to determine git branch: %w", err)
		}
	}
	tick("project/branch resolution")

	cwd, _ := os.Getwd()
	wtPath, err := CurrentWorktreePath(cwd)
	if err != nil || wtPath == "" {
		wtPath, err = EnsureWorktree(cwd, stateDir, projectSlug, branch)
		if err != nil {
			return fmt.Errorf("worktree setup failed: %w", err)
		}
	}
	tick("worktree resolution")

	dockerfile := resolveDockerfile()
	imageRef, imageDigest, err := EnsureImage(ctx, dockerfile, opts.ImageRebuild)
	if err != nil {
		return fmt.Errorf("image setup failed: %w", err)
	}
	tick("image hash/check/build")

	vm := NewVolumeManager(opts.VolumeFallback, stateDir)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef, opts.ResetHome)
	if err != nil {
		return fmt.Errorf("volume setup failed: %w", err)
	}
	tick("volume ensure")

	providerCfg, err := LoadProviderConfig(EmbeddedProviderConfig)
	if err != nil {
		return fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, err := os.Stat(".sandbox/opencode"); err == nil {
		projectConfigDir = ".sandbox/opencode"
	}
	configFiles, err := BuildMergedConfig(userConfigDir, projectConfigDir, providerCfg)
	if err != nil {
		return fmt.Errorf("merge config: %w", err)
	}
	secrets := BuildSecrets()
	cpus := opts.CPUs
	if cpus == 0 {
		cpus = NumCPUs()
	}
	maxMemoryGiB := TotalMemoryGiB()
	name := sandboxName(projectSlug, BranchSlug(branch))
	tick("config/secrets")

	envExtra := readSandboxEnv()
	envMap := buildEnvMap(envExtra)

	mounts := map[string]m.MountConfig{
		"/home/dev":           m.Mount.Named(homeVol, m.MountOptions{}),
		"/home/dev/workspace": m.Mount.Bind(wtPath, m.MountOptions{}),
	}

	if err := m.EnsureInstalled(ctx); err != nil {
		return fmt.Errorf("microsandbox runtime: %w", err)
	}

	sb, err := m.CreateSandbox(ctx, name,
		m.WithImage(imageRef),
		m.WithMounts(mounts),
		m.WithSecrets(secrets...),
		m.WithEnv(envMap),
		m.WithUser("dev"),
		m.WithWorkdir("/home/dev/workspace"),
		m.WithCPUs(cpus),
		m.WithMaxCPUs(NumCPUs()),
		m.WithMemory(parseMemory(opts.Memory)),
		m.WithMaxMemory(uint32(maxMemoryGiB)*1024),
		m.WithReplace(),
	)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = m.RemoveSandbox(context.Background(), name)
	}()

	fs := sb.FS()
	fs.Mkdir(ctx, "/home/dev/.config/opencode")
	for fname, data := range configFiles {
		fs.Write(ctx, "/home/dev/.config/opencode/"+fname, data)
	}
	for _, envrc := range envrcFiles(wtPath) {
		fs.Remove(ctx, "/home/dev/workspace/"+envrc)
	}
	tick("config setup")

	setup := `eval "$(goenv init -)" && exec opencode ` + strings.Join(opts.Args, " ")
	exitCode, err := sb.Attach(ctx, "/bin/bash", "-lc", setup)
	tick("opencode session")

	if err != nil {
		return fmt.Errorf("opencode session failed: %w", err)
	}
	os.Exit(exitCode)
	return nil
}
```

- [ ] **Step 4: Remove the stub runCommand from cmd.go**

Edit `internal/opencodemsb/cmd.go` and remove the stub:
```go
func runCommand(opts RunOptions) error {
	return fmt.Errorf("run command not yet implemented")
}
```
(The real `runCommand` now lives in `runner.go`.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/opencodemsb/ -v`
Expected: PASS for all runner tests.

- [ ] **Step 6: Verify it builds**

Run: `go build ./cmd/opencode-msb`
Expected: builds successfully.

- [ ] **Step 7: Commit**

```bash
git add internal/opencodemsb/runner.go internal/opencodemsb/runner_test.go internal/opencodemsb/cmd.go
git commit -m "feat: add main orchestration flow with SDK sandbox creation"
```

---

### Task 12: Final verification + lint

**Files:**
- None (verification only)

- [ ] **Step 1: Run all tests**

Run: `go test ./... -v`
Expected: All tests pass.

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: No issues.

- [ ] **Step 3: Check formatting**

Run: `gofmt -l .`
Expected: No files listed (all formatted). If files are listed, run `gofmt -w .` and commit.

- [ ] **Step 4: Run golangci-lint**

Run: `golangci-lint run`
Expected: No issues. Fix any reported issues.

- [ ] **Step 5: Verify the binary runs**

Run: `go run ./cmd/opencode-msb doctor`
Expected: Either "doctor: all checks passed" (if msb/docker/git installed) or error messages about missing prerequisites.

- [ ] **Step 6: Verify --version**

Run: `go run ./cmd/opencode-msb --version`
Expected: Prints version `dev`.

- [ ] **Step 7: Final commit (if formatting/lint changes)**

```bash
git add -A
git commit -m "chore: format and lint fixes"
```

---
