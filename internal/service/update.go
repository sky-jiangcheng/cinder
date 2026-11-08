package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/version"
)

// Update check: asks GitHub Releases for the newest published version and
// compares it against the running build. Deliberately NOT an auto-updater:
// the check is advisory, the download is one click into the system browser
// (or the user's package manager), and nothing on this path can write to
// the installation. A desktop app that silently replaces its own binary is
// a much bigger trust and failure surface than a button that opens a
// release page — and on macOS/Windows the running binary cannot replace
// itself anyway.
//
// Network reality: api.github.com is unreachable or slow behind the GFW,
// which most of this app's users sit behind. So the call carries a short
// timeout, an optional per-user proxy (Settings → Updates), and a failure
// here is a message in the settings tab, never a startup blocker.

// updateCheckTimeout bounds one Releases API call. Short on purpose: a user
// who just clicked "check" should get an answer or an error, not a spinner.
const updateCheckTimeout = 10 * time.Second

// GitHub's API rejects anonymous requests without a User-Agent.
const updateCheckUserAgent = "repotory-app"

// updateNotesMaxLen caps the release-notes body rendered in the settings
// tab. Release notes are written for the release page, not for a card;
// past this the tab would scroll for screens.
const updateNotesMaxLen = 2000

// releasesLatestURL is the GitHub Releases "latest" endpoint. `latest`
// excludes drafts and prereleases by definition, which is exactly the
// channel stable builds track. A var, not a const, so tests can point it
// at a stub server — a const would make every test hit the real API.
var releasesLatestURL = "https://api.github.com/repos/sky-jiangcheng/cinder/releases/latest"

// UpdateInfo is the verdict of one update check.
type UpdateInfo struct {
	// Current is the running build (version.Version, overridden by CI ldflags).
	Current string `json:"current"`
	// Latest is the newest published tag ("v" prefix stripped).
	Latest string `json:"latest"`
	// HasUpdate is true when Latest is a higher semver than Current.
	HasUpdate bool `json:"has_update"`
	// ReleaseURL is the human-facing release page (the download button target).
	ReleaseURL string `json:"release_url"`
	// PublishedAt is the release timestamp, RFC 3339 as GitHub reports it.
	PublishedAt string `json:"published_at"`
	// Notes is the release body, truncated to updateNotesMaxLen.
	Notes string `json:"notes"`
	// Assets are the release's app bundles, in GitHub's order. The MCP server
	// and IDE plugin archives are excluded — they are not "the app", and
	// offering them as the download target would be a lie.
	Assets []UpdateAsset `json:"assets"`
	// DownloadURL / DownloadName point at the bundle matching the running
	// platform. Empty when the release ships no bundle for this platform
	// (e.g. linux/arm64, which CI does not build) — the UI then falls back to
	// the release page.
	DownloadURL  string `json:"download_url"`
	DownloadName string `json:"download_name"`
	// DownloadSHA256 is the digest GitHub's release API publishes for the
	// bundle above ("sha256:<hex>"). The in-app download verifies the
	// received bytes against it. Empty when the release published no digest
	// for that asset.
	DownloadSHA256 string `json:"download_sha256"`
	// DownloadSize is the bundle's size in bytes, 0 when unknown.
	DownloadSize int64 `json:"download_size"`
}

