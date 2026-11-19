package service

// Component updates: version detection and command-line upgrade for the
// binaries that accompany the desktop app (cinder-mcp, cinder-capture,
// cinder-server) and the two IDE plugins.
//
// Why this exists. CheckUpdate only answers for the running desktop build.
// A user who reinstalls the app stays on an old MCP server — the same release
// ships every component but each has its own installation channel, and the
// app never said a word about any of them. This file reads each installed
// binary's embedded Go build info (no execution, no shell, no trust in a
// --version flag that older builds do not have), compares it with the latest
// release, and produces the exact command that fits HOW the component was
// installed. The command is returned as text and can be copied; applying it
// is an explicit, separate action.

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"repotory/internal/db"
)

// Component IDs are the stable keys the frontend switches on. Adding one
// means a row in the Updates tab; renaming one breaks that contract.
const (
	ComponentApp       = "app"
	ComponentMCP       = "mcp"
	ComponentCapture   = "capture"
	ComponentServer    = "server"
	ComponentVSCode    = "vscode"
	ComponentJetBrains = "jetbrains"
)

// Install methods decide the upgrade command. "unknown" means the binary is
// on PATH but no package manager owns it; "missing" means it was not found.
const (
	InstallBrewCask    = "brew-cask"
	InstallBrewFormula = "brew-formula"
	InstallScoop       = "scoop"
	InstallScript      = "script"
	InstallUnknown     = "unknown"
	InstallMissing     = "missing"
)

// ComponentUpdate is one row of the component update table.
type ComponentUpdate struct {
	ID string `json:"id"`
	// Name is the human-facing component name.
	Name string `json:"name"`
	// Installed is the parsed release version. Empty when the binary does not
	// carry one (a locally built binary has no release stamp).
	Installed string `json:"installed"`
	// BuildTag describes an unstamped build, e.g. "dev (ed833a8)". Empty for
	// release builds, which have an exact Installed version.
	BuildTag string `json:"build_tag"`
	// Latest is the newest published release version, shared by every row.
	Latest string `json:"latest"`
	// Outdated is true for an older release stamp and for any unstamped build:
	// every release binary is stamped by CI, so "no stamp" cannot be current.
	Outdated bool `json:"outdated"`
	// Missing is true when the component is not installed at all.
	Missing bool `json:"missing"`
	// Path is the resolved executable/plugin path, or "" when missing.
	Path string `json:"path"`
	// Method is one of the Install* constants.
	Method string `json:"method"`
	// Command is the exact command-line upgrade for this install method.
	// Empty when there is no command (an IDE plugin installed by hand).
	Command string `json:"command"`
	// Hint explains a missing command or an unstamped build in one line.
	Hint string `json:"hint"`
}

// versionStampRE pulls the release version out of the -ldflags build setting
// CI writes into every shipped Go binary.
var versionStampRE = regexp.MustCompile(`-X\s+repotory/internal/version\.Version=([^\s]+)`)

// shortRevisionRE keeps a VCS revision to its human-comparable prefix.
var shortRevisionRE = regexp.MustCompile(`^[0-9a-f]{7,40}`)

// binaryReleaseVersion reads a Go binary's build info without running it.
// It returns (releaseVersion, buildTag):
//   - a CI build has -ldflags "-X repotory/internal/version.Version=X.Y.Z"
//   - a local build has a vcs.revision but no release stamp
//
// A non-Go binary, a missing file or an unreadable file yields two empty
// strings. That is "unknown", never "up to date".
func binaryReleaseVersion(path string) (string, string) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, st := range info.Settings {
		if st.Key != "-ldflags" {
			continue
		}
		if m := versionStampRE.FindStringSubmatch(st.Value); len(m) == 2 {
			return strings.TrimPrefix(m[1], "v"), ""
		}
	}
	for _, st := range info.Settings {
		if st.Key == "vcs.revision" {
			if m := shortRevisionRE.FindString(st.Value); m != "" {
				return "", "dev (" + m[:7] + ")"
			}
		}
	}
	return "", ""
}

