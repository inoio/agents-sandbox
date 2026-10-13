package builder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// builderRecord persists the builder VM's chosen host port so a later process
// can rediscover a still-running VM: the msb SDK drops port bindings from
// SandboxHandle.Config(), so the port cannot be recovered from the sandbox
// itself.
type builderRecord struct {
	Name     string `json:"name"`
	HostPort int    `json:"hostPort"`
}

func builderRecordPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), "builder-vm.json")
}

func writeBuilderRecord(name string, hostPort int) error {
	dir := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create builder state dir: %w", err)
	}
	data, err := json.Marshal(builderRecord{Name: name, HostPort: hostPort})
	if err != nil {
		return fmt.Errorf("encode builder record: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "builder-vm-*.json")
	if err != nil {
		return fmt.Errorf("create builder record temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write builder record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close builder record temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod builder record: %w", err)
	}
	if err := os.Rename(tmpPath, builderRecordPath()); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename builder record: %w", err)
	}
	return nil
}

func readBuilderRecord() (builderRecord, bool) {
	data, err := os.ReadFile(builderRecordPath())
	if err != nil {
		return builderRecord{}, false
	}
	var record builderRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return builderRecord{}, false
	}
	return record, true
}

func clearBuilderRecord() error {
	if err := os.Remove(builderRecordPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove builder record: %w", err)
	}
	return nil
}
