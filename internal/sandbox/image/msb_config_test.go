package image

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestReadImageInfoFromMSB(t *testing.T) {
	c := &msb.MockMsbClient{ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
		return &msbSdk.ImageConfig{
			Env:    []string{"PATH=/usr/bin", "OPENCODE_DISABLE_AUTOUPDATE=true"},
			Labels: map[string]string{OpenCodeVersionLabel: "1.2.3"},
		}, nil
	}}

	env, version, err := readImageInfoFromMSB(context.Background(), c, "opencode-sandbox/runner-test:abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["PATH"] != "/usr/bin" || env["OPENCODE_DISABLE_AUTOUPDATE"] != "true" {
		t.Errorf("env = %v", env)
	}
	if version != "1.2.3" {
		t.Errorf("version = %q, want %q", version, "1.2.3")
	}
}

func TestReadImageInfoFromMSBNilConfig(t *testing.T) {
	c := &msb.MockMsbClient{ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
		//nolint:nilnil // simulating an absent config in the msb image cache
		return nil, nil
	}}

	env, version, err := readImageInfoFromMSB(context.Background(), c, "ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) != 0 || version != "" {
		t.Errorf("expected empty env/version on nil config, got env=%v version=%q", env, version)
	}
}

func TestReadImageInfoFromMSBIPropagatesInspectError(t *testing.T) {
	c := &msb.MockMsbClient{ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
		return nil, errors.New("inspect failed")
	}}

	if _, _, err := readImageInfoFromMSB(context.Background(), c, "ref"); err == nil {
		t.Fatal("expected error propagation")
	}
}