// componentRun is the command runner used by install-method probes and by
// componentCmd. A var so tests never touch the user's Homebrew or Scoop.
var componentRun = func(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// componentLookPath is the PATH resolver, swappable in tests.
var componentLookPath = exec.LookPath

// brewPrefix returns Homebrew's installation prefix once per check. An empty
// answer is normal on machines without Homebrew.
func brewPrefix() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := componentRun(ctx, "brew", "--prefix")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// detectBinaryMethod classifies how one installed binary got onto this
// machine. Order matters: an explicit package-manager listing beats a path
// guess, because copying a binary into /opt/homebrew/bin does not make it a
// cask — and confusing the two generates a brew upgrade that cannot work.
func detectBinaryMethod(path, brew, token string) string {
	if brew != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if _, err := componentRun(ctx, "brew", "list", "--cask", token); err == nil {
			cancel()
			return InstallBrewCask
		}
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		if _, err := componentRun(ctx, "brew", "list", token); err == nil {
			cancel()
			return InstallBrewFormula
		}
		cancel()
	}
	p := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(p, "/scoop/shims/") {
		return InstallScoop
	}
	if brew != "" && strings.HasPrefix(p, strings.ToLower(filepath.ToSlash(brew))+"/bin/") {
		return InstallUnknown // in the brew prefix but not owned by a cask/formula
	}
	if p != "" {
		return InstallScript
	}
	return InstallMissing
}

// componentBinary defines the search names and package-manager token for one
// CLI component. The old and new binary names are both searched: existing
// client configs point at repotory-*, while releases ship cinder-*.
type componentBinary struct {
	ID    string
	Name  string
	Names []string
	Token string
	Hint  string
}

var componentBinaries = []componentBinary{
	{ID: ComponentMCP, Name: "MCP Server", Names: []string{"cinder-mcp", "repotory-mcp"}, Token: "repotory-mcp", Hint: "IDE 客户端仍指向旧文件名时，升级后软链会自动重建"},
	{ID: ComponentCapture, Name: "Capture CLI", Names: []string{"cinder-capture", "repotory-capture"}, Token: "repotory-capture", Hint: "会话结束钩子使用旧文件名，升级后软链会自动重建"},
	{ID: ComponentServer, Name: "Headless Server", Names: []string{"cinder-server", "repotory-server"}, Token: "", Hint: "该组件没有独立安装包，脚本安装会一并更新"},
}

// componentUpdateLocal derives one row's path, method and command from the
// local machine only. RunComponentUpdate uses it so an upgrade never depends
// on reaching GitHub — the very network failure that sends users here.
func componentUpdateLocal(id string) ComponentUpdate {
	if componentDisplayName(id) == id {
		// Unknown ids are not components. Return no command so the caller
		// fails closed instead of running a default upgrade.
		return ComponentUpdate{ID: id, Method: InstallMissing}
	}
	row := ComponentUpdate{ID: id, Name: componentDisplayName(id), Method: InstallMissing}
	switch id {
	case ComponentApp:
		row.Path = appBundlePath(runningExecutable())
		row.Method = detectBinaryMethod(row.Path, brewPrefix(), "repotory")
	case ComponentMCP, ComponentCapture, ComponentServer:
		spec := componentSpec(id)
		for _, name := range spec.Names {
			if p, err := componentLookPath(name); err == nil && p != "" {
				row.Path = p
				break
			}
		}
		if row.Path != "" {
			row.Method = detectBinaryMethod(row.Path, brewPrefix(), spec.Token)
		}
	case ComponentVSCode, ComponentJetBrains:
		row = ideComponentStatus(id, componentDisplayName(id), "")
	}
	row.Command = componentUpgradeCommand(id, row.Method, "")
	return row
}

func componentDisplayName(id string) string {
	switch id {
	case ComponentApp:
		return "Cinder Desktop"
	case ComponentMCP:
		return "MCP Server"
	case ComponentCapture:
		return "Capture CLI"
	case ComponentServer:
		return "Headless Server"
	case ComponentVSCode:
		return "VS Code Plugin"
	case ComponentJetBrains:
		return "JetBrains Plugin"
	}
	return id
}

func componentSpec(id string) componentBinary {
	for _, c := range componentBinaries {
		if c.ID == id {
			return c
		}
	}
	return componentBinary{}
}

