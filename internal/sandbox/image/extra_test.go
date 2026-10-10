package image

import (
	"context"
	"testing"
)

func TestReferencesImageSkipsFromWithoutImage(t *testing.T) {
	if referencesImage([]byte("FROM --platform=linux/amd64\n"), "debian") {
		t.Error("expected referencesImage=false for a FROM line carrying only flags")
	}
}

func TestReferencesImageFalseForUnparseableRef(t *testing.T) {
	if referencesImage([]byte("FROM INVALIDREF\n"), "anything") {
		t.Error("expected referencesImage=false when the FROM reference cannot be parsed")
	}
}

func TestFinalStageTokenSkipsFromWithoutImage(t *testing.T) {
	if got := finalStageToken([]byte("FROM --platform=linux/amd64\n")); got != "" {
		t.Errorf("finalStageToken = %q, want empty", got)
	}
}

func TestFinalStageTokenResolvesAlias(t *testing.T) {
	dockerfile := []byte("FROM golang:1.24 AS build\nFROM build AS final\nRUN echo hi\n")
	if got := finalStageToken(dockerfile); got != "golang:1.24" {
		t.Errorf("finalStageToken = %q, want golang:1.24 resolved through stage aliases", got)
	}
}

func TestBaseImageRefFallsBackToRawToken(t *testing.T) {
	if got := baseImageRef([]byte("FROM INVALIDREF\n")); got != "INVALIDREF" {
		t.Errorf("baseImageRef = %q, want the raw unparseable token", got)
	}
}

func TestSplitFinalStageNoFrom(t *testing.T) {
	earlier, base, body := splitFinalStage([]byte("RUN echo hi\n"))
	if earlier != "" || body != "" {
		t.Errorf(
			"splitFinalStage without a FROM must return the input as the base, got earlier=%q base=%q body=%q",
			earlier,
			base,
			body,
		)
	}
	if base != "RUN echo hi\n" {
		t.Errorf("base = %q, want the input", base)
	}
}

func TestInjectDockerBlockMarkerWithoutTrailingNewline(t *testing.T) {
	got, injected := injectDockerBlock("RUN prereq\n# agents-sandbox:dind", true)
	if !injected {
		t.Fatal("expected injection at the legacy dind marker")
	}
	want := "RUN prereq\n" + dockerMergeBlock()
	if got != want {
		t.Errorf("injectDockerBlock = %q, want %q", got, want)
	}
}

func TestResolveAgentVersionFallsBackToEmpty(t *testing.T) {
	got, err := resolveAgentVersion(context.Background(), plainAgent{}, "")
	if err != nil {
		t.Fatalf("resolveAgentVersion: %v", err)
	}
	if got != "" {
		t.Errorf(
			"resolveAgentVersion = %q, want empty for an agent without an UpgradeChecker and no requested version",
			got,
		)
	}
}
