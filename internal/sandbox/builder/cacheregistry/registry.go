package cacheregistry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry/handlers"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

const (
	endpointFileName = "builder-registry.json"
	configFileName   = "builder-registry-config.json"
	lockFileName     = "builder-registry.lock"
	cacheDirName     = "builder-registry-cache"

	registryIdleCheckInterval = 5 * time.Second
	registryReadHeaderTimeout = 10 * time.Second
	registryBasePort          = 5000
	registryCatalogMaxEntries = 1000
	registryStateFileMode     = 0o600
)

// IdleTimeout is how long the builder-side singletons stay alive without
// activity: the embedded cache registry daemon and the shared builder VM both
// use it, so they expire together rather than one outliving the other.
const IdleTimeout = 30 * time.Second

// GuestHost is the name a builder VM resolves to the host. The cache
// reference and BuildKit auth key use it, and the VM reaches it at the host's
// loopback (microsandbox forwards the host alias there).
const GuestHost = "host.microsandbox.internal"

// registryLoopbackBind is the host address the registry binds. Loopback is
// reachable from the builder VM and never exposed beyond the host, satisfying
// the "never 0.0.0.0" invariant.
const registryLoopbackBind = "127.0.0.1"

// registryLoopbackBind6 is the IPv6 loopback, bound best-effort so a guest that
// resolves the host alias to its AAAA record is also served.
const registryLoopbackBind6 = "::1"

// Endpoint is a ready builder cache registry reachable from the builder VM.
type Endpoint struct {
	// Host is the guest-visible registry host (the sandbox host alias), the
	// authority of the cache reference and the BuildKit auth key.
	Host  string
	Port  int
	Token string

	// dialHost is the host-side address the registry listens on. It differs
	// from Host (loopback vs. the guest alias); host-side callers dial it.
	// Empty falls back to Host.
	dialHost string

	// root is the registry's filesystem storage root. It is unexported because
	// callers address the registry over HTTP; Prune uses it only to drop the
	// empty repository directories the distribution API leaves behind.
	root string
}

// registryConfig is persisted for the detached child so it can serve the
// registry with the same root, bind address, token, and idle timeout that the
// parent chose.
type registryConfig struct {
	Root        string        `json:"root"`
	Host        string        `json:"host"`
	Port        int           `json:"port"`
	Token       string        `json:"token"`
	IdleTimeout time.Duration `json:"idleTimeout"`
}

// registryBindHost and registryBindPort are variables so tests can rebind the
// registry without a real host; production binds loopback, never 0.0.0.0.
var (
	registryBindHost = defaultRegistryBindHost
	registryBindPort = firstFreeRegistryPort
)

// registrySpawnFunc starts the detached serve process and returns its pid.
type registrySpawnFunc func(ctx context.Context, configPath string) (int, error)

// defaultRegistryBindHost returns the host address the registry binds: the
// host's loopback, which the builder VM reaches through the sandbox host alias.
func defaultRegistryBindHost() (string, error) {
	return registryLoopbackBind, nil
}

// firstFreeRegistryPort returns the first port at or above registryBasePort that
// can be bound on host, the address the registry will actually serve on.
func firstFreeRegistryPort(host string) (int, error) {
	var lastErr error
	for port := registryBasePort; port < registryBasePort+1024; port++ {
		//nolint:noctx // a transient probe of host port availability needs no context
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			lastErr = err
			continue
		}
		_ = listener.Close()
		return port, nil
	}
	return 0, fmt.Errorf("find free registry port on %s: %w", host, lastErr)
}

// ConfigFlagName is the flag the daemon child is launched with to locate its
// persisted registry config.
const ConfigFlagName = "config"

// ServeCommandArgs runs the registry daemon from the hidden command's argument
// list (the args after the command name), reading the --config path. It is the
// single entry point shared by the real CLI command and a test binary's TestMain,
// which must serve the daemon that Ensure re-execs: under `go test` the spawner's
// os.Executable() is the test binary, not the agents-sandbox binary.
func ServeCommandArgs(ctx context.Context, args []string) error {
	configPath, err := configPathFromArgs(args)
	if err != nil {
		return err
	}
	return Serve(ctx, configPath)
}

func configPathFromArgs(args []string) (string, error) {
	flag := "--" + ConfigFlagName
	for index, arg := range args {
		if arg != flag {
			continue
		}
		if index+1 >= len(args) {
			return "", fmt.Errorf("%s requires a config path", flag)
		}
		return args[index+1], nil
	}
	return "", fmt.Errorf("missing %s", flag)
}

// Serve runs the cache registry until ctx is cancelled or it has been quiet for
// the configured idle timeout. It is the entry point of the hidden
// __builder-registry command.
func Serve(ctx context.Context, configPath string) error {
	cfg, err := readRegistryConfig(configPath)
	if err != nil {
		return err
	}
	handler, app, err := newRegistryHandler(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = app.Shutdown() }()

	//exhaustruct:ignore // only the last-request timestamp matters here
	activity := &requestActivity{}
	activity.touch()
	server := &http.Server{
		Handler:           trackActivity(handler, activity),
		ReadHeaderTimeout: registryReadHeaderTimeout,
	}

	host, err := bindHost(cfg)
	if err != nil {
		return err
	}
	port, err := bindPort(cfg, host)
	if err != nil {
		return err
	}
	listeners, err := listenLoopback(ctx, host, port)
	if err != nil {
		return err
	}

	idleTimeout, checkInterval := idleSchedule(cfg.IdleTimeout)
	go reapWhenIdle(ctx, server, activity, idleTimeout, checkInterval)

	var serving sync.WaitGroup
	for _, listener := range listeners {
		serving.Add(1)
		go func(listener net.Listener) {
			defer serving.Done()
			if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				_ = server.Close()
			}
		}(listener)
	}
	serving.Wait()
	return nil
}

