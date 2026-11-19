package fakecmd

import (
	"os"
	"strings"
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	Register("faketool", func(args []string) (string, string, int) {
		return "out:" + strings.Join(args, ","), "err\n", 3
	})
	if status := Dispatch(); status >= 0 {
		os.Exit(status)
	}
	mocks.InitFailFastMocks(m)
}
