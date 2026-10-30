// Package upgrade checks the agents-sandbox GitHub releases for a newer
// version than the one running, and optionally installs it. It mirrors how
// opencode resolves its own latest release (GitHub releases/latest) and
// compares version strings using semantic versioning.
package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	githubRepo = "inoio/agents-sandbox"

	// devVersion is the version baked into locally built binaries; we never
	// auto-upgrade or compare against it meaningfully.
	devVersion = "dev"

	// DefaultInterval is how often to check for a new release when unset.
	DefaultInterval = 24 * time.Hour
	// MinInterval guards against hammering the GitHub API (which rate-limits
	// unauthenticated requests) with absurdly frequent checks.
	MinInterval = time.Hour
)

var (
	latestURL    = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	releasesURL  = fmt.Sprintf("https://api.github.com/repos/%s/releases", githubRepo)
	downloadBase = fmt.Sprintf("https://github.com/%s/releases/download", githubRepo)
)

// Mode selects how a detected newer release is handled.
type Mode string

const (
	// ModePrompt asks the user what to do when a newer release is found.
	ModePrompt Mode = "prompt"
	// ModeNotify only reports the newer release without installing it.
	ModeNotify Mode = "notify"
	// ModeAuto installs the newer release and continues running.
	ModeAuto Mode = "auto"
	// ModeAutoExit installs the newer release and exits so the next
	// invocation uses the new binary.
	ModeAutoExit Mode = "auto-exit"
)

// ParseMode maps a mode name to its Mode, returning an error for unknown
// values. It is case-insensitive and trims surrounding whitespace.
func ParseMode(s string) (Mode, error) {
	m := Mode(strings.ToLower(strings.TrimSpace(s)))
	switch m {
	case ModePrompt, ModeNotify, ModeAuto, ModeAutoExit:
		return m, nil
	default:
		return "", fmt.Errorf("invalid upgrade mode %q (want prompt, notify, auto or auto-exit)", s)
	}
}

// Result describes what Check decided to do.
//
//exhaustruct:ignore
type Result struct {
	HasUpdate bool
	Updated   bool
	Exit      bool
	Latest    string
}

// Options configures a single Check.
//
//exhaustruct:ignore
type Options struct {
	CurrentVersion string
	Mode           Mode
	Interval       time.Duration
	UI             termio.UI

	// Channel selects which releases to offer. The zero value means stable.
	Channel Channel

	// AllowAnyVersion is the undocumented dev-build lever: with it set, an
	// unparseable current version (empty, "dev", or garbage) counts as older
	// than any release, so a locally built binary exercises the real check and
	// install. It is never set from config or a flag.
	AllowAnyVersion bool

	// Store persists the launcher's update-check state. When nil, the check
	// runs against an in-memory store: it still works, but nothing outlives
	// the process.
	Store Store

	// UpdateFunc installs the given latest version over the running binary.
	// When nil, updateExecutable is used. Tests inject it to avoid touching
	// the running executable.
	UpdateFunc func(ctx context.Context, latest string) error
}

// store returns the injected Store, defaulting to an in-memory one. Callers
// invoke it once per Check: a nil Store allocates a fresh memoryStore on each
// call, so two calls would not share state.
func (o Options) store() Store {
	if o.Store == nil {
		return &memoryStore{}
	}
	return o.Store
}