// listenLoopback binds the configured host address and, when that is IPv4
// loopback, also best-effort binds IPv6 loopback so a guest resolving the host
// alias to its AAAA record is served too. It never binds a wildcard address.
func listenLoopback(ctx context.Context, host string, port int) ([]net.Listener, error) {
	addresses := []string{net.JoinHostPort(host, strconv.Itoa(port))}
	if host == registryLoopbackBind {
		addresses = append(addresses, net.JoinHostPort(registryLoopbackBind6, strconv.Itoa(port)))
	}
	var listenConfig net.ListenConfig
	listeners := make([]net.Listener, 0, len(addresses))
	for index, address := range addresses {
		listener, err := listenConfig.Listen(ctx, "tcp", address)
		if err != nil {
			if index == 0 {
				return nil, fmt.Errorf("listen for builder registry on %s: %w", address, err)
			}
			continue
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

// newRegistryHandler assembles the distribution registry app and wraps it in
// the token middleware.
func newRegistryHandler(ctx context.Context, cfg registryConfig) (http.Handler, *handlers.App, error) {
	registryConfiguration, err := buildRegistryConfiguration(cfg.Root)
	if err != nil {
		return nil, nil, err
	}
	app := handlers.NewApp(ctx, registryConfiguration)
	return tokenAuth(cfg.Token, app), app, nil
}

// buildRegistryConfiguration is a filesystem-backed registry with manifest
// deletion enabled (Prune needs it) and a random HTTP secret.
func buildRegistryConfiguration(root string) (*configuration.Configuration, error) {
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate registry http secret: %w", err)
	}
	config := &configuration.Configuration{
		Version: "0.1",
		Log:     configuration.Log{Level: "error"},
		Storage: configuration.Storage{
			"filesystem": configuration.Parameters{"rootdirectory": root},
			"delete":     configuration.Parameters{"enabled": true},
			// Enable the in-memory blob-descriptor cache. Without it every blob
			// descriptor lookup during cache import/export hits the filesystem
			// driver, which is the registry's hottest path.
			"cache": configuration.Parameters{"blobdescriptor": "inmemory"},
		},
		HTTP:    configuration.HTTP{Secret: hex.EncodeToString(secret)},
		Catalog: configuration.Catalog{MaxEntries: registryCatalogMaxEntries},
		Tags:    configuration.Tags{MaxTags: registryCatalogMaxEntries},
	}
	return config, nil
}

// writeRegistryConfig atomically writes the child config as 0600.
func writeRegistryConfig(path string, cfg registryConfig) error {
	return writeStateFile(path, cfg)
}

func readRegistryConfig(path string) (registryConfig, error) {
	//nolint:gosec // G703: path is the config this binary wrote for its own daemon child
	data, err := os.ReadFile(path)
	if err != nil {
		return registryConfig{}, fmt.Errorf("read builder registry config: %w", err)
	}
	var cfg registryConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return registryConfig{}, fmt.Errorf("decode builder registry config: %w", err)
	}
	return cfg, nil
}

func bindHost(cfg registryConfig) (string, error) {
	if cfg.Host != "" {
		return cfg.Host, nil
	}
	return registryBindHost()
}

func bindPort(cfg registryConfig, host string) (int, error) {
	if cfg.Port > 0 {
		return cfg.Port, nil
	}
	return registryBindPort(host)
}

func idleSchedule(configured time.Duration) (time.Duration, time.Duration) {
	idleTimeout := configured
	if idleTimeout <= 0 {
		idleTimeout = IdleTimeout
	}
	checkInterval := min(registryIdleCheckInterval, idleTimeout/2)
	if checkInterval <= 0 {
		checkInterval = time.Millisecond
	}
	return idleTimeout, checkInterval
}

// requestActivity records the most recent request time so the idle reaper can
// shut the daemon down after a quiet period.
type requestActivity struct {
	lastUnixNano atomic.Int64
}

func (activity *requestActivity) touch() {
	activity.lastUnixNano.Store(time.Now().UnixNano())
}

func (activity *requestActivity) idleFor() time.Duration {
	return time.Since(time.Unix(0, activity.lastUnixNano.Load()))
}

func trackActivity(next http.Handler, activity *requestActivity) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		activity.touch()
		next.ServeHTTP(writer, request)
	})
}

func reapWhenIdle(
	ctx context.Context,
	server *http.Server,
	activity *requestActivity,
	idleTimeout time.Duration,
	checkInterval time.Duration,
) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = server.Close()
			return
		case <-ticker.C:
			if activity.idleFor() >= idleTimeout {
				_ = server.Close()
				return
			}
		}
	}
}

// writeStateFile atomically writes JSON with 0600 permissions, creating the
// parent directory if needed.
func writeStateFile(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create builder registry state dir: %w", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode builder registry state: %w", err)
	}
	temp, err := os.CreateTemp(dir, "builder-registry-*.tmp")
	if err != nil {
		return fmt.Errorf("create builder registry temp file: %w", err)
	}
	tempPath := temp.Name()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("write builder registry state: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close builder registry temp file: %w", err)
	}
	if err := os.Chmod(tempPath, registryStateFileMode); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("chmod builder registry state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename builder registry state: %w", err)
	}
	return nil
}

// cacheDirPath is the filesystem storage root of the embedded registry.
func cacheDirPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), cacheDirName)
}