// UpdateAsset is one downloadable file attached to a GitHub release.
type UpdateAsset struct {
	// Name is the file name, e.g. "repotory-darwin-arm64.dmg".
	Name string `json:"name"`
	// URL is the direct download URL (browser_download_url).
	URL string `json:"url"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Digest is the digest the release API publishes for this asset, in
	// "sha256:<hex>" form. Empty when the release carries none.
	Digest string `json:"digest"`
}

// newUpdateHTTPClient builds the client for one check. A configured proxy
// (update_proxy) overrides the default transport, which reads the process
// environment — and a GUI app launched from Finder/Dock inherits no
// HTTP_PROXY, which is why the proxy is a setting and not just env lore.
// Swappable in tests.
var newUpdateHTTPClient = func(proxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("更新代理配置无效：%s（形如 http://127.0.0.1:7890）", proxy)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: updateCheckTimeout, Transport: transport}, nil
}

// validateUpdateProxy checks the configured proxy without building a client.
// The download path needs the same check as the check path (a typo in the
// proxy field must fail the same way in both), but it must not build a
// client it will not use.
func validateUpdateProxy(proxy string) error {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return nil
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Host == "" {
		return fmt.Errorf("更新代理配置无效：%s（形如 http://127.0.0.1:7890）", proxy)
	}
	return nil
}

// CheckUpdate queries GitHub Releases and reports whether a newer stable
// version exists. A network failure is returned as an error so the caller
// can say so plainly — silently reporting "up to date" on a failed request
// would teach users the button is broken.
func (s *Service) CheckUpdate() (*UpdateInfo, error) {
	proxy, _ := db.GetConfig(s.db, "update_proxy")
	client, err := newUpdateHTTPClient(strings.TrimSpace(proxy))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesLatestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build update request: %w", err)
	}
	req.Header.Set("User-Agent", updateCheckUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败：无法连接 GitHub（%v）——如在内地网络，可在下方配置代理", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("检查更新失败：GitHub 返回 HTTP %d", resp.StatusCode)
	}

	var release struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
		Prerelease  bool   `json:"prerelease"`
		Assets      []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("检查更新失败：无法解析 GitHub 响应（%v）", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, fmt.Errorf("检查更新失败：GitHub 未返回版本号")
	}

	newer, err := isNewerVersion(version.Version, release.TagName)
	if err != nil {
		return nil, err
	}
	assets := collectAppAssets(release.Assets)
	platformAsset := platformAsset(assets)
	return &UpdateInfo{
		Current:        version.Version,
		Latest:         strings.TrimPrefix(strings.TrimSpace(release.TagName), "v"),
		HasUpdate:      newer,
		ReleaseURL:     release.HTMLURL,
		PublishedAt:    release.PublishedAt,
		Notes:          truncateRunes(release.Body, updateNotesMaxLen),
		Assets:         assets,
		DownloadURL:    platformAsset.URL,
		DownloadName:   platformAsset.Name,
		DownloadSHA256: platformAsset.Digest,
		DownloadSize:   platformAsset.Size,
	}, nil
}

// appAssetPattern matches the desktop app's own release bundles: the file
// name CI produces for each target. The name carries the brand AND the
// version, e.g. "Cinder-2.33.0-darwin-arm64.dmg" or
// "Cinder-2.33.0-windows-amd64.zip".
//
// It is deliberately an exact-shape pattern rather than a substring search.
// The same release also carries cinder-mcp-*.tar.gz, cinder-server-*, the
// VSIX and the JetBrains plugin zip — all of which contain "cinder" and a
// platform token, so a looser match would offer the user an MCP server
// archive as "the new version of the app".
var appAssetPattern = regexp.MustCompile(`^Cinder-(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)-(darwin|windows|linux)-(amd64|arm64)\.(dmg|zip|tar\.gz|AppImage|deb|msi)$`)

// legacyAppAssetPattern is the pre-rename shape ("repotory-darwin-arm64.dmg").
// It stays matched so an older build of this app — whose updater only knows
// this pattern — is not the only reader that has to be considered, and so a
// download of a pre-rename release still resolves to its bundle.
var legacyAppAssetPattern = regexp.MustCompile(`^repotory-(darwin|windows|linux)-(amd64|arm64)\.(dmg|zip|tar\.gz|AppImage|deb|msi)$`)

// isAppAsset reports whether name is one of the app's own bundles, in either
// naming era.
func isAppAsset(name string) bool {
	return appAssetPattern.MatchString(name) || legacyAppAssetPattern.MatchString(name)
}

// collectAppAssets keeps only the desktop app bundles out of the release's
// asset list.
func collectAppAssets(raw []struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}) []UpdateAsset {
	out := make([]UpdateAsset, 0, len(raw))
	for _, a := range raw {
		if !isAppAsset(a.Name) {
			continue
		}
		out = append(out, UpdateAsset{Name: a.Name, URL: a.URL, Size: a.Size, Digest: a.Digest})
	}
	return out
}

// platformAsset returns the bundle built for the running platform, or the
// zero value when the release ships none (e.g. linux/arm64, which CI does
// not build). The UI then falls back to the release page.
func platformAsset(assets []UpdateAsset) UpdateAsset {
	// The version sits between the brand and the platform token now, so the
	// platform is matched inside the name rather than as a prefix.
	want := "-" + runtime.GOOS + "-" + runtime.GOARCH + "."
	for _, a := range assets {
		if strings.Contains(a.Name, want) {
			return a
		}
	}
	return UpdateAsset{}
}

// UpdateCheckEnabled reports whether the startup background check may run.
// Default ON (absent row): the check is one cheap anonymous GET behind a
// short timeout, and a user who never opened the settings tab still gets
// update awareness. "0" opts out explicitly.
func (s *Service) UpdateCheckEnabled() bool {
	v, _ := db.GetConfig(s.db, "update_check_enabled")
	return v != "0"
}

// isNewerVersion compares two semver strings ("v" prefix optional, build
// metadata and prerelease suffixes ignored — this project ships neither).
// It errors on unparseable input rather than guessing: a wrong verdict here
// either nags users about a version they already run or hides a real one.
func isNewerVersion(current, latest string) (bool, error) {
	cur, ok := parseSemver(current)
	if !ok {
		return false, fmt.Errorf("当前版本号无法解析：%s", current)
	}
	new_, ok := parseSemver(latest)
	if !ok {
		return false, fmt.Errorf("GitHub 版本号无法解析：%s", latest)
	}
	for i := 0; i < 3; i++ {
		if new_[i] > cur[i] {
			return true, nil
		}
		if new_[i] < cur[i] {
			return false, nil
		}
	}
	return false, nil
}

// parseSemver parses "MAJOR.MINOR.PATCH" with an optional leading "v" and
// ignores anything from "-" (prerelease) or "+" (build metadata) onward.
func parseSemver(tag string) ([3]int, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// truncateRunes cuts s to max runes (not bytes: release notes carry CJK and
// emoji, and a byte cut can split a character into mojibake) and marks the
// cut with an ellipsis so the card does not look like it simply ended.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