// Check looks for a newer agents-sandbox release than CurrentVersion and
// acts on it according to Mode. It is a no-op for development builds unless
// the undocumented dev lever is set, for Homebrew-managed installs (which
// update via `brew upgrade`), and when a check happened within Interval.
// Transient network or parsing failures are silently ignored so an offline
// run never blocks startup. An explicitly configured interval below
// MinInterval is a misconfiguration and fails loudly rather than being
// silently clamped.
func Check(ctx context.Context, opts Options) (Result, error) {
	if !opts.AllowAnyVersion && (opts.CurrentVersion == "" || opts.CurrentVersion == devVersion) {
		return Result{}, nil
	}
	if InstalledViaHomebrew() {
		opts.UI.Verbosef(
			"agents-sandbox is managed by Homebrew; skipping the self-update check " +
				"(run `brew upgrade` to update)",
		)
		return Result{}, nil
	}
	interval := opts.Interval
	switch {
	case interval <= 0:
		interval = DefaultInterval
	case interval < MinInterval:
		return Result{}, fmt.Errorf("upgrade interval %s is below the minimum %s", interval, MinInterval)
	}
	// The dev lever reduces the check window to the floor rather than removing
	// the throttle: a check spends one unauthenticated GitHub request, so an
	// unthrottled check on every run would hit the rate limit.
	if opts.AllowAnyVersion && interval > MinInterval {
		interval = MinInterval
	}
	updateFunc := opts.UpdateFunc
	if updateFunc == nil {
		updateFunc = updateExecutable
	}

	store := opts.store()
	// Only fold the retired upgrade.json when a real store was injected. With a
	// nil Store the check runs against an ephemeral memoryStore, so folding
	// would delete the real file after "writing" into state that cannot outlive
	// the process: the caller is the one that knows whether persistence is real.
	//
	// A fold that fails leaves this process without the legacy timestamp and
	// dismissals, so defer the check rather than act on a state view known to be
	// incomplete (which could re-prompt a version the user already dismissed).
	// The file stays in place, so the next run retries the fold and resumes.
	if opts.Store != nil {
		if err := FoldLegacyState(store); err != nil {
			opts.UI.Verbosef("upgrade state migration deferred: %v", err)
			return Result{}, nil
		}
	}
	st, err := store.Load()
	if err != nil {
		opts.UI.Verbosef("upgrade state unavailable: %v", err)
		return Result{}, nil
	}
	if time.Since(st.LastChecked) < interval {
		return Result{}, nil
	}

	latest, err := latestForChannel(ctx, opts.Channel)
	if err != nil {
		opts.UI.Verbosef("upgrade check failed: %v", err)
		return Result{}, nil
	}

	newer, err := newerThan(opts.CurrentVersion, latest, opts.AllowAnyVersion)
	if err != nil {
		opts.UI.Verbosef("upgrade check failed: %v", err)
		return Result{}, nil
	}
	if !newer || slices.Contains(st.DismissedVersions, latest) {
		st.LastChecked = time.Now()
		persistBestEffort(store, st, opts.UI)
		return Result{}, nil
	}

	res := applyMode(ctx, opts, updateFunc, latest, &st)
	if res.Updated || res.Exit {
		opts.UI.Infof("agents-sandbox updated to %s; restart to use it", latest)
	}
	st.LastChecked = time.Now()
	persistBestEffort(store, st, opts.UI)
	return res, nil
}

// persistBestEffort persists the upgrade state, reporting a failure as a verbose
// note rather than an error: losing the check time only means checking again.
//
// It writes through Update and unions the dismissals, so a dismissal recorded by
// a concurrent launcher after this process read the state survives. LastChecked
// is set outright: this run's check is the most recent.
func persistBestEffort(store Store, st LauncherState, ui termio.UI) {
	err := store.Update(func(current *LauncherState) {
		current.LastChecked = st.LastChecked
		for _, version := range st.DismissedVersions {
			if !slices.Contains(current.DismissedVersions, version) {
				current.DismissedVersions = append(current.DismissedVersions, version)
			}
		}
	})
	if err != nil {
		ui.Verbosef("could not persist upgrade state: %v", err)
	}
}

// isNewer reports whether current is strictly older than latest, comparing
// them as semantic versions.
func isNewer(current, latest string) (bool, error) {
	cv, err := semver.NewVersion(strings.TrimPrefix(current, "v"))
	if err != nil {
		return false, fmt.Errorf("parse current version %q: %w", current, err)
	}
	lv, err := semver.NewVersion(strings.TrimPrefix(latest, "v"))
	if err != nil {
		return false, fmt.Errorf("parse latest version %q: %w", latest, err)
	}
	return cv.LessThan(lv), nil
}

// isParseable reports whether s is a semantic version, with or without a
// leading "v".
func isParseable(s string) bool {
	_, err := semver.NewVersion(strings.TrimPrefix(s, "v"))
	return err == nil
}

// newerThan reports whether latest should be offered over current. With
// allowAnyVersion an unparseable current version (empty, "dev", or garbage)
// counts as older than any parseable release. That is the undocumented dev
// lever: it is what lets a locally built binary exercise the real check and
// install instead of being skipped.
func newerThan(current, latest string, allowAnyVersion bool) (bool, error) {
	if allowAnyVersion && !isParseable(current) {
		if _, err := semver.NewVersion(strings.TrimPrefix(latest, "v")); err != nil {
			return false, fmt.Errorf("parse latest version %q: %w", latest, err)
		}
		return true, nil
	}
	return isNewer(current, latest)
}