// CheckComponentUpdates reports the installed version and upgrade command for
// every shipping component. The latest release is read through CheckUpdate so
// there is one GitHub call and one proxy configuration for the whole table.
func (s *Service) CheckComponentUpdates() ([]ComponentUpdate, error) {
	release, err := s.CheckUpdate()
	if err != nil {
		return nil, err
	}
	latest := release.Latest
	brew := brewPrefix()
	out := make([]ComponentUpdate, 0, 6)

	appMethod := detectBinaryMethod(appBundlePath(runningExecutable()), brew, "repotory")
	out = append(out, ComponentUpdate{
		ID: ComponentApp, Name: "Cinder Desktop", Installed: release.Current,
		Latest: latest, Outdated: release.HasUpdate, Method: appMethod,
		Command: componentUpgradeCommand(ComponentApp, appMethod, latest),
		Hint:    "桌面应用也可用设置里的下载按钮；脚本命令会一并更新全部组件",
	})

	for _, c := range componentBinaries {
		path := ""
		for _, name := range c.Names {
			if p, err := componentLookPath(name); err == nil && p != "" {
				path = p
				break
			}
		}
		row := ComponentUpdate{ID: c.ID, Name: c.Name, Latest: latest, Path: path, Hint: c.Hint}
		if path == "" {
			row.Missing = true
			row.Method = InstallMissing
			row.Command = installScriptCommand()
			out = append(out, row)
			continue
		}
		installed, tag := binaryReleaseVersion(path)
		row.Installed, row.BuildTag = installed, tag
		switch {
		case installed != "":
			outdated, err := isNewerVersion(installed, latest)
			row.Outdated = err == nil && outdated
		case tag != "":
			row.Outdated = true // release binaries are always stamped
		default:
			row.Hint = "无法从二进制读取版本（非 Go 构建或损坏），建议重装"
		}
		row.Method = detectBinaryMethod(path, brew, c.Token)
		row.Command = componentUpgradeCommand(c.ID, row.Method, latest)
		out = append(out, row)
	}

	out = append(out, ideComponentStatus(ComponentVSCode, "VS Code Plugin", latest),
		ideComponentStatus(ComponentJetBrains, "JetBrains Plugin", latest))
	return out, nil
}

// installScriptCommand is the all-in-one channel. It is already the command
// in the README, so offering it here teaches no new ritual.
func installScriptCommand() string {
	return "curl -fsSL https://raw.githubusercontent.com/sky-jiangcheng/cinder/master/scripts/install.sh | bash"
}

// componentUpgradeCommand maps an install method to one exact command.
// The id is part of the allowlist: a caller cannot smuggle an arbitrary
// command into the "run" binding by changing id or method.
func componentUpgradeCommand(id, method, latest string) string {
	switch id {
	case ComponentApp:
		switch method {
		case InstallBrewCask:
			return "brew upgrade --cask repotory"
		case InstallScoop:
			return "scoop update repotory"
		default:
			return installScriptCommand()
		}
	case ComponentMCP:
		switch method {
		case InstallBrewCask:
			return "brew upgrade --cask repotory-mcp"
		case InstallBrewFormula:
			return "brew upgrade repotory-mcp"
		case InstallScoop:
			return "scoop update repotory-mcp"
		default:
			return installScriptCommand()
		}
	case ComponentCapture:
		switch method {
		case InstallBrewCask:
			return "brew upgrade --cask repotory-capture"
		case InstallBrewFormula:
			return "brew upgrade repotory-capture"
		case InstallScoop:
			return "scoop update repotory-capture"
		default:
			return installScriptCommand()
		}
	case ComponentServer:
		return installScriptCommand()
	case ComponentVSCode:
		return "code --install-extension sky-jiangcheng.repotory-vscode --force"
	case ComponentJetBrains:
		return "" // the marketplace has no CLI upgrade; the release page has the zip
	}
	return ""
}

// ideComponentStatus finds the installed IDE plugin version from its own
// metadata: package.json for VS Code, the plugin jar name for JetBrains.
func ideComponentStatus(id, name, latest string) ComponentUpdate {
	row := ComponentUpdate{ID: id, Name: name, Latest: latest, Method: InstallMissing,
		Command: componentUpgradeCommand(id, InstallScript, latest)}
	home, err := os.UserHomeDir()
	if err != nil {
		row.Missing = true
		return row
	}
	var matches []string
	switch id {
	case ComponentVSCode:
		for _, base := range []string{
			filepath.Join(home, ".vscode", "extensions"),
			filepath.Join(home, ".vscode-server", "extensions"),
			filepath.Join(home, ".vscode-oss", "extensions"),
		} {
			hits, _ := filepath.Glob(filepath.Join(base, "*repotory-vscode-*", "package.json"))
			matches = append(matches, hits...)
		}
		if len(matches) > 0 {
			row.Path = matches[0]
			row.Installed = packageJSONVersion(matches[0])
			row.Method = InstallScript
		}
	case ComponentJetBrains:
		for _, base := range jetBrainsPluginRoots(home) {
			hits, _ := filepath.Glob(filepath.Join(base, "*", "plugins", "lib", "cinder-jetbrains-*.jar"))
			if len(hits) == 0 {
				hits, _ = filepath.Glob(filepath.Join(base, "*", "plugins", "lib", "repotory-jetbrains-*.jar"))
			}
			matches = append(matches, hits...)
		}
		if len(matches) > 0 {
			row.Path = matches[0]
			row.Installed = jarVersion(filepath.Base(matches[0]))
			row.Method = InstallScript
			row.Command = "" // no CLI channel; point the user at the release page
			row.Hint = "JetBrains 插件请在 IDE 设置中升级，或从 Release 下载 zip 手动替换"
		}
	}
	row.Missing = row.Path == ""
	if row.Installed != "" {
		if outdated, err := isNewerVersion(row.Installed, latest); err == nil {
			row.Outdated = outdated
		}
	} else if !row.Missing {
		row.Hint = "无法读取插件版本，建议升级"
	}
	return row
}

