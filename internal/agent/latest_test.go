package agent

import (
	"context"
	"testing"
)

func TestLatestVersionFromJSONBuildRequestError(t *testing.T) {
	if _, err := latestVersionFromJSON(context.Background(), "://bad", "test"); err == nil {
		t.Fatal("expected error for malformed URL")
	}
}
