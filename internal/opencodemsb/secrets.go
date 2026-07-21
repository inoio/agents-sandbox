package opencodemsb

import (
	"os"
	"sync"
)

// SecretMap maps environment variable names to their allowed hosts.
// These secrets will be forwarded to the microsandbox VM.
var SecretMap = map[string]string{
	"LITELLM_API_KEY": "litellm.inoio.de",
	"GITHUB_TOKEN":    "github.com",
}

var (
	logMu  sync.Mutex
	logOut = newLogger(os.Stderr, false)
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
