package cacheregistry

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestEnsureStartsThenReusesDaemon(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	host := "127.0.0.1"
	port := freeTestPort(t)
	stubRegistryBind(t, host, port)

	var (
		mutex      sync.Mutex
		spawnCount int
		shutdowns  []func()
	)
	t.Cleanup(func() {
		for _, shutdown := range shutdowns {
			shutdown()
		}
	})
	restoreSpawn := stubSpawnRegistry(t, func(_ context.Context, configPath string) (int, error) {
		cfg, err := readRegistryConfig(configPath)
		if err != nil {
			return 0, err
		}
		shutdown, err := startFakeRegistry(t, cfg.Host, cfg.Port, cfg.Token)
		if err != nil {
			return 0, err
		}
		mutex.Lock()
		spawnCount++
		shutdowns = append(shutdowns, shutdown)
		mutex.Unlock()
		return os.Getpid(), nil
	})
	defer restoreSpawn()

	endpoints := make([]Endpoint, 2)
	errors := make([]error, 2)
	var waitGroup sync.WaitGroup
	for index := range endpoints {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			endpoints[index], errors[index] = Ensure(t.Context())
		}(index)
	}
	waitGroup.Wait()

	for index, err := range errors {
		if err != nil {
			t.Fatalf("Ensure call %d failed: %v", index, err)
		}
	}
	if endpoints[0] != endpoints[1] {
		t.Fatalf("endpoints differ: %+v vs %+v", endpoints[0], endpoints[1])
	}
	mutex.Lock()
	defer mutex.Unlock()
	if spawnCount != 1 {
		t.Fatalf("spawn count = %d, want 1", spawnCount)
	}
}

func TestEnsureRestartsOnDeadEndpoint(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	host := "127.0.0.1"
	port := freeTestPort(t)
	stubRegistryBind(t, host, port)

	deadPort := freeTestPort(t)
	if err := writeEndpointFile(endpointRecord{
		Host:  host,
		Port:  deadPort,
		Token: "stale-token",
		Pid:   1 << 30,
	}); err != nil {
		t.Fatalf("writeEndpointFile: %v", err)
	}

	var shutdowns []func()
	t.Cleanup(func() {
		for _, shutdown := range shutdowns {
			shutdown()
		}
	})
	restoreSpawn := stubSpawnRegistry(t, func(_ context.Context, configPath string) (int, error) {
		cfg, err := readRegistryConfig(configPath)
		if err != nil {
			return 0, err
		}
		shutdown, err := startFakeRegistry(t, cfg.Host, cfg.Port, cfg.Token)
		if err != nil {
			return 0, err
		}
		shutdowns = append(shutdowns, shutdown)
		return os.Getpid(), nil
	})
	defer restoreSpawn()

	endpoint, err := Ensure(t.Context())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(shutdowns) != 1 {
		t.Fatalf("spawn count = %d, want 1 (dead endpoint not restarted)", len(shutdowns))
	}
	if endpoint.Host != host || endpoint.Port != port {
		t.Errorf("endpoint = %+v, want host %s port %d", endpoint, host, port)
	}
	if endpoint.Token == "stale-token" {
		t.Error("endpoint still reports the stale token after restart")
	}
	record, ok := readEndpointFile()
	if !ok {
		t.Fatal("endpoint file missing after restart")
	}
	if record.Token != endpoint.Token {
		t.Errorf("endpoint file token = %q, want %q", record.Token, endpoint.Token)
	}
}

func TestEndpointFileMode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := writeEndpointFile(endpointRecord{Host: "127.0.0.1", Port: 1234, Token: "t", Pid: 9}); err != nil {
		t.Fatalf("writeEndpointFile: %v", err)
	}
	info, err := os.Stat(endpointPath())
	if err != nil {
		t.Fatalf("stat endpoint file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("endpoint file mode = %o, want 600", perm)
	}
}

func TestChildConfigFileMode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	path := registryConfigPath()
	if err := writeRegistryConfig(path, registryConfig{
		Root:        t.TempDir(),
		Host:        "127.0.0.1",
		Port:        5000,
		Token:       "secret",
		IdleTimeout: 0,
	}); err != nil {
		t.Fatalf("writeRegistryConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("child config file mode = %o, want 600", perm)
	}
}