// applyMode handles a detected newer release according to the configured mode
// and returns the resulting action. It may mutate st to record a dismissal.
func applyMode(
	ctx context.Context,
	opts Options,
	updateFunc func(context.Context, string) error,
	latest string,
	st *LauncherState,
) Result {
	res := Result{HasUpdate: true, Latest: latest}
	install := func() bool {
		if err := updateFunc(ctx, latest); err != nil {
			notify(opts.UI, opts.CurrentVersion, latest)
			return false
		}
		return true
	}
	switch opts.Mode {
	case ModeAuto:
		res.Updated = install()
	case ModeAutoExit:
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
		"A new version of agents-sandbox is available: %s (you have %s). "+
			"Run `agents-sandbox upgrade` to install it, or set upgrade.mode in your config.",
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
		fmt.Sprintf("A new version of agents-sandbox is available: %s (you have %s)", latest, current),
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

// UpgradeOptions configures Upgrade.
//
//exhaustruct:ignore
type UpgradeOptions struct {
	CurrentVersion string
	Channel        Channel

	// AllowAnyVersion is the undocumented dev-build lever, mirroring
	// Options.AllowAnyVersion: it lets a locally built binary install a release.
	AllowAnyVersion bool
}

// Upgrade checks for a newer release than opts.CurrentVersion and installs it
// over the running binary, reporting progress via ui. It is the on-demand
// counterpart to Check's automatic modes. Homebrew-managed installs are directed
// to `brew upgrade` instead of replacing the keg binary out from under
// Homebrew.
func Upgrade(ctx context.Context, ui termio.UI, opts UpgradeOptions) error {
	if !opts.AllowAnyVersion && (opts.CurrentVersion == "" || opts.CurrentVersion == devVersion) {
		return errors.New("cannot upgrade a development build; use the released binary")
	}
	if InstalledViaHomebrew() {
		ui.Infof("agents-sandbox is managed by Homebrew; run `brew update && brew upgrade agents-sandbox` to update it")
		return nil
	}
	latest, err := LatestVersion(ctx, opts.Channel)
	if err != nil {
		return err
	}
	newer, err := newerThan(opts.CurrentVersion, latest, opts.AllowAnyVersion)
	if err != nil {
		return err
	}
	if !newer {
		ui.Infof("agents-sandbox is up to date (%s)", opts.CurrentVersion)
		return nil
	}
	ui.Infof("upgrading agents-sandbox %s -> %s", opts.CurrentVersion, latest)
	if err := Update(ctx, latest); err != nil {
		return err
	}
	ui.Infof("upgraded to %s; restart to use it", latest)
	return nil
}

// LatestVersion returns the newest release version for the channel (leading "v"
// stripped) by querying the GitHub releases API.
var LatestVersion = latestForChannel

func latestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build latest release request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agents-sandbox")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("latest agents-sandbox release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest agents-sandbox release: unexpected status %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode latest agents-sandbox release: %w", err)
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

// Update downloads and installs the release binary for the given version over
// the running executable.
var Update = updateExecutable

// updateExecutable downloads the release binary for the given version and the
// current platform and atomically replaces the running executable with it.
func updateExecutable(ctx context.Context, latest string) error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	return installRelease(ctx, latest, exe)
}

// installRelease downloads the release binary for version and installs it at
// exePath, verifying its SHA-256 against the release's published checksums
// before the replacement. The replacement is unreachable unless verification
// passes, so a tampered, truncated, or missing-metadata download leaves the
// installed binary untouched.
func installRelease(ctx context.Context, version, exePath string) error {
	asset := assetName()
	assetPath, err := downloadAssetToDir(ctx, filepath.Dir(exePath), version)
	if err != nil {
		return err
	}
	defer os.Remove(assetPath)
	if err := verifyAssetChecksum(ctx, version, assetPath, asset); err != nil {
		return fmt.Errorf("refusing to install %s: %w", asset, err)
	}
	return replaceExecutable(assetPath, exePath)
}

// newAssetRequest builds the GET request the updater uses to fetch a release
// asset, carrying the User-Agent the GitHub release endpoints expect. Callers
// address the specific asset; this keeps request construction in one place.
func newAssetRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "agents-sandbox")
	return req, nil
}

// downloadAssetToDir downloads the release binary for the given version and the
// current platform into dir and returns the downloaded file path.
//
// The download is addressed by release tag rather than the "latest" endpoint,
// which GitHub never serves for a prerelease; the tag is "v" + version by this
// repository's tagging convention (every release tag is v<version>).
func downloadAssetToDir(ctx context.Context, dir string, version string) (string, error) {
	asset := assetName()
	url := fmt.Sprintf("%s/v%s/%s", downloadBase, version, asset)
	req, err := newAssetRequest(ctx, url)
	if err != nil {
		return "", fmt.Errorf("build download request: %w", err)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: unexpected status %d", asset, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, ".agents-sandbox-update-*")
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

// replaceExecutable makes assetPath carry the target's permissions and renames
// it over exePath. The caller must place assetPath in exePath's directory so
// the rename is on the same filesystem and therefore atomic. Preserving the
// old binary's permissions honors installs that used a non-default mode
// (e.g., a setuid or group-writable binary).
func replaceExecutable(assetPath, exePath string) error {
	mode := os.FileMode(0o755)
	if info, err := os.Stat(exePath); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(assetPath, mode); err != nil {
		return fmt.Errorf("make downloaded binary executable: %w", err)
	}
	if err := os.Rename(assetPath, exePath); err != nil {
		return fmt.Errorf("replace running executable: %w", err)
	}
	return nil
}
