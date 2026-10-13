package cacheregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ociManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	ociConfigMediaType   = "application/vnd.oci.image.config.v1+json"
	registryAPITimeout   = 30 * time.Second
)

var (
	cacheTagSanitizer = regexp.MustCompile(`[^a-z0-9]+`)
	registryAPIClient = &http.Client{Timeout: registryAPITimeout}
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// registryRequest issues an authenticated request against the embedded
// registry's HTTP API.
func registryRequest(
	ctx context.Context,
	endpoint Endpoint,
	method string,
	path string,
	accept string,
) (*http.Response, error) {
	requestURL := "http://" + endpoint.RegistryHost() + path
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build registry request %s %s: %w", method, path, err)
	}
	request.Header.Set("Authorization", "Bearer "+endpoint.Token)
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := registryAPIClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("registry request %s %s: %w", method, path, err)
	}
	return response, nil
}

// RegistryHost is the "host:port" the registry is reachable at. It is both the
// authority of the cache reference and the key BuildKit resolves registry auth
// against, so the two must always agree.
func (e Endpoint) RegistryHost() string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// CacheRef derives the deterministic OCI reference BuildKit uses as the
// registry cache target for an image identity (tag + platform), so caches are
// reusable across builds and distinct between projects/agents/platforms.
func (e Endpoint) CacheRef(imageTag, platform string) string {
	return fmt.Sprintf(
		"%s/agents-sandbox/%s-%s:cache",
		e.RegistryHost(),
		sanitizeCacheComponent(imageTag),
		sanitizeCacheComponent(platformArch(platform)),
	)
}

func platformArch(platform string) string {
	if _, arch, found := strings.CutLast(platform, "/"); found {
		return arch
	}
	return platform
}

func sanitizeCacheComponent(value string) string {
	sanitized := strings.Trim(cacheTagSanitizer.ReplaceAllString(strings.ToLower(value), "-"), "-")
	if sanitized == "" {
		return "cache"
	}
	return sanitized
}

// Prune deletes every cache repository whose name matches the predicate,
// including its manifests and referenced blobs. Callers pass an
// `agents-sandbox/` prefix predicate to scope deletion to this tool's caches.
func Prune(ctx context.Context, endpoint Endpoint, match func(reference string) bool) error {
	repositories, err := listCatalog(ctx, endpoint)
	if err != nil {
		return err
	}
	for _, repository := range repositories {
		if !match(repository) {
			continue
		}
		if err := pruneRepository(ctx, endpoint, repository); err != nil {
			return err
		}
		if err := removeRepositoryDirectory(endpoint.root, repository); err != nil {
			return err
		}
	}
	return nil
}

// removeRepositoryDirectory drops the (now manifest-free) repository directory
// so the distribution catalog stops listing it. The HTTP API deletes content
// but leaves the directory tree behind.
func removeRepositoryDirectory(root, repository string) error {
	if root == "" {
		return nil
	}
	path := filepath.Join(root, "docker", "registry", "v2", "repositories", filepath.FromSlash(repository))
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove repository directory %s: %w", path, err)
	}
	return nil
}

func pruneRepository(ctx context.Context, endpoint Endpoint, repository string) error {
	tags, err := listTags(ctx, endpoint, repository)
	if err != nil {
		return err
	}
	for _, tag := range tags {
		manifest, digest, err := getManifest(ctx, endpoint, repository, tag)
		if err != nil {
			return err
		}
		for _, blobDigest := range manifestBlobDigests(manifest) {
			if err := deleteResource(ctx, endpoint, repository, "blobs", blobDigest); err != nil {
				return err
			}
		}
		if err := deleteResource(ctx, endpoint, repository, "manifests", digest); err != nil {
			return err
		}
	}
	return nil
}

type imageManifest struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
}

func manifestBlobDigests(data []byte) []string {
	var manifest imageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	digests := make([]string, 0, len(manifest.Layers)+1)
	if manifest.Config.Digest != "" {
		digests = append(digests, manifest.Config.Digest)
	}
	for _, layer := range manifest.Layers {
		if layer.Digest != "" {
			digests = append(digests, layer.Digest)
		}
	}
	return digests
}

func listCatalog(ctx context.Context, endpoint Endpoint) ([]string, error) {
	response, err := registryRequest(ctx, endpoint, http.MethodGet, "/v2/_catalog", "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list builder registry catalog: status %d", response.StatusCode)
	}
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(response.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode builder registry catalog: %w", err)
	}
	return catalog.Repositories, nil
}

func listTags(ctx context.Context, endpoint Endpoint, repository string) ([]string, error) {
	response, err := registryRequest(ctx, endpoint, http.MethodGet, "/v2/"+repository+"/tags/list", "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list tags for %s: status %d", repository, response.StatusCode)
	}
	var tags struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decode tags for %s: %w", repository, err)
	}
	return tags.Tags, nil
}

func getManifest(ctx context.Context, endpoint Endpoint, repository, tag string) ([]byte, string, error) {
	response, err := registryRequest(
		ctx, endpoint, http.MethodGet, "/v2/"+repository+"/manifests/"+tag, ociManifestMediaType,
	)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("get manifest %s:%s: status %d", repository, tag, response.StatusCode)
	}
	digest := response.Header.Get("Docker-Content-Digest")
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read manifest %s:%s: %w", repository, tag, err)
	}
	if digest == "" {
		digest = "sha256:" + sha256Hex(data)
	}
	return data, digest, nil
}

func deleteResource(ctx context.Context, endpoint Endpoint, repository, kind, digest string) error {
	response, err := registryRequest(
		ctx, endpoint, http.MethodDelete, "/v2/"+repository+"/"+kind+"/"+digest, "",
	)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete %s %s/%s: status %d", kind, repository, digest, response.StatusCode)
	}
	return nil
}
