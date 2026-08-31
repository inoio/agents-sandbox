// Package update checks the opencode-sandbox GitHub releases for a newer
// version than the one running, and optionally installs it. It mirrors how
// opencode resolves its own latest release (GitHub releases/latest) and how
// it compares version strings (numerical dot-separated segments).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/opencode"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

const (
	githubRepo = "inoio/opencode-sandbox"

	// devVersion is the version baked into locally built binaries; we never
	// auto-update or compare against it meaningfully.
	devVersion = "dev"

	// DefaultInterval is how often to check for a new release when unset.
	DefaultInterval = 24 * time.Hour
	// MinInterval guards against hammering the GitHub API (which rate-limits
	// unauthenticated requests) with absurdly frequent checks.
	MinInterval = time.Hour

	stateFileName = "update.json"
)

//nolint:gochecknoglobals // test seams for the otherwise unmockable endpoints
var (
	latestURL    = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	downloadBase = fmt.Sprintf("https://github.com/%s/releases/latest/download", githubRepo)
)

// Mode selects how a detected newer release is handled.
type Mode string

const (
	// ModePrompt asks the user what to do when a newer release is found.
	ModePrompt Mode = "prompt"
	// ModeNotify only reports the newer release without installing it.
	ModeNotify Mode = "notify"
	// ModeAutoUpgrade installs the newer release and continues running.
	ModeAutoUpgrade Mode = "auto-upgrade"
	// ModeAutoUpgradeExit installs the newer release and exits so the next
	// invocation uses the new binary.
	ModeAutoUpgradeExit Mode = "auto-upgrade-exit"
)

// ParseMode maps a mode name to its Mode, returning an error for unknown
// values. It is case-insensitive and trims surrounding whitespace.
func ParseMode(s string) (Mode, error) {
	m := Mode(strings.ToLower(strings.TrimSpace(s)))
	switch m {
	case ModePrompt, ModeNotify, ModeAutoUpgrade, ModeAutoUpgradeExit:
		return m, nil
	default:
		return "", fmt.Errorf("invalid update mode %q (want prompt, notify, auto-upgrade or auto-upgrade-exit)", s)
	}
}

// Result describes what Check decided to do.
type Result struct {
	HasUpdate bool
	Latest    string
	Updated   bool
	Exit      bool
}

// Options configures a single Check.
type Options struct {
	CurrentVersion string
	Mode           Mode
	Interval       time.Duration
	StatePath      string
	UI             termio.UI

	// UpdateFunc installs the given latest version over the running binary.
	// When nil, updateExecutable is used. Tests inject it to avoid touching
	// the running executable.
	UpdateFunc func(ctx context.Context, latest string) error
}

// state is the persisted per-user update state.
type state struct {
	LastCheck         time.Time `json:"last_check"`
	DismissedVersions []string  `json:"dismissed_versions,omitempty"`
}

func (o Options) statePath() string {
	if o.StatePath != "" {
		return o.StatePath
	}
	return filepath.Join(configpaths.Get().UserStateDir(), stateFileName)
}

// Check looks for a newer opencode-sandbox release than CurrentVersion and
// acts on it according to Mode. It is a no-op for development builds and when
// a check happened within Interval. Transient network or parsing failures are
// silently ignored so an offline run never blocks startup.
func Check(ctx context.Context, opts Options) (Result, error) {
	if opts.CurrentVersion == "" || opts.CurrentVersion == devVersion {
		return Result{}, nil
	}
	interval := opts.Interval
	switch {
	case interval <= 0:
		interval = DefaultInterval
	case interval < MinInterval:
		interval = MinInterval
	}
	updateFunc := opts.UpdateFunc
	if updateFunc == nil {
		updateFunc = updateExecutable
	}

	path := opts.statePath()
	st := loadState(path)
	if time.Since(st.LastCheck) < interval {
		return Result{}, nil
	}

	latest, err := latestRelease(ctx)
	if err != nil {
		opts.UI.Verbosef("update check failed: %v", err)
		return Result{}, nil
	}

	if opencode.VersionCompare(opts.CurrentVersion, latest) >= 0 ||
		slices.Contains(st.DismissedVersions, latest) {
		st.LastCheck = time.Now()
		_ = saveState(path, st)
		return Result{}, nil
	}

	res := applyMode(ctx, opts, updateFunc, latest, &st)
	if res.Updated || res.Exit {
		opts.UI.Infof("opencode-sandbox updated to %s; restart to use it", latest)
	}
	st.LastCheck = time.Now()
	_ = saveState(path, st)
	return res, nil
}

