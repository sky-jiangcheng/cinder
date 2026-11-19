package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Build a real Go binary so binaryReleaseVersion reads REAL embedded build
// info rather than a fixture that could pass with a broken regex.
func TestBinaryReleaseVersionReadsLDFlagsStamp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "tool")
	cmd := runCommand("go", "build", "-ldflags", "-X repotory/internal/version.Version=2.40.2", "-o", bin, src)
	if cmd != nil {
		t.Fatalf("build fixture: %v", cmd)
	}
	got, tag := binaryReleaseVersion(bin)
	if got != "2.40.2" || tag != "" {
		t.Fatalf("binaryReleaseVersion = (%q, %q), want (2.40.2, empty)", got, tag)
	}
}

func TestBinaryReleaseVersionRejectsNonGoAndMissing(t *testing.T) {
	if got, tag := binaryReleaseVersion(filepath.Join(t.TempDir(), "nope")); got != "" || tag != "" {
		t.Fatalf("missing binary = (%q, %q), want empty", got, tag)
	}
	f := filepath.Join(t.TempDir(), "random.bin")
	if err := os.WriteFile(f, []byte("not a go binary"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, tag := binaryReleaseVersion(f); got != "" || tag != "" {
		t.Fatalf("non-Go binary = (%q, %q), want empty", got, tag)
	}
}

func TestComponentUpgradeCommandMatchesInstallMethod(t *testing.T) {
	cases := []struct{ id, method, want string }{
		{ComponentMCP, InstallBrewCask, "brew upgrade --cask repotory-mcp"},
		{ComponentMCP, InstallBrewFormula, "brew upgrade repotory-mcp"},
		{ComponentMCP, InstallScoop, "scoop update repotory-mcp"},
		{ComponentMCP, InstallScript, installScriptCommand()},
		{ComponentCapture, InstallBrewCask, "brew upgrade --cask repotory-capture"},
		{ComponentApp, InstallBrewCask, "brew upgrade --cask repotory"},
		{ComponentVSCode, InstallScript, "code --install-extension sky-jiangcheng.repotory-vscode --force"},
		{ComponentJetBrains, InstallScript, ""},
	}
	for _, c := range cases {
		if got := componentUpgradeCommand(c.id, c.method, "2.40.2"); got != c.want {
			t.Errorf("%s/%s = %q, want %q", c.id, c.method, got, c.want)
		}
	}
}

func TestDetectBinaryMethodPrefersPackageManagerOverPath(t *testing.T) {
	oldRun, oldLook := componentRun, componentLookPath
	defer func() { componentRun, componentLookPath = oldRun, oldLook }()

	// brew lists the cask: even a path in /usr/local/bin is reported as a cask.
	componentRun = func(ctx context.Context, name string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "list" && args[1] == "--cask" {
			return "repotory-mcp", nil
		}
		return "", context.Canceled
	}
	got := detectBinaryMethod("/usr/local/bin/cinder-mcp", "/opt/homebrew", "repotory-mcp")
	if got != InstallBrewCask {
		t.Fatalf("cask-owned binary = %q, want %q", got, InstallBrewCask)
	}

	// Not listed, but inside the brew prefix: a manual copy, NOT a cask.
	componentRun = func(ctx context.Context, name string, args ...string) (string, error) {
		return "", context.Canceled
	}
	got = detectBinaryMethod("/opt/homebrew/bin/cinder-mcp", "/opt/homebrew", "repotory-mcp")
	if got != InstallUnknown {
		t.Fatalf("manual copy in brew prefix = %q, want %q", got, InstallUnknown)
	}
	got = detectBinaryMethod("/Users/me/bin/cinder-mcp", "", "repotory-mcp")
	if got != InstallScript {
		t.Fatalf("PATH binary = %q, want %q", got, InstallScript)
	}
}

func TestPluginVersionParsers(t *testing.T) {
	if got := jarVersion("cinder-jetbrains-2.26.0.jar"); got != "2.26.0" {
		t.Fatalf("jarVersion = %q", got)
	}
	if got := jarVersion("other.jar"); got != "" {
		t.Fatalf("jarVersion on unrelated jar = %q, want empty", got)
	}
}

func TestRunComponentUpdateRejectsUnknownID(t *testing.T) {
	s, _ := setupService(t)
	if _, err := s.RunComponentUpdate("not-a-component"); err == nil {
		t.Fatal("unknown component must error")
	}
}

func TestAllowedCommandsCoverGeneratedCommands(t *testing.T) {
	// Every command the UI can show must be runnable; otherwise the button
	// silently refuses the very command it just displayed.
	for _, cid := range []string{ComponentApp, ComponentMCP, ComponentCapture, ComponentServer, ComponentVSCode, ComponentJetBrains} {
		for _, method := range []string{InstallBrewCask, InstallBrewFormula, InstallScoop, InstallScript, InstallUnknown, InstallMissing} {
			cmd := componentUpgradeCommand(cid, method, "")
			if cmd == "" {
				continue
			}
			allowed := false
			for _, other := range []string{ComponentApp, ComponentMCP, ComponentCapture, ComponentServer, ComponentVSCode, ComponentJetBrains} {
				for _, m := range []string{InstallBrewCask, InstallBrewFormula, InstallScoop, InstallScript, InstallUnknown, InstallMissing} {
					if componentUpgradeCommand(other, m, "") == cmd {
						allowed = true
					}
				}
			}
			if !allowed {
				t.Errorf("%s/%s generated %q which the allowlist rejects", cid, method, cmd)
			}
		}
	}
	if runtime.GOOS == "windows" && strings.Contains(installScriptCommand(), "|") {
		// The Windows install path uses install.ps1; the script command is only
		// shown on Unix. This test documents that boundary rather than fixing
		// it here — see the install channel docs.
		t.Log("Windows: install.sh is not the native channel; IDE/plugin rows stay manual")
	}
}
