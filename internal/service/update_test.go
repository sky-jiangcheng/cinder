package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"repotory/internal/version"
)

// stubUpdateClient points the Releases URL and the HTTP client at a test
// server (or a failing one) and restores both afterwards. Both must move:
// the URL is a package var so the request lands on the stub, and the client
// factory is swapped so no real network is touched.
func stubUpdateClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	prevURL := releasesLatestURL
	prevClient := newUpdateHTTPClient
	releasesLatestURL = server.URL
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) {
		if proxy != "" {
			t.Errorf("unexpected proxy %q in stubbed client", proxy)
		}
		return server.Client(), nil
	}
	t.Cleanup(func() {
		releasesLatestURL = prevURL
		newUpdateHTTPClient = prevClient
	})
}

func releaseHandler(t *testing.T, tag, htmlURL, body, publishedAt string, prerelease bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("GitHub API requires a User-Agent; request had none")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     tag,
			"html_url":     htmlURL,
			"body":         body,
			"published_at": publishedAt,
			"prerelease":   prerelease,
		})
	}
}

func TestCheckUpdateFindsNewerVersion(t *testing.T) {
	srv := httptest.NewServer(releaseHandler(t,
		"v9.9.9", "https://github.com/sky-jiangcheng/cinder/releases/tag/v9.9.9",
		"- shiny things", "2026-10-24T10:00:00Z", false))
	defer srv.Close()
	stubUpdateClient(t, srv)

	svc, _ := setupService(t)
	info, err := svc.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if info.Current != version.Version {
		t.Errorf("Current = %q, want %q", info.Current, version.Version)
	}
	if !info.HasUpdate {
		t.Errorf("HasUpdate = false for latest %q over current %q", info.Latest, info.Current)
	}
	if info.Latest != "9.9.9" {
		t.Errorf("Latest = %q, want 9.9.9 (v prefix stripped)", info.Latest)
	}
	if info.ReleaseURL == "" || info.PublishedAt == "" {
		t.Errorf("ReleaseURL/PublishedAt not carried through: %+v", info)
	}
}

func TestCheckUpdateUpToDate(t *testing.T) {
	// The running version served back as latest: same numbers, no nag.
	srv := httptest.NewServer(releaseHandler(t, "v"+version.Version, "https://example.invalid/release", "", "2026-10-24T10:00:00Z", false))
	defer srv.Close()
	stubUpdateClient(t, srv)

	svc, _ := setupService(t)
	info, err := svc.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if info.HasUpdate {
		t.Errorf("HasUpdate = true when latest equals current %q", version.Version)
	}
}

func TestCheckUpdateOlderReleaseIsNotAnUpdate(t *testing.T) {
	srv := httptest.NewServer(releaseHandler(t, "v1.0.0", "https://example.invalid/release", "", "", false))
	defer srv.Close()
	stubUpdateClient(t, srv)

	svc, _ := setupService(t)
	info, err := svc.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if info.HasUpdate {
		t.Errorf("HasUpdate = true for latest %q BELOW current %q", info.Latest, version.Version)
	}
}

func TestCheckUpdateNetworkFailureIsAnError(t *testing.T) {
	// A dead server must produce an error, never a silent "up to date":
	// a check that cannot reach the network has no opinion about versions.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	stubUpdateClient(t, srv)

	svc, _ := setupService(t)
	if _, err := svc.CheckUpdate(); err == nil {
		t.Fatal("CheckUpdate succeeded on HTTP 500, want error")
	}
}

func TestCheckUpdateProxyIsValidated(t *testing.T) {
	svc, _ := setupService(t)
	if err := svc.UpdateConfig("update_proxy", "://not-a-url"); err != nil {
		t.Fatalf("store proxy value: %v", err)
	}
	_, err := svc.CheckUpdate()
	if err == nil || !strings.Contains(err.Error(), "代理") {
		t.Fatalf("CheckUpdate with invalid proxy: err = %v, want a proxy complaint", err)
	}
}

