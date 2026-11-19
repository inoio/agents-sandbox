package firstrun

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"os"
	"path/filepath"
	"sort"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// HostFiles resolves the host config files the seed would read for a: it walks
// the agent's provision rules against hostHome and returns the host-side path of
// every selected file, sorted, together with a stable hash of their
// home-relative paths and contents. An agent that declares no host setup (or no
// Provisioner capability) yields no files and an empty hash, so the caller can
// omit "learn from my host setup".
//
// The hash covers the home-relative path, not the absolute host path, so it is
// stable across a moved home and is what a recorded decision compares against.
// A walk error yields no files and an empty hash: a host config the probe cannot
// read offers no seed rather than a partial one.
func HostFiles(a agent.Agent, hostHome string) ([]string, string) {
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		return nil, ""
	}

	type hostFile struct {
		rel  string
		data []byte
	}
	var selected []hostFile
	_, err := agent.EvalProvisionRules(
		provisioner.ProvisionRules(),
		hostHome,
		"",
		func(dstPath string, data []byte, _ os.FileMode) error {
			selected = append(selected, hostFile{rel: dstPath, data: data})
			return nil
		},
	)
	if err != nil || len(selected) == 0 {
		return nil, ""
	}

	sort.Slice(selected, func(i, j int) bool { return selected[i].rel < selected[j].rel })
	files := make([]string, len(selected))
	digest := sha256.New()
	for i, file := range selected {
		files[i] = filepath.Join(hostHome, file.rel)
		writeHashField(digest, []byte(file.rel))
		writeHashField(digest, file.data)
	}
	return files, hex.EncodeToString(digest.Sum(nil))
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
