package opencodemsb

import (
	"archive/tar"
	"bufio"
	"bytes"
	"strings"
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

func dockerfileTar(dockerfile []byte) *bytes.Buffer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Mode: 0o644,
		Size: int64(len(dockerfile)),
	})
	_, _ = tw.Write(dockerfile)
	_ = tw.Close()
	return &buf
}
