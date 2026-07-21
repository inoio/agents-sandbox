package opencodemsb

import (
	"os"
	"sync"

	"golang.org/x/term"
)

var SecretMap = map[string]string{
	"LITELLM_API_KEY": "litellm.inoio.de",
	"GITHUB_TOKEN":    "github.com",
}

var (
	logMu  sync.Mutex
	logOut = newLogger(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())))
)

func warn(msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	logOut.Warn(msg)
}

func errorMsg(msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	logOut.Error(msg)
}
