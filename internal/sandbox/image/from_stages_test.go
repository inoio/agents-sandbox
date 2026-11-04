package image

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestScanFromStagesLowercaseInstructions(t *testing.T) {
	lastImage, stageBase := scanFromStages([]byte(
		"from golang:1.24 as build\nrun go build\nFrom debian:trixie-slim AS final\n",
	))
	if lastImage != "debian:trixie-slim" {
		t.Errorf("lastImage = %q, want debian:trixie-slim", lastImage)
	}
	if got := stageBase["build"]; got != "golang:1.24" {
		t.Errorf("stageBase[build] = %q, want golang:1.24", got)
	}
	if got := stageBase["final"]; got != "debian:trixie-slim" {
		t.Errorf("stageBase[final] = %q, want debian:trixie-slim", got)
	}
}

// TestResolveStageBaseFollowsAliasesAndStopsOnCycles pins the token the walk
// returns, not just that it terminates: a token that names no stage is returned
// as is, a self-alias stops at "0", and a two-stage cycle entered at "b" stops
// at "b".
func TestResolveStageBaseFollowsAliasesAndStopsOnCycles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		token     string
		stageBase map[string]string
		want      string
	}{
		{"unresolved token", "debian", map[string]string{"builder": "base"}, "debian"},
		{"self alias", "0", map[string]string{"0": "0"}, "0"},
		{"two-stage cycle", "b", map[string]string{"a": "b", "b": "a"}, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveStageBase(tc.token, tc.stageBase); got != tc.want {
				t.Errorf("resolveStageBase(%q, %v) = %q, want %q", tc.token, tc.stageBase, got, tc.want)
			}
		})
	}
}

func TestParseFromLowercaseKeywordAndAlias(t *testing.T) {
	image, alias := parseFrom("from --platform=linux/amd64 debian:trixie-slim as build")
	if image != "debian:trixie-slim" {
		t.Errorf("image = %q, want debian:trixie-slim", image)
	}
	if alias != "build" {
		t.Errorf("alias = %q, want build", alias)
	}
}

func TestParseFromIgnoresNonFromInstruction(t *testing.T) {
	if image, alias := parseFrom("RUN echo hi"); image != "" || alias != "" {
		t.Errorf("parseFrom(RUN ...) = (%q, %q), want empty", image, alias)
	}
}

// TestParseFromTrimsCRLF pins the trailing-whitespace trim of a CRLF Dockerfile
// line. Without it the alias keeps a trailing CR, which no longer matches the
// alias the line-buffered scanner sees, so a custom CRLF base fails the build.
func TestParseFromTrimsCRLF(t *testing.T) {
	image, alias := parseFrom("FROM ubuntu:24.04 AS base\r")
	if image != "ubuntu:24.04" || alias != "base" {
		t.Errorf("parseFrom(CRLF line) = (%q, %q), want (ubuntu:24.04, base)", image, alias)
	}
}

func TestParseFromIgnoresFromage(t *testing.T) {
	if image, _ := parseFrom("FROMAGE debian:trixie-slim"); image != "" {
		t.Errorf("parseFrom(FROMAGE ...) image = %q, want empty", image)
	}
}

// TestBaseImageRefComposition pins that baseImageRef resolves the final stage's
// FROM through any stage aliases and returns it unchanged: the tool-owned base
// (qualified where it is rendered) and a user's own base alike. Re-qualifying a
// user's base would redirect the lookup to Docker Hub even when the engine
// resolves that name through a mirror, a search registry, or a rewrite. The
// label's familiar spelling is applied by resolveBaseDigest, covered separately.
// A token that is no valid reference is returned as is.
func TestBaseImageRefResolvesTheFinalStageUnchanged(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if got := baseImageRef(renderBytes(t, a, nil, dockerOff)); got != distroBaseRef {
		t.Errorf("managed baseImageRef = %q, want %q", got, distroBaseRef)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, custom, dockerOff)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q, want ubuntu:24.04", got)
	}
	aliased := []byte("FROM fedora:latest AS base\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, aliased, dockerOff)); got != "fedora:latest" {
		t.Errorf("aliased baseImageRef = %q, want fedora:latest", got)
	}
	private := []byte("FROM company/base:latest\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, private, dockerOff)); got != "company/base:latest" {
		t.Errorf("private baseImageRef = %q, want the token the engine resolves, company/base:latest", got)
	}
	if got := baseImageRef([]byte("FROM INVALIDREF\n")); got != "INVALIDREF" {
		t.Errorf("baseImageRef = %q, want the raw unparseable token", got)
	}
}

func TestFinalStageTokenResolvesTheFinalStageImage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dockerfile string
		want       string
	}{
		{"through stage aliases", "FROM golang:1.24 AS build\nFROM build AS final\nRUN echo hi\n", "golang:1.24"},
		{"FROM without an image", "FROM --platform=linux/amd64\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := finalStageToken([]byte(tc.dockerfile)); got != tc.want {
				t.Errorf("finalStageToken = %q, want %q", got, tc.want)
			}
		})
	}
}
