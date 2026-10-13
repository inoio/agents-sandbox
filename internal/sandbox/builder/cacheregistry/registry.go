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
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry/handlers"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

const (
	endpointFileName = "builder-registry.json"
	configFileName   = "builder-registry-config.json"
	lockFileName     = "builder-registry.lock"
	cacheDirName     = "builder-registry-cache"

	registryIdleTimeout       = 5 * time.Minute
	registryIdleCheckInterval = 5 * time.Second
	registryReadHeaderTimeout = 10 * time.Second
	registryBasePort          = 5000
	registryCatalogMaxEntries = 1000
	registryStateFileMode     = 0o600
)

// BuilderIPv4Pool is the dedicated /30 the builder VM's network pool is pinned
// to. The cache registry binds that pool's gateway address (network+1), which
// the builder VM reaches at host.microsandbox.internal, and nothing else.
const BuilderIPv4Pool = "172.30.0.0/30"

// Endpoint is a ready builder cache registry reachable from the builder VM.
type Endpoint struct {
	Host  string
	Port  int
	Token string

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
// registry to a loopback endpoint without a KVM host. Production binds the
// builder pool's gateway address, never 0.0.0.0.
var (
	registryBindHost = builderGatewayHost
	registryBindPort = func() int { return options.FirstFreeHostPort(registryBasePort) }
)

// registrySpawnFunc starts the detached serve process and returns its pid.
type registrySpawnFunc func(ctx context.Context, configPath string) (int, error)

// builderGatewayHost derives the pinned pool's gateway address (network+1).
func builderGatewayHost() string {
	prefix, err := netip.ParsePrefix(BuilderIPv4Pool)
	if err != nil {
		return ""
	}
	return prefix.Addr().Next().String()
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

	address := net.JoinHostPort(bindHost(cfg), strconv.Itoa(bindPort(cfg)))
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("listen for builder registry on %s: %w", address, err)
	}

	idleTimeout, checkInterval := idleSchedule(cfg.IdleTimeout)
	go reapWhenIdle(ctx, server, activity, idleTimeout, checkInterval)

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve builder registry: %w", err)
	}
	return nil
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

func bindHost(cfg registryConfig) string {
	if cfg.Host != "" {
		return cfg.Host
	}
	return registryBindHost()
}

func bindPort(cfg registryConfig) int {
	if cfg.Port > 0 {
		return cfg.Port
	}
	return registryBindPort()
}

func idleSchedule(configured time.Duration) (time.Duration, time.Duration) {
	idleTimeout := configured
	if idleTimeout <= 0 {
		idleTimeout = registryIdleTimeout
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