// applyMode handles a detected newer release according to the configured mode
// and returns the resulting action. It may mutate st to record a dismissal.
func applyMode(
	ctx context.Context,
	opts Options,
	updateFunc func(context.Context, string) error,
	latest string,
	st *state,
) Result {
	//nolint:exhaustruct // Updated/Exit default false until set below
	res := Result{HasUpdate: true, Latest: latest}
	install := func() bool {
		if err := updateFunc(ctx, latest); err != nil {
			notify(opts.UI, opts.CurrentVersion, latest)
			return false
		}
		return true
	}
	switch opts.Mode {
	case ModeAutoUpgrade:
		res.Updated = install()
	case ModeAutoUpgradeExit:
		if install() {
			res.Updated = true
			res.Exit = true
		}
	case ModeNotify:
		notify(opts.UI, opts.CurrentVersion, latest)
	case ModePrompt, "":
		switch prompt(opts.UI, opts.CurrentVersion, latest) {
		case actionUpgrade:
			res.Updated = install()
		case actionUpgradeExit:
			if install() {
				res.Updated = true
				res.Exit = true
			}
		case actionSkip:
			st.DismissedVersions = append(st.DismissedVersions, latest)
		case actionContinue:
			// keep running the current version
		}
	}
	return res
}

// notify reports a newer release without installing it.
func notify(ui termio.UI, current, latest string) {
	ui.Infof(
		"A new version of opencode-sandbox is available: %s (you have %s). "+
			"Run `opencode-sandbox upgrade` to install it, or set update.mode in your config.",
		latest, current,
	)
}

type promptAction int

const (
	actionContinue promptAction = iota
	actionSkip
	actionUpgrade
	actionUpgradeExit
)

// prompt asks the user how to proceed with a detected newer release. When the
// UI is not interactive it falls back to a plain notification.
func prompt(ui termio.UI, current, latest string) promptAction {
	if !ui.IsInteractive() {
		notify(ui, current, latest)
		return actionContinue
	}
	choices := []termio.Choice{
		{Label: "Continue with current version", Key: "c", Description: "keep running " + current},
		{Label: "Don't ask again for this version", Key: "s", Description: "dismiss " + latest},
		{Label: "Upgrade & continue", Key: "u", Description: "install " + latest + " and keep running current"},
		{Label: "Upgrade & exit", Key: "x", Description: "install " + latest + " and exit; restart to use it"},
	}
	key, err := ui.Select(
		fmt.Sprintf("A new version of opencode-sandbox is available: %s (you have %s)", latest, current),
		choices,
		"c",
	)
	if err != nil {
		return actionContinue
	}
	switch key {
	case "u":
		return actionUpgrade
	case "x":
		return actionUpgradeExit
	case "s":
		return actionSkip
	default:
		return actionContinue
	}
}

// LatestVersion returns the newest stable opencode-sandbox release string
// (leading "v" stripped) by querying the GitHub releases/latest endpoint.
//
//nolint:gochecknoglobals // test seam
var LatestVersion = latestRelease

func latestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build latest release request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "opencode-sandbox")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("latest opencode-sandbox release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest opencode-sandbox release: unexpected status %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode latest opencode-sandbox release: %w", err)
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

// Update downloads and installs the release binary for the given version over
// the running executable.
//
//nolint:gochecknoglobals // test seam
var Update = updateExecutable

// updateExecutable downloads the latest release binary for the current
// platform and atomically replaces the running executable with it. The latest
// version is pinned by the "latest" release asset download endpoint.
func updateExecutable(ctx context.Context, _ string) error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	assetPath, err := downloadAssetToDir(ctx, filepath.Dir(exe))
	if err != nil {
		return err
	}
	defer os.Remove(assetPath)
	return replaceExecutable(assetPath, exe)
}

// downloadAssetToDir downloads the release binary for the current platform
// into dir and returns the downloaded file path.
func downloadAssetToDir(ctx context.Context, dir string) (string, error) {
	asset := fmt.Sprintf("opencode-sandbox-%s-%s", runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("%s/%s", downloadBase, asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("User-Agent", "opencode-sandbox")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: unexpected status %d", asset, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, ".opencode-sandbox-update-*")
	if err != nil {
		return "", fmt.Errorf("create download temp file: %w", err)
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("write downloaded binary: %w", err)
	}
	return tmp.Name(), nil
}

// executablePath returns the path of the running executable, resolving
// symlinks so a later rename replaces the real binary rather than the link.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve running executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return filepath.Abs(exe)
}

// replaceExecutable makes assetPath executable and renames it over exePath.
// The caller must place assetPath in exePath's directory so the rename is on
// the same filesystem and therefore atomic.
func replaceExecutable(assetPath, exePath string) error {
	//nolint:gosec // G302: the downloaded release binary must be executable
	if err := os.Chmod(assetPath, 0o755); err != nil {
		return fmt.Errorf("make downloaded binary executable: %w", err)
	}
	if err := os.Rename(assetPath, exePath); err != nil {
		return fmt.Errorf("replace running executable: %w", err)
	}
	return nil
}

// loadState reads the persisted update state, tolerating a missing or corrupt
// file by returning empty state.
func loadState(path string) state {
	data, err := os.ReadFile(path)
	if err != nil {
		return state{}
	}
	var s state
	_ = json.Unmarshal(data, &s)
	return s
}

// saveState writes the update state to path, creating its directory if needed.
func saveState(path string, s state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