// jetBrainsPluginRoots covers the OS-specific IDE config locations.
func jetBrainsPluginRoots(home string) []string {
	if runtime.GOOS == "darwin" {
		return []string{filepath.Join(home, "Library", "Application Support", "JetBrains")}
	}
	if runtime.GOOS == "windows" {
		return []string{filepath.Join(os.Getenv("APPDATA"), "JetBrains")}
	}
	return []string{filepath.Join(home, ".config", "JetBrains")}
}

var pluginVersionRE = regexp.MustCompile(`(?m)"version"\s*:\s*"([0-9]+\.[0-9]+\.[0-9]+[^"]*)"`)

func packageJSONVersion(path string) string {
	buf, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := pluginVersionRE.FindSubmatch(buf); m != nil {
		return string(m[1])
	}
	return ""
}

var jarVersionRE = regexp.MustCompile(`(?:cinder|repotory)-jetbrains-([0-9]+\.[0-9]+\.[0-9]+[^/]*)\.jar`)

func jarVersion(name string) string {
	if m := jarVersionRE.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
}

// ComponentUpdateResult is the outcome of running one generated command.
type ComponentUpdateResult struct {
	Command string `json:"command"`
	Output  string `json:"output"`
}

// RunComponentUpdate executes the generated command for one component after
// re-deriving it from the CURRENT install state: the frontend never supplies
// a shell string. The configured update proxy is injected because a GUI app
// started from Finder has no HTTP_PROXY in its environment — the same reason
// update_proxy exists for in-app downloads.
func (s *Service) RunComponentUpdate(id string) (*ComponentUpdateResult, error) {
	// Re-derived locally, never from the frontend. No GitHub call: a user
	// behind the GFW must still be able to run the upgrade they just copied.
	row := componentUpdateLocal(id)
	cmd := row.Command
	if cmd == "" {
		return nil, fmt.Errorf("组件 %s 没有命令行升级方式", id)
	}
	// Only commands this package generated may run. Re-derive every permitted
	// string from componentUpgradeCommand, so a future change to one method's
	// command cannot leave the allowlist silently behind.
	allowed := map[string]bool{}
	for _, cid := range []string{ComponentApp, ComponentMCP, ComponentCapture, ComponentServer, ComponentVSCode, ComponentJetBrains} {
		for _, method := range []string{
			InstallBrewCask, InstallBrewFormula, InstallScoop, InstallScript, InstallUnknown, InstallMissing,
		} {
			if c := componentUpgradeCommand(cid, method, ""); c != "" {
				allowed[c] = true
			}
		}
	}
	if !allowed[cmd] {
		return nil, fmt.Errorf("拒绝执行未授权的升级命令")
	}

	shell, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/c"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmdObj := exec.CommandContext(ctx, shell, flag, cmd)
	proxy, _ := db.GetConfig(s.db, "update_proxy")
	if p := strings.TrimSpace(proxy); p != "" {
		cmdObj.Env = append(os.Environ(), "HTTP_PROXY="+p, "HTTPS_PROXY="+p, "http_proxy="+p, "https_proxy="+p)
	}
	out, err := cmdObj.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return &ComponentUpdateResult{Command: cmd, Output: text}, fmt.Errorf("升级失败：%v%s", err, withOutput(text))
	}
	return &ComponentUpdateResult{Command: cmd, Output: text}, nil
}

func withOutput(text string) string {
	if text == "" {
		return ""
	}
	return "（" + truncateRunes(text, 300) + "）"
}
