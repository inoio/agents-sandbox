package cacheregistry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

const (
	registryLogFileName = "builder-registry.log"

	registryTokenBytes        = 32
	registryHealthTimeout     = 500 * time.Millisecond
	registryStartTimeout      = 5 * time.Second
	registryStartPollInterval = 50 * time.Millisecond
)

// CommandName is the hidden CLI command the daemon is re-exec'd as.
const CommandName = "__builder-registry"

// endpointRecord is the on-disk pointer to the running daemon. Pid is advisory:
// liveness is confirmed by a /v2/ health probe, not by the pid alone.
type endpointRecord struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Token string `json:"token"`
	Pid   int    `json:"pid"`
}

var (
	registryHTTPClient                   = &http.Client{Timeout: registryHealthTimeout}
	spawnRegistry      registrySpawnFunc = realSpawnRegistry

	// registryKeepAliveInterval is the client-side keep-alive cadence that
	// refreshes the daemon's idle timer during a build. It is well below
	// IdleTimeout so the quiet phase between a build's cache import and export
	// cannot let the idle reaper fire. It is a variable so tests can shorten it.
	registryKeepAliveInterval = 5 * time.Second

	// touchRegistry refreshes the daemon's idle-activity timer with a health
	// probe. It is a variable so tests can observe keep-alive ticks without a
	// live daemon.
	touchRegistry = func(ctx context.Context, endpoint Endpoint) {
		_ = registryHealthy(ctx, endpoint)
	}
)

// Ensure returns a ready cache registry endpoint, starting the per-user
// singleton daemon if none is running or the published one fails its health
// probe. An exclusive flock serializes racing starters.
func Ensure(ctx context.Context) (Endpoint, error) {
	release, err := acquireRegistryLock()
	if err != nil {
		return Endpoint{}, err
	}
	defer release()

	if record, ok := readEndpointFile(); ok {
		endpoint := Endpoint{
			Host: GuestHost, Port: record.Port, Token: record.Token,
			dialHost: record.Host, root: cacheDirPath(),
		}
		if registryHealthy(ctx, endpoint) {
			return endpoint, nil
		}
	}
	return startRegistry(ctx)
}

func startRegistry(ctx context.Context) (Endpoint, error) {
	config, err := newChildConfig()
	if err != nil {
		return Endpoint{}, err
	}
	configPath := registryConfigPath()
	if configErr := writeRegistryConfig(configPath, config); configErr != nil {
		return Endpoint{}, configErr
	}
	pid, err := spawnRegistry(ctx, configPath)
	if err != nil {
		return Endpoint{}, fmt.Errorf("spawn builder registry: %w", err)
	}
	endpoint := Endpoint{
		Host: GuestHost, Port: config.Port, Token: config.Token,
		dialHost: config.Host, root: config.Root,
	}
	if err := waitHealthy(ctx, endpoint); err != nil {
		return Endpoint{}, err
	}
	record := endpointRecord{Host: config.Host, Port: endpoint.Port, Token: endpoint.Token, Pid: pid}
	if err := writeEndpointFile(record); err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

func newChildConfig() (registryConfig, error) {
	token, err := mintToken()
	if err != nil {
		return registryConfig{}, err
	}
	host, err := registryBindHost()
	if err != nil {
		return registryConfig{}, err
	}
	port, err := registryBindPort(host)
	if err != nil {
		return registryConfig{}, err
	}
	return registryConfig{
		Root:        cacheDirPath(),
		Host:        host,
		Port:        port,
		Token:       token,
		IdleTimeout: IdleTimeout,
	}, nil
}

func mintToken() (string, error) {
	buffer := make([]byte, registryTokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("mint builder registry token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func registryHealthy(ctx context.Context, endpoint Endpoint) bool {
	probeCtx, cancel := context.WithTimeout(ctx, registryHealthTimeout)
	defer cancel()
	probeURL := "http://" + endpoint.dialAddress() + "/v2/"
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, nil)
	if err != nil {
		return false
	}
	request.Header.Set("Authorization", "Bearer "+endpoint.Token)
	response, err := registryHTTPClient.Do(request)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode == http.StatusOK
}

// KeepAlive refreshes the daemon's idle-activity timer on an interval until the
// returned stop function is called or ctx is done. The registry only sees HTTP
// traffic at cache import and export, so a build that runs longer than
// IdleTimeout would otherwise let the reaper shut the daemon down mid-build and
// fail the cache export with a reset connection. The stop function blocks until
// the loop has exited and is safe to call more than once.
func (e Endpoint) KeepAlive(ctx context.Context) func() {
	done := make(chan struct{})
	exited := make(chan struct{})
	ticker := time.NewTicker(registryKeepAliveInterval)
	go func() {
		defer close(exited)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				touchRegistry(ctx, e)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-exited
		})
	}
}

func waitHealthy(ctx context.Context, endpoint Endpoint) error {
	deadline := time.Now().Add(registryStartTimeout)
	for {
		if registryHealthy(ctx, endpoint) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"builder registry at %s did not become ready",
				endpoint.dialAddress(),
			)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registryStartPollInterval):
		}
	}
}

// realSpawnRegistry re-execs the agents-sandbox binary as the detached registry
// child, detached from the parent's session so it outlives the launcher.
func realSpawnRegistry(ctx context.Context, configPath string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locate agents-sandbox executable: %w", err)
	}
	logFile, err := os.OpenFile(registryLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, registryStateFileMode)
	if err != nil {
		return 0, fmt.Errorf("open builder registry log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	command := exec.CommandContext(context.WithoutCancel(ctx), executable, CommandName, "--config", configPath)
	command.Stdout = logFile
	command.Stderr = logFile
	return startDetachedCommand(command)
}

// startDetachedCommand starts command in its own session (so it survives the
// launcher) and reaps it in the background once it exits. Reaping matters because
// the launcher can stay alive long after the daemon idle-reaps: without a Wait,
// the exited child lingers as a zombie until the launcher itself exits.
func startDetachedCommand(command *exec.Cmd) (int, error) {
	//exhaustruct:ignore // only Setsid matters: detach into a new session
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("start detached command: %w", err)
	}
	pid := command.Process.Pid
	go func() { _ = command.Wait() }()
	return pid, nil
}

func acquireRegistryLock() (func(), error) {
	dir := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create builder registry state dir: %w", err)
	}
	path := registryLockPath()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, registryStateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open builder registry lock %s: %w", path, err)
	}
	if err := state.FlockExclusive(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock builder registry %s: %w", path, err)
	}
	return func() { _ = file.Close() }, nil
}

func writeEndpointFile(record endpointRecord) error {
	return writeStateFile(endpointPath(), record)
}

func readEndpointFile() (endpointRecord, bool) {
	data, err := os.ReadFile(endpointPath())
	if err != nil {
		return endpointRecord{}, false
	}
	var record endpointRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return endpointRecord{}, false
	}
	return record, true
}

func endpointPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), endpointFileName)
}

func registryConfigPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), configFileName)
}

func registryLockPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), lockFileName)
}

func registryLogPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), registryLogFileName)
}
