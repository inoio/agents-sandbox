//go:build ignore

// Spike: determine whether microsandbox's EnsureRuntime replaces an existing
// runtime pair whose version differs from the SDK's pinned version, or reuses
// it as-is. Run: CGO_ENABLED=1 go run ./spike/ensuretest
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

func main() {
	home, err := os.MkdirTemp("", "msb-ensure-home-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(home)

	msbPath := filepath.Join(home, "bin", "msb")
	libPath := filepath.Join(home, "lib", "libkrunfw.so.5.6.1")
	if err := os.MkdirAll(filepath.Dir(msbPath), 0o755); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(filepath.Dir(libPath), 0o755); err != nil {
		panic(err)
	}
	fakeMSB := "#!/bin/sh\necho \"msb 0.7.8\"\n"
	if err := os.WriteFile(msbPath, []byte(fakeMSB), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(libPath, []byte("fake"), 0o644); err != nil {
		panic(err)
	}

	fmt.Printf("SDK version: %s\n", microsandbox.SDKVersion())
	fmt.Printf("home=%s\n", home)
	before, _ := os.ReadFile(msbPath)
	fmt.Printf("before: msb bytes=%q\n", string(before))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resolved, err := microsandbox.EnsureRuntime(ctx, microsandbox.RuntimeConfig{Home: home}, microsandbox.InstallOptions{})
	if err != nil {
		fmt.Printf("EnsureRuntime error: %v\n", err)
	} else {
		fmt.Printf("EnsureRuntime ok: msb=%s libkrunfw=%s origin=%s\n", resolved.MSBPath, resolved.LibkrunfwPath, resolved.Origin)
	}

	after, _ := os.ReadFile(msbPath)
	fmt.Printf("after:  msb bytes=%q\n", string(after))
	if string(before) == string(after) {
		fmt.Println("VERDICT: runtime file unchanged -> EnsureRuntime did NOT replace/downgrade it")
	} else {
		fmt.Println("VERDICT: runtime file changed -> EnsureRuntime replaced/downgraded it")
	}
}
