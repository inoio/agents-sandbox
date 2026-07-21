package opencodemsb

import (
	"os"
	"strconv"
	"strings"
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
