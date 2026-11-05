package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyAssetChecksum(t *testing.T) {
	asset := []byte("release-binary")
	digest := sha256.Sum256(asset)

	path := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(path, asset, 0o600); err != nil {
		t.Fatal(err)
	}

	// A prerelease tag proves the checksums fetch is addressed by the same
	// release tag as the asset download.
	const version = "1.9.0-canary.3"
	releaseServer(t, version, nil, checksumLine(digest[:], assetName()))
	if err := verifyAssetChecksum(context.Background(), version, path, assetName()); err != nil {
		t.Fatalf("expected a verified match, got %v", err)
	}
}

func TestVerifyAssetChecksumUnreadableAsset(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	releaseServer(t, "2.0.0", nil, checksumLine(make([]byte, sha256.Size), assetName()))
	err := verifyAssetChecksum(context.Background(), "2.0.0", missing, assetName())
	if err == nil {
		t.Fatal("expected error when the downloaded asset cannot be read")
	}
	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Fatalf("expected the hash error to surface, got %v", err)
	}
}

func TestVerifyAssetChecksumMissingEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(path, []byte("release-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseServer(t, "2.0.0", nil, checksumLine(make([]byte, sha256.Size), "agents-sandbox-other-arch"))

	if err := verifyAssetChecksum(
		context.Background(),
		"2.0.0",
		path,
		assetName(),
	); err == nil ||
		!strings.Contains(err.Error(), "no entry for") {
		t.Fatalf("expected a missing-entry error, got %v", err)
	}
}

func TestVerifyAssetChecksumFetchFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(path, []byte("release-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	pointDownloadBase(t, "://missing-scheme")

	err := verifyAssetChecksum(context.Background(), "2.0.0", path, assetName())
	if err == nil {
		t.Fatal("expected error when the checksums fetch fails")
	}
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("expected the fetch error to surface, got %v", err)
	}
}

func TestVerifyAssetChecksumMismatch(t *testing.T) {
	asset := []byte("release-binary")
	path := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(path, asset, 0o600); err != nil {
		t.Fatal(err)
	}

	wrong := sha256.Sum256([]byte("tampered"))
	releaseServer(t, "2.0.0", nil, checksumLine(wrong[:], assetName()))
	if err := verifyAssetChecksum(context.Background(), "2.0.0", path, assetName()); err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
}

// TestFetchChecksumsSizeLimit pins the read bound at its literal limit: a body
// of exactly maxChecksumsBytes is accepted, one byte more is rejected. The
// literal sizes keep the constant's value observable.
func TestFetchChecksumsSizeLimit(t *testing.T) {
	const limit = 1 << 20
	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "at the limit", size: limit},
		{name: "one byte over", size: limit + 1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(bytes.Repeat([]byte("a"), tc.size))
			}))
			t.Cleanup(srv.Close)
			pointDownloadBase(t, srv.URL)

			data, err := fetchChecksums(context.Background(), "2.0.0")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error for an oversized checksums.txt")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(data) != tc.size {
				t.Fatalf("read %d bytes, want %d", len(data), tc.size)
			}
		})
	}
}

func TestFetchChecksumsUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	pointDownloadBase(t, srv.URL)

	if _, err := fetchChecksums(
		context.Background(),
		"2.0.0",
	); err == nil ||
		!strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("expected an unexpected-status error, got %v", err)
	}
}

func TestFetchChecksumsSetsUserAgent(t *testing.T) {
	var gotUserAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, "data")
	}))
	t.Cleanup(srv.Close)
	pointDownloadBase(t, srv.URL)

	if _, err := fetchChecksums(context.Background(), "2.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUserAgent != "agents-sandbox" {
		t.Fatalf("User-Agent = %q, want agents-sandbox", gotUserAgent)
	}
}

func TestFetchChecksumsNetworkError(t *testing.T) {
	pointDownloadBase(t, "http://127.0.0.1:1")

	_, err := fetchChecksums(context.Background(), "2.0.0")
	if err == nil {
		t.Fatal("expected error for an unreachable checksums endpoint")
	}
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("expected the transport error to be preserved, got %v", err)
	}
}

func TestFetchChecksumsTruncatedBody(t *testing.T) {
	// A body shorter than its declared Content-Length makes the read fail, so
	// this exercises the read-error branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	pointDownloadBase(t, srv.URL)

	_, err := fetchChecksums(context.Background(), "2.0.0")
	if err == nil {
		t.Fatal("expected error for a truncated checksums.txt")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected the read error to be preserved, got %v", err)
	}
}

func TestFetchChecksumsBadURL(t *testing.T) {
	pointDownloadBase(t, "://missing-scheme")

	_, err := fetchChecksums(context.Background(), "2.0.0")
	if err == nil {
		t.Fatal("expected error for an unbuildable checksums request")
	}
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("expected the request error to be preserved, got %v", err)
	}
}

func TestChecksumForAsset(t *testing.T) {
	// A fixed 64-character digest; its value is irrelevant to parsing.
	const digest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "two-space line", body: digest + "  " + assetName() + "\n", want: digest},
		{name: "surrounding blank lines", body: "\n" + digest + "  " + assetName() + "\n\n", want: digest},
		{name: "uppercase digest normalized", body: strings.ToUpper(digest) + "  " + assetName() + "\n", want: digest},
		{
			name: "other assets ignored",
			body: digest + "  agents-sandbox-other-arch\n" + digest + "  " + assetName() + "\n",
			want: digest,
		},
		{name: "missing entry", body: digest + "  agents-sandbox-other-arch\n", wantErr: true},
		{name: "empty file", body: "", wantErr: true},
		{name: "short digest", body: "abcd  " + assetName() + "\n", wantErr: true},
		{name: "non-hex digest", body: strings.Repeat("z", 64) + "  " + assetName() + "\n", wantErr: true},
		{name: "extra field", body: digest + " extra " + assetName() + "\n", wantErr: true},
		{
			name: "duplicate entries take the first",
			body: strings.Repeat("f", 64) + "  " + assetName() + "\n" + digest + "  " + assetName() + "\n",
			want: strings.Repeat("f", 64),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checksumForAsset([]byte(tc.body), assetName())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum := sha256.Sum256([]byte("payload"))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFileSHA256OpenError(t *testing.T) {
	_, err := fileSHA256(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for a missing file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected the open error to be preserved, got %v", err)
	}
}

func TestFileSHA256ReadError(t *testing.T) {
	// Opening a directory succeeds; copying from it fails, so this exercises
	// the read-error branch.
	_, err := fileSHA256(t.TempDir())
	if err == nil {
		t.Fatal("expected error when hashing a directory")
	}
	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Fatalf("expected the read error to be preserved, got %v", err)
	}
}
