package firstrun

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// HostFiles resolves the host config files the seed would read for a: it
// enumerates the agent's provision rules against hostHome, keeps every selected
// file the seed would copy, and returns the host-side path of each one, sorted,
// together with a stable hash of their home-relative paths and contents. An
// agent that declares no host setup (or no Provisioner capability), or a host
// whose selected files are all unseedable, yields no files and an empty hash, so
// the caller omits "learn from my host setup".
//
// The set matches SeedFromHost's own exclusions, so the offer names only files
// the seed would actually read: a value store (the dotenv, or a login credential
// store such as opencode's auth.json, so an OAuth refresh does not re-arm the
// decision), a file no config format's extension matches, and a name the agent's
// merge never reads are all left out. Each included file is read through
// readback.ReadCappedFile, so an over-cap file is skipped rather than loaded.
//
// The hash covers the home-relative path, not the absolute host path, so it is
// stable across a moved home and is what a recorded decision compares against. A
// walk error yields no files and an empty hash: a host config the probe cannot
// read offers no seed rather than a partial one.
func HostFiles(a agent.Agent, hostHome string) ([]string, string) {
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		return nil, ""
	}
	agentName := a.Name()

	type hostFile struct {
		rel  string
		path string
		data []byte
	}
	var selected []hostFile
	for _, rule := range provisioner.ProvisionRules() {
		if rule.Dir == "" {
			continue
		}
		for _, hostPath := range selectHostPaths(rule, hostHome) {
			if !seedableHostFile(agentName, hostHome, hostPath) {
				continue
			}
			content, ok := readback.ReadCappedFile(hostPath, readback.MaxHashedBytes)
			if !ok {
				continue
			}
			selected = append(selected, hostFile{
				rel:  hostRelativePath(hostHome, hostPath),
				path: hostPath,
				data: content,
			})
		}
	}
	if len(selected) == 0 {
		return nil, ""
	}

	sort.Slice(selected, func(i, j int) bool { return selected[i].rel < selected[j].rel })
	files := make([]string, len(selected))
	digest := sha256.New()
	for i, file := range selected {
		files[i] = file.path
		writeHashField(digest, []byte(file.rel))
		writeHashField(digest, file.data)
	}
	return files, hex.EncodeToString(digest.Sum(nil))
}

// selectHostPaths returns the host paths under hostHome the provision rule
// selects, in lexical order and without reading file contents. It mirrors
// filewalk's rule walk (prune an unselected directory, skip an unselected file)
// so the enumeration matches what agent.EvalProvisionRules would copy.
func selectHostPaths(rule agent.ProvisionRule, hostHome string) []string {
	srcRoot := filepath.Join(hostHome, rule.Dir)
	var paths []string
	_ = filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort: skip entries the walk cannot read
		}
		// WalkDir hands back every path built from srcRoot, so the two share
		// absoluteness and Rel cannot fail.
		rel, _ := filepath.Rel(srcRoot, path)
		if rel == "." {
			return nil
		}
		if !agent.SelectProvisionRule(rule, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths
}

// seedableHostFile reports whether the seed (SeedFromHost) would read hostPath:
// it is not a value store, its extension is a config format, and the agent's
// merge reads its snippet name. SeedFromHost performs each of these skips, so
// HostFiles must exclude the same files or the offer would name a no-op seed.
func seedableHostFile(agentName, hostHome, hostPath string) bool {
	if readback.SkipsInlineCredentials(hostPath) {
		return false
	}
	if _, known := configdecode.FormatFor(hostRelativePath(hostHome, hostPath)); !known {
		return false
	}
	return agent.MergesSnippetName(agentName, filepath.Base(hostPath))
}

// writeHashField feeds a length-prefixed field into h, so two different file
// sets cannot hash to the same digest by concatenation (e.g. paths ["ab", "c"]
// and ["a", "bc"]).
func writeHashField(h hash.Hash, field []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(field)
}
