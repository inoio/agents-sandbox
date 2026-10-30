package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// checksumsAsset is the release asset naming the SHA-256 of every other asset.
const checksumsAsset = "checksums.txt"

// maxChecksumsBytes bounds how much of checksums.txt is read. The file holds
// one line per release asset (a few hundred bytes), so a larger response is
// malformed rather than useful.
const maxChecksumsBytes = 1 << 20

// assetName is the flat name of the release asset for the current platform,
// matching the names the release workflow uploads and lists in checksums.txt.
func assetName() string {
	return fmt.Sprintf("agents-sandbox-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// verifyAssetChecksum fetches the release's checksums.txt for the same tag and
// confirms the downloaded asset's SHA-256 matches its entry. It fails closed:
// a missing checksums.txt, a missing or malformed entry, or a digest mismatch
// is an error, so an unverified binary is never installed.
func verifyAssetChecksum(ctx context.Context, version, assetPath, asset string) error {
	checksums, err := fetchChecksums(ctx, version)
	if err != nil {
		return err
	}
	want, err := checksumForAsset(checksums, asset)
	if err != nil {
		return err
	}
	got, err := fileSHA256(assetPath)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: downloaded %s, release publishes %s", asset, got, want)
	}
	return nil
}

// fetchChecksums downloads the release's checksums.txt for version, addressed
// by the same release tag as the asset download.
func fetchChecksums(ctx context.Context, version string) ([]byte, error) {
	url := fmt.Sprintf("%s/v%s/%s", downloadBase, version, checksumsAsset)
	req, err := newAssetRequest(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", checksumsAsset, err)
	}
	// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="the client timeout is defensive; a local test server answers immediately and the value is not a contract"
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", checksumsAsset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %d", checksumsAsset, resp.StatusCode)
	}
	// gomutants:disable-next-line INTEGER_INCREMENT reason="reading max+1 or max+2 bytes both cross the same bound, so the overflow sentinel's size is equivalent"
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", checksumsAsset, err)
	}
	if len(data) > maxChecksumsBytes {
		return nil, fmt.Errorf("read %s: response exceeds %d bytes", checksumsAsset, maxChecksumsBytes)
	}
	return data, nil
}

// checksumForAsset returns the SHA-256 recorded for asset in a checksums.txt
// body. Entries use the sha256sum shape - a 64-hex digest, whitespace, then the
// asset name (the published file uses two spaces); a line that names asset with
// any other shape is malformed and fails closed. A duplicate entry resolves to
// the first match.
func checksumForAsset(checksums []byte, asset string) (string, error) {
	for line := range strings.SplitSeq(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[len(fields)-1] != asset {
			continue
		}
		if len(fields) != 2 || !isSHA256Hex(fields[0]) {
			return "", fmt.Errorf("%s: malformed entry for %s", checksumsAsset, asset)
		}
		return strings.ToLower(fields[0]), nil
	}
	return "", fmt.Errorf("%s: no entry for %s", checksumsAsset, asset)
}

// isSHA256Hex reports whether s is a 64-character hexadecimal string.
func isSHA256Hex(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// fileSHA256 returns the lowercase hex SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash downloaded binary: %w", err)
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return "", fmt.Errorf("hash downloaded binary: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