func TestCheckUpdateNotesTruncatedByRunes(t *testing.T) {
	// Multi-byte body: the cut must land on a rune boundary, and the card
	// must show it was cut rather than looking like the notes just ended.
	long := strings.Repeat("界面", 3000) // 6000 runes, 18000 bytes
	srv := httptest.NewServer(releaseHandler(t, "v9.9.9", "https://example.invalid/release", long, "", false))
	defer srv.Close()
	stubUpdateClient(t, srv)

	svc, _ := setupService(t)
	info, err := svc.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	runes := []rune(info.Notes)
	if len(runes) != updateNotesMaxLen+1 { // body + ellipsis
		t.Errorf("notes len = %d runes, want %d", len(runes), updateNotesMaxLen+1)
	}
	if !strings.HasSuffix(info.Notes, "…") {
		t.Error("truncated notes lack the ellipsis marker")
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
		wantErr         bool
	}{
		{"2.12.3", "2.12.4", true, false},
		{"2.12.3", "v2.12.4", true, false},
		{"2.12.3", "2.13.0", true, false},
		{"2.12.3", "3.0.0", true, false},
		{"2.12.3", "2.12.3", false, false},
		{"2.12.3", "2.12.2", false, false},
		{"2.12.3", "2.11.9", false, false},
		{"2.12.3", "2.12.4-rc.1", true, false}, // prerelease suffix ignored
		{"2.12.3", "2.12.3+build.5", false, false},
		{"2.12.3", "not-a-version", false, true},
		{"garbage", "2.12.4", false, true},
	}
	for _, tt := range tests {
		got, err := isNewerVersion(tt.current, tt.latest)
		if tt.wantErr {
			if err == nil {
				t.Errorf("isNewerVersion(%q, %q) = %v, want error", tt.current, tt.latest, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("isNewerVersion(%q, %q): %v", tt.current, tt.latest, err)
		} else if got != tt.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestUpdateCheckEnabledDefaultsOn(t *testing.T) {
	svc, _ := setupService(t)
	if !svc.UpdateCheckEnabled() {
		t.Error("UpdateCheckEnabled = false on a fresh DB, want default-on")
	}
	if err := svc.UpdateConfig("update_check_enabled", "0"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if svc.UpdateCheckEnabled() {
		t.Error("UpdateCheckEnabled = true after explicit opt-out")
	}
}

func TestCollectAppAssetsKeepsOnlyAppBundles(t *testing.T) {
	// The release carries the app bundles AND the MCP server / headless server
	// archives, the VSIX and the JetBrains plugin. Only the app bundles may be
	// offered as "the new version".
	raw := []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	}{
		{Name: "repotory-darwin-arm64.dmg", URL: "https://example.invalid/dmg", Size: 10, Digest: "sha256:aa"},
		{Name: "repotory-windows-amd64.zip", URL: "https://example.invalid/zip", Size: 20},
		{Name: "repotory-linux-amd64.tar.gz", URL: "https://example.invalid/tgz", Size: 30},
		{Name: "repotory-mcp-darwin-arm64.tar.gz", URL: "https://example.invalid/mcp", Size: 40},
		{Name: "repotory-server-linux-amd64.tar.gz", URL: "https://example.invalid/server", Size: 50},
		{Name: "repotory-vscode-2.12.3.vsix", URL: "https://example.invalid/vsix", Size: 60},
		{Name: "repotory-jetbrains-plugin-2.12.3.zip", URL: "https://example.invalid/jb", Size: 70},
		{Name: "SHA256SUMS", URL: "https://example.invalid/sums", Size: 80},
	}
	assets := collectAppAssets(raw)
	if len(assets) != 3 {
		t.Fatalf("collectAppAssets returned %d assets, want 3: %+v", len(assets), assets)
	}
	wantNames := []string{"repotory-darwin-arm64.dmg", "repotory-windows-amd64.zip", "repotory-linux-amd64.tar.gz"}
	for i, want := range wantNames {
		if assets[i].Name != want {
			t.Errorf("assets[%d].Name = %q, want %q", i, assets[i].Name, want)
		}
	}
	// The digest the release published must survive the filtering — the
	// in-app download verifies the received bytes against it.
	if assets[0].Digest != "sha256:aa" {
		t.Errorf("assets[0].Digest = %q, want %q", assets[0].Digest, "sha256:aa")
	}
}

func TestPlatformAssetMatchesRunningPlatform(t *testing.T) {
	// The running platform's bundle must be the one that carries the size and
	// digest — the in-app download verifies the received bytes against them,
	// so a zero size or an empty digest on the chosen asset would make the
	// verification vacuous.
	//
	// The assets are built from runtime.GOOS / runtime.GOARCH rather than
	// hard-coded: a fixed "darwin-arm64" bundle only exists on a Mac, so the
	// test would look for a bundle the fixture never had on Linux CI and
	// report a size/digest mismatch for a reason that has nothing to do with
	// the code under test.
	mine := "repotory-" + runtime.GOOS + "-" + runtime.GOARCH + ".dmg"
	assets := []UpdateAsset{
		{Name: "repotory-otheros-amd64.dmg", URL: "https://example.invalid/other.dmg"},
		{Name: mine, URL: "https://example.invalid/mine.dmg", Size: 42, Digest: "sha256:bb"},
		{Name: "repotory-thirdos-amd64.dmg", URL: "https://example.invalid/third.dmg"},
	}
	want := "repotory-" + runtime.GOOS + "-" + runtime.GOARCH + "."
	got := platformAsset(assets)
	if !strings.HasPrefix(got.Name, want) {
		t.Errorf("platformAsset name = %q, want a name starting with %q", got.Name, want)
	}
	if got.URL == "" {
		t.Errorf("platformAsset URL = %q for the running platform, want a URL", got.URL)
	}
	if got.Size != 42 || got.Digest != "sha256:bb" {
		t.Errorf("platformAsset must carry the size and digest, got size=%d digest=%q", got.Size, got.Digest)
	}
}

func TestPlatformAssetEmptyWhenReleaseHasNoneForThisPlatform(t *testing.T) {
	// A release with only other platforms' bundles must not hand back a
	// foreign download: the UI falls back to the release page instead.
	assets := []UpdateAsset{
		{Name: "repotory-plan9-amd64.zip", URL: "https://example.invalid/other.zip"},
	}
	if got := platformAsset(assets); got.URL != "" || got.Name != "" {
		t.Errorf("platformAsset = %+v for a release without our platform, want the zero value", got)
	}
}
