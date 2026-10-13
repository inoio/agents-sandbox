//go:build integration

package image

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// benchMobyBackend measures the *legacy* host-Docker path: the Docker Engine
// API's ImageBuild (moby client), which uses the daemon's classic builder and
// its layer cache. This is how agents-sandbox built the runner image before the
// BuildKit builder VM (commit f2048fe), so it is the pre-BuildKit baseline —
// distinct from the "host" backend, which drives the docker CLI (BuildKit) and
// from "docker", which runs a buildkitd container.
func benchMobyBackend(t *testing.T, ctx context.Context, workload benchWorkload, runs int) {
	t.Helper()
	contextDir := workload.contextDir(t)
	t.Cleanup(func() {
		_, _ = exec.Command("docker", "image", "rm", "-f", workload.tag).CombinedOutput()
	})
	result := benchResult{workload: workload.name, backend: "moby"}
	result.cold = mustMobyBuild(t, ctx, workload, contextDir, true, "cold")
	for i := range runs {
		result.warm = append(result.warm, mustMobyBuild(
			t, ctx, workload, contextDir, false, fmt.Sprintf("warm-%d", i+1)))
	}
	for i := range runs {
		result.rebuild = append(result.rebuild, mustMobyBuild(
			t, ctx, workload, contextDir, true, fmt.Sprintf("rebuild-%d", i+1)))
	}
	reportBench(t, result)
}

// mustMobyBuild runs one timed ImageBuild through the moby client and drains the
// streamed build output before stopping the clock (ImageBuild returns before the
// build completes). It removes the produced image afterwards.
func mustMobyBuild(
	t *testing.T,
	ctx context.Context,
	workload benchWorkload,
	contextDir string,
	noCache bool,
	phase string,
) time.Duration {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("moby client [%s]: %v", phase, err)
	}
	buildContext, err := tarContext(contextDir)
	if err != nil {
		t.Fatalf("tar context [%s]: %v", phase, err)
	}

	start := time.Now()
	res, err := cli.ImageBuild(ctx, bytes.NewReader(buildContext), client.ImageBuildOptions{
		Tags:      []string{workload.tag},
		Remove:    true,
		NoCache:   noCache,
		BuildArgs: mobyBuildArgs(workload),
	})
	if err != nil {
		t.Fatalf("moby ImageBuild [%s]: %v", phase, err)
	}
	scanErr := drainMobyBuild(res.Body)
	_ = res.Body.Close()
	duration := time.Since(start)
	if scanErr != nil {
		t.Fatalf("moby build [%s]: %v", phase, scanErr)
	}
	logBench(t, workload.name, "moby", phase, duration)
	return duration
}

// mobyBuildArgs mirrors hostBuildArgs: the workload's own args plus BASE_IMAGE,
// in the *string map the engine API expects.
func mobyBuildArgs(workload benchWorkload) map[string]*string {
	args := map[string]*string{}
	for key, value := range workload.buildArgs {
		v := value
		args[key] = &v
	}
	base := workload.baseRef
	args["BASE_IMAGE"] = &base
	return args
}

// tarContext tars every regular file directly under dir into a Docker build
// context (flat, as the old writeDockerfileTar produced).
func tarContext(dir string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: filepath.Base(path),
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			return err
		}
		_, err = tw.Write(content)
		return err
	})
	if err != nil {
		_ = tw.Close()
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drainMobyBuild consumes the engine /build JSON stream and returns the first
// build error it reports.
func drainMobyBuild(r io.Reader) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("decode build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("%s", msg.Error)
		}
	}
}
