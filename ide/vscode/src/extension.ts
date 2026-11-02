// Repotory for VS Code — thin client over Repotory (ADR-0009 step two / TODO M5).
//
// Commands: load session context, write the session handoff and ask the
// knowledge base; a status-bar item and the chat console make the product
// visible inside the IDE. Everything is delegated: read endpoints go to the
// loopback-only headless service when it is up, all tools go through
// repotory-mcp over stdio — zero logic duplication (thin-client discipline).
//
// The panel is a CONVERSATION (ADR-0036): a question goes in, a turn comes
// out, and the turns stay in the feed. The conversation model lives in
// chatSession.ts, the rendering in chatConsole.ts.
import * as vscode from "vscode";
import * as path from "node:path";
import * as os from "node:os";
import { accessSync, constants as fsConstants, existsSync } from "node:fs";
import { exec, execFile } from "node:child_process";
import { McpStdioClient } from "./mcpClient.js";
import { getJson, postJson } from "./headless.js";
import { RepotoryChatConsoleProvider } from "./chatConsole.js";
import { ChatSession, type Transport } from "./chatSession.js";

const CONFIG_SECTION = "repotory";
const SERVER_URL_KEY = "serverUrl";
const MCP_PATH_KEY = "mcpPath";
const MCP_ENV = "REPOTORY_MCP_BIN";
const BINARY_NAMES = ["repotory-mcp", "repotory-mcp.exe"];
const RELEASES_URL = "https://github.com/sky-jiangcheng/repotory/releases";
const AI_INTEGRATION_DOC =
  "https://github.com/sky-jiangcheng/repotory/blob/master/docs/features/ai-integration.md";
/** The webview view the chat console lives in. */
const CONSOLE_VIEW_ID = "repotory.notesSearch";

export let output: vscode.OutputChannel;
let statusItem: vscode.StatusBarItem;
let mcp: McpStdioClient | null = null;
let extensionContext: vscode.ExtensionContext | undefined;

interface HandoffStatus {
  note_id?: number;
  project_id?: number;
  title?: string;
  updated_at?: string;
}

function serverUrl(): string {
  return (
    vscode.workspace.getConfiguration(CONFIG_SECTION).get<string>(SERVER_URL_KEY) ??
    "http://127.0.0.1:18765"
  );
}

// workspacePath is the folder the session is actually standing in. It is the
// input the backend resolves into a project AND a repository, which is what makes
// opening `…/CBiPay/pay-common` behave differently from opening `…/CBiPay`.
function workspacePath(): string | undefined {
  const folder = vscode.workspace.workspaceFolders?.[0];
  return folder ? folder.uri.fsPath : undefined;
}

interface WorkspaceResolution {
  Project?: { id: number; name: string; root_path: string } | null;
  Repository?: { id: number; path: string } | null;
  MatchedOn?: string;
  Candidates?: unknown[];
}

type BinarySource = "setting" | "env" | "storage" | "path" | "common";

interface BinaryResolution {
  path?: string;
  source?: BinarySource;
  problem?: string;
}

function expandHome(value: string): string {
  if (value === "~") return os.homedir();
  if (value.startsWith("~/") || value.startsWith("~\\")) {
    return path.join(os.homedir(), value.slice(2));
  }
  return value;
}

function isExecutable(candidate: string): boolean {
  try {
    accessSync(candidate, fsConstants.X_OK);
    return true;
  } catch {
    // Windows does not honour X_OK for most files; existence is the best
    // signal there, and spawn will fail with a clear error if it is not.
    return process.platform === "win32" && existsSync(candidate);
  }
}

function commonBinaryDirs(): string[] {
  const home = os.homedir();
  if (process.platform === "darwin") {
    return [
      "/opt/homebrew/bin",
      "/usr/local/bin",
      "/usr/local/opt/repotory-mcp/bin",
      path.join(home, ".local", "bin"),
    ];
  }
  if (process.platform === "win32") {
    const local = process.env.LOCALAPPDATA ?? path.join(home, "AppData", "Local");
    return [
      path.join(home, "scoop", "shims"),
      path.join(home, "scoop", "apps", "repotory-mcp", "current"),
      path.join(local, "Programs", "repotory-mcp"),
      path.join(local, "repotory-mcp"),
    ];
  }
  return [
    "/usr/local/bin",
    "/usr/bin",
    path.join(home, ".local", "bin"),
    "/home/linuxbrew/.linuxbrew/bin",
  ];
}

function storageBinary(): string | undefined {
  const dir = extensionContext?.globalStorageUri.fsPath;
  if (!dir) return undefined;
  for (const name of BINARY_NAMES) {
    const candidate = path.join(dir, "bin", name);
    if (isExecutable(candidate)) return candidate;
  }
  return undefined;
}

/**
 * Resolve repotory-mcp without requiring it to be on PATH.
 *
 * Order: explicit setting -> env var -> extension storage -> PATH -> common
 * package-manager directories. A configured-but-missing path is reported as a
 * problem instead of silently falling through to PATH: the user pointed at a
 * file, and pretending otherwise would hide the mistake.
 */
export function resolveMcpBinary(): BinaryResolution {
  const configuredRaw =
    vscode.workspace.getConfiguration(CONFIG_SECTION).get<string>(MCP_PATH_KEY)?.trim() ?? "";
  if (configuredRaw) {
    const configured = expandHome(configuredRaw);
    return isExecutable(configured)
      ? { path: configured, source: "setting" }
      : { problem: `repotory.mcpPath 指向的文件不可执行：${configured}` };
  }

  const fromEnv = process.env[MCP_ENV]?.trim();
  if (fromEnv) {
    const expanded = expandHome(fromEnv);
    return isExecutable(expanded)
      ? { path: expanded, source: "env" }
      : { problem: `环境变量 ${MCP_ENV} 指向的文件不可执行：${expanded}` };
  }

  const stored = storageBinary();
  if (stored) return { path: stored, source: "storage" };

  const common = commonBinaryDirs();
  const seen = new Set<string>();
  for (const dir of [...(process.env.PATH ?? "").split(path.delimiter), ...common]) {
    if (!dir || seen.has(dir)) continue;
    seen.add(dir);
    for (const name of BINARY_NAMES) {
      const candidate = path.join(dir, name);
      if (isExecutable(candidate)) {
        return { path: candidate, source: common.includes(dir) ? "common" : "path" };
      }
    }
  }
  return {
    problem: "在 PATH、Homebrew / Scoop 常见目录和扩展存储中都没有找到 repotory-mcp",
  };
}

export function findBinary(): string | undefined {
  return resolveMcpBinary().path;
}

async function pickMcpBinary(): Promise<string | undefined> {
  const picked = await vscode.window.showOpenDialog({
    canSelectFiles: true,
    canSelectFolders: false,
    canSelectMany: false,
    openLabel: "使用此文件",
    title: "选择 repotory-mcp 可执行文件",
  });
  const file = picked?.[0];
  if (!file) return undefined;
  if (!isExecutable(file.fsPath)) {
    void vscode.window.showWarningMessage(`Repotory: 该文件不可执行：${file.fsPath}`);
    return undefined;
  }
  await vscode.workspace
    .getConfiguration(CONFIG_SECTION)
    .update(MCP_PATH_KEY, file.fsPath, vscode.ConfigurationTarget.Global);
  resetMcp();
  return file.fsPath;
}

function resetMcp(): void {
  mcp?.stop();
  mcp = null;
}

async function getMcp(): Promise<McpStdioClient> {
  if (mcp) return mcp;
  const resolved = resolveMcpBinary();
  if (!resolved.path) {
    const choice = await vscode.window.showErrorMessage(
      `Repotory: 未找到 repotory-mcp。${resolved.problem ?? ""}`,
      "定位可执行文件…",
      "打开下载页",
      "环境诊断",
    );
    if (choice === "定位可执行文件…") {
      const picked = await pickMcpBinary();
      if (picked) return getMcp();
    } else if (choice === "打开下载页") {
      void vscode.env.openExternal(vscode.Uri.parse(RELEASES_URL));
    } else if (choice === "环境诊断" && extensionContext) {
      await diagnose(extensionContext);
    }
    throw new Error(resolved.problem ?? "repotory-mcp not found");
  }
  const client = new McpStdioClient({ command: resolved.path }, (line: string) =>
    output?.appendLine(line),
  );
  await client.start();
  mcp = client;
  return client;
}

export async function callTool(name: string, args: Record<string, unknown>): Promise<string> {
  const client = await getMcp();
  try {
    return await client.callTool(name, args);
  } catch (err) {
    // A dead server must not wedge the extension: drop it so the next call restarts.
    mcp = null;
    client.stop();
    throw err;
  }
}

// The scope lives in the chat session now (ADR-0036): the panel locks to the
// workspace's project, and the status bar shows what it resolved to. The
// matching rules live server-side on purpose: the MCP tools resolve the same
// path, and a TypeScript reimplementation would let the panel and an agent's
// repotory_context disagree about which knowledge a session gets.
function showScopeInStatusBar(scope: { projectId: number; projectName: string; repository: string }): void {
  if (!statusItem) return;
  const dir = workspacePath();
  if (!scope.projectId) {
    statusItem.tooltip = dir
      ? `Repotory: ${dir} is not in the knowledge base yet\n\nClick to load the session context`
      : "Repotory: click to load the session context";
    return;
  }
  const label = scope.repository ? `${scope.projectName} / ${scope.repository}` : scope.projectName;
  statusItem.tooltip = `Workspace: ${label}\n${dir ?? ""}\n\nClick to load the session context`;
}

async function pingHeadless(): Promise<boolean> {
  try {
    await getJson(serverUrl(), "/health");
    return true;
  } catch {
    return false;
  }
}

async function refreshLastHandoff(): Promise<void> {
  if (!statusItem) return;
  try {
    const response = (await postJson(serverUrl(), "/api/rpc", {
      method: "LatestHandoff",
      // 0 = unscoped: the newest handoff across all projects. The status bar is
      // chrome about "when did I last hand anything off", so an unscoped answer
      // is what it wants; the workspace's own project would need the resolution
      // result, which is not this call's business.
      args: [0],
    })) as { result?: HandoffStatus | null };
    const handoff = response.result;
    const at = handoff?.updated_at ? new Date(handoff.updated_at) : undefined;
    if (handoff && at && !Number.isNaN(at.getTime())) {
      statusItem.text = `$(repo) Repotory · ${at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
      statusItem.tooltip = `Last handoff: ${handoff.title ?? "session"}\n${at.toLocaleString()}\n\nClick to load the session context`;
    } else {
      statusItem.text = "$(repo) Repotory";
      statusItem.tooltip = "Repotory: no session handoff yet. Click to load the session context";
    }
  } catch {
    statusItem.text = "$(repo) Repotory";
    statusItem.tooltip = "Repotory: click to load the session context";
  }
}

function withProgress<T>(title: string, task: () => Promise<T>): Thenable<T> {
  return vscode.window.withProgress(
    { location: vscode.ProgressLocation.Notification, title: `Repotory: ${title}` },
    task,
  );
}

function showOutput(title: string, body: string): void {
  output.appendLine(`\n=== ${title} ===\n${body}`);
  output.show(true);
}

// ---------------------------------------------------------------- commands

async function loadContext(): Promise<void> {
  const args: Record<string, unknown> = {};
  // The workspace path is the only input: the backend resolves it into a project
  // AND a repository. There is no id to keep in sync and none to go stale.
  const dir = workspacePath();
  if (dir) args.path = dir;
  const text = await withProgress("loading session context", () =>
    callTool("repotory_context", args),
  );
  showOutput("Session context", text);
}

async function writeHandoff(): Promise<void> {
  // The project comes from the workspace path, resolved server-side. It used to
  // be that this command simply refused without a hand-typed number, which made
  // the primary write path of the product unusable out of the box.
  const dir = workspacePath();
  if (!dir) {
    void vscode.window.showErrorMessage(
      "Repotory: open a folder — the handoff is attached to the project your workspace resolves to.",
    );
    return;
  }
  const summary = await vscode.window.showInputBox({
    prompt: "Repotory handoff — one paragraph: what was this session about?",
    placeHolder: "e.g. Wired the FTS5 rebuild path; tests green",
    ignoreFocusOut: true,
  });
  if (!summary) return;

  const askList = async (label: string): Promise<string[]> => {
    const raw = await vscode.window.showInputBox({
      prompt: `Repotory handoff — ${label} (comma-separated, empty to skip)`,
      ignoreFocusOut: true,
    });
    return raw
      ? raw
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean)
      : [];
  };
  const changes = await askList("changes");
  const nextSteps = await askList("next steps");

  const handoffArgs: Record<string, unknown> = {
    agent: "vscode-extension",
    summary,
    changes,
    next_steps: nextSteps,
    // The path is how the backend picks the project. There is no id to pin, so
    // none is sent — an explicit 0 would be a magic number meaning "unknown".
    path: dir,
  };

  const noteText = await withProgress("writing session handoff", () =>
    callTool("repotory_handoff", handoffArgs),
  );
  showOutput("Handoff recorded", noteText);
  void refreshLastHandoff();
}

// `repotory-init` is private and is not installed globally, so a shipped VSIX
// cannot assume it exists in the user's project. Prefer the copy bundled into the
// extension itself (scripts/sync-init.mjs puts it there at compile time); fall
// back to a workspace copy so developing inside this repository still uses the
// live source.
async function findInitScript(
  context: vscode.ExtensionContext,
): Promise<vscode.Uri | undefined> {
  const folder = vscode.workspace.workspaceFolders?.[0];
  const candidates = [vscode.Uri.joinPath(context.extensionUri, "repotory-init", "index.mjs")];
  if (folder) {
    candidates.push(vscode.Uri.joinPath(folder.uri, "scripts", "repotory-init", "index.mjs"));
  }
  for (const candidate of candidates) {
    try {
      await vscode.workspace.fs.stat(candidate);
      return candidate;
    } catch {
      /* keep looking */
    }
  }
  return undefined;
}

/**
 * Run the bundled installer without opening a terminal.
 *
 * The extension host is Electron, so `process.execPath` is the editor binary;
 * ELECTRON_RUN_AS_NODE turns it into a plain Node runtime. Falling back to
 * `node` on PATH covers forks that disable that switch. Only a spawn failure
 * (ENOENT/EACCES) falls back — a non-zero exit is a real installer error and
 * must not be retried with a second runtime.
 */
function runInitScript(
  script: vscode.Uri,
  cwd: string,
  args: string[],
): Promise<void> {
  const attempt = (command: string, env: NodeJS.ProcessEnv): Promise<void> =>
    new Promise((resolve, reject) => {
      execFile(
        command,
        [script.fsPath, ...args],
        { cwd, env, timeout: 60_000, windowsHide: true },
        (err, stdout, stderr) => {
          if (stdout.trim()) output.appendLine(stdout.trim());
          if (stderr.trim()) output.appendLine(stderr.trim());
          if (err) reject(err);
          else resolve();
        },
      );
    });

  return attempt(process.execPath, { ...process.env, ELECTRON_RUN_AS_NODE: "1" }).catch(
    (err: NodeJS.ErrnoException) => {
      if (err.code !== "ENOENT" && err.code !== "EACCES") throw err;
      output.appendLine(`Electron-as-node unavailable (${err.code}); retrying with node on PATH.`);
      return attempt("node", process.env);
    },
  );
}

async function registerMcp(context: vscode.ExtensionContext): Promise<void> {
  const folder = vscode.workspace.workspaceFolders?.[0];
  if (!folder) {
    void vscode.window.showErrorMessage(
      "Repotory: 请先打开一个项目文件夹——MCP 注册会写入该项目的 .mcp.json / .vscode/mcp.json / .cursor/mcp.json。",
    );
    return;
  }
  const script = await findInitScript(context);
  if (!script) {
    const choice = await vscode.window.showWarningMessage(
      "Repotory: 扩展内没有找到 repotory-init，请重新安装扩展。",
      "打开文档",
    );
    if (choice === "打开文档") {
      void vscode.env.openExternal(vscode.Uri.parse(AI_INTEGRATION_DOC));
    }
    return;
  }

  const bin = findBinary();
  const args = ["--yes", "--with-hook"];
  if (bin) args.push("--bin", bin);

  await withProgress("registering MCP server", async () => {
    try {
      await runInitScript(script, folder.uri.fsPath, args);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      const choice = await vscode.window.showErrorMessage(
        `Repotory: 自动注册失败（${message}）。`,
        "查看日志",
        "在终端手动运行",
      );
      if (choice === "查看日志") output.show(true);
      if (choice === "在终端手动运行") {
        // Last-resort escape hatch, not the primary path: the terminal exists
        // only when the silent run could not complete.
        const terminal = vscode.window.createTerminal({ name: "repotory-init", cwd: folder.uri });
        terminal.sendText(
          `node "${script.fsPath}" ${args
            .map((arg) => (arg.includes(" ") ? `"${arg}"` : arg))
            .join(" ")}`,
        );
        terminal.show();
      }
      return;
    }
    const choice = await vscode.window.showInformationMessage(
      "Repotory: MCP 服务器已写入当前项目的客户端配置（Claude Code / Cursor / VS Code / Windsurf），重载窗口后生效。",
      "查看日志",
    );
    if (choice === "查看日志") output.show(true);
  });
}

function binarySourceLabel(source?: BinarySource): string {
  switch (source) {
    case "setting":
      return "设置 repotory.mcpPath";
    case "env":
      return `环境变量 ${MCP_ENV}`;
    case "storage":
      return "扩展存储";
    case "path":
      return "PATH";
    case "common":
      return "常见安装目录";
    default:
      return "";
  }
}

async function resolveWorkspaceForDiagnostics(): Promise<{
  reachable: boolean;
  resolution: WorkspaceResolution | null;
}> {
  const dir = workspacePath();
  if (!dir) return { reachable: true, resolution: null };
  try {
    const response = (await postJson(serverUrl(), "/api/rpc", {
      method: "ResolveWorkspacePath",
      args: [dir],
    })) as { result?: WorkspaceResolution | null };
    return { reachable: true, resolution: response.result ?? null };
  } catch {
    return { reachable: false, resolution: null };
  }
}

async function isMcpRegistered(dir: string | undefined): Promise<boolean> {
  const files = [
    ...(dir
      ? [".mcp.json", path.join(".vscode", "mcp.json"), path.join(".cursor", "mcp.json")]
      : []),
    path.join(os.homedir(), ".codeium", "windsurf", "mcp_config.json"),
  ];
  for (const rel of files) {
    const target = path.isAbsolute(rel) ? rel : path.join(dir ?? "", rel);
    try {
      const raw = await vscode.workspace.fs.readFile(vscode.Uri.file(target));
      if (/"repotory"\s*:/.test(Buffer.from(raw).toString("utf8"))) return true;
    } catch {
      /* missing or unreadable */
    }
  }
  return false;
}

interface DiagnosticAction extends vscode.QuickPickItem {
  run: () => Promise<void>;
}

/**
 * One place that answers "why does this not work?" and offers the fix inline.
 * Every row is clickable: binary missing -> file picker/releases; service down
 * -> re-check; workspace unmatched -> explanation; MCP not registered -> run
 * the bundled installer. This is the GUI replacement for the old
 * "install this, run that command" error dialogs.
 */
async function diagnose(context: vscode.ExtensionContext): Promise<void> {
  const binary = resolveMcpBinary();
  const serverOk = await pingHeadless();
  const dir = workspacePath();
  const workspaceState = await resolveWorkspaceForDiagnostics();
  const registered = await isMcpRegistered(dir);
  const repositoryName = workspaceState.resolution?.Repository?.path
    ? path.basename(workspaceState.resolution.Repository.path)
    : undefined;

  const items: DiagnosticAction[] = [
    {
      label: binary.path ? "$(check) repotory-mcp" : "$(error) repotory-mcp",
      description: binary.path ?? binary.problem ?? "未找到",
      detail: binary.path
        ? `来源：${binarySourceLabel(binary.source)}`
        : "点击选择可执行文件；取消后可改从 Releases 下载",
      run: async () => {
        if (binary.path) {
          output.appendLine(`repotory-mcp: ${binary.path}`);
          output.show(true);
          return;
        }
        const picked = await pickMcpBinary();
        if (!picked) {
          void vscode.env.openExternal(vscode.Uri.parse(RELEASES_URL));
          return;
        }
        await diagnose(context);
      },
    },
    {
      label: serverOk ? "$(check) 本地服务" : "$(warning) 本地服务",
      description: serverOk
        ? serverUrl()
        : `未连接到 ${serverUrl()}（搜索仍可经 repotory-mcp 完成）`,
      detail: serverOk ? "健康检查通过" : "桌面版可在设置中修改 repotory.serverUrl；点击重新检测",
      run: async () => {
        await diagnose(context);
      },
    },
    {
      label: dir
        ? workspaceState.resolution?.Project
          ? "$(check) 工作区"
          : "$(warning) 工作区"
        : "$(circle-slash) 工作区",
      description: !dir
        ? "未打开文件夹"
        : !workspaceState.reachable
          ? "服务不可达，暂时无法解析"
          : workspaceState.resolution?.Project
            ? `${workspaceState.resolution.Project.name}${
                repositoryName ? ` · ${repositoryName}` : ""
              }`
            : "未匹配知识库项目",
      detail: dir ?? "",
      run: async () => {
        if (!dir) {
          void vscode.window.showWarningMessage("先打开一个项目文件夹。");
          return;
        }
        if (!workspaceState.reachable) {
          void vscode.window.showWarningMessage(
            "本地服务不可达，无法解析工作区；启动服务后再检测。",
          );
          return;
        }
        if (!workspaceState.resolution?.Project) {
          void vscode.window.showInformationMessage(
            "该文件夹还不在知识库中：先在 Repotory 桌面应用里扫描/导入；命令本身仍可用。",
          );
          return;
        }
        output.appendLine(
          `workspace: ${dir} -> ${workspaceState.resolution.Project.name}${
            workspaceState.resolution.Repository
              ? ` / ${workspaceState.resolution.Repository.path}`
              : ""
          }`,
        );
        output.show(true);
      },
    },
    {
      label: registered ? "$(check) MCP 客户端配置" : "$(circle-outline) MCP 客户端配置",
      description: registered ? "已写入 repotory 条目" : "当前项目尚未注册",
      detail: dir
        ? "写入 .mcp.json / .vscode/mcp.json / .cursor/mcp.json（后台执行，不打开终端）"
        : "需要先打开项目文件夹",
      run: async () => {
        if (!dir) {
          void vscode.window.showWarningMessage("先打开一个项目文件夹。");
          return;
        }
        await registerMcp(context);
        await diagnose(context);
      },
    },
  ];

  const picked = await vscode.window.showQuickPick(items, {
    title: "Repotory 环境诊断",
    placeHolder: "选择一项查看详情或修复",
  });
  if (picked) await picked.run();
}

// 打开 Repotory 桌面应用：优先用平台原生方式启动，找不到时引导到 Releases 下载。
async function openDesktop(): Promise<void> {
  let cmd: string;
  switch (process.platform) {
    case "darwin":
      cmd = "open -a Repotory";
      break;
    case "win32":
      cmd = "start \"\" repotory.exe";
      break;
    default:
      cmd = "repotory";
      break;
  }
  exec(cmd, (err) => {
    if (err) {
      void vscode.window
        .showErrorMessage(
          "Repotory: 桌面应用未找到。请先从 GitHub Releases 下载并安装 Repotory 桌面应用。",
          "打开 Releases 页面",
        )
        .then((choice) => {
          if (choice === "打开 Releases 页面") {
            void vscode.env.openExternal(
              vscode.Uri.parse(
                "https://github.com/sky-jiangcheng/repotory/releases",
              ),
            );
          }
        });
    }
  });
}

// 首次安装引导：让新用户一眼看出这是 Repotory 扩展，并一步进入配置向导。
async function onboard(context: vscode.ExtensionContext): Promise<void> {
  const already = context.globalState.get<boolean>("repotory.onboarded");
  if (already) return;

  const choice = await vscode.window.showInformationMessage(
    "👋 欢迎使用 Repotory。\n\n" +
      "配置向导会自动检测 repotory-mcp、检查本地服务、并把 MCP 写入当前项目——" +
      "不需要手动敲命令。",
    "开始配置",
    "稍后再说",
  );

  if (choice === "开始配置") {
    await diagnose(context);
  }

  await context.globalState.update("repotory.onboarded", true);
}

// ---------------------------------------------------------------- lifecycle

export function activate(context: vscode.ExtensionContext): void {
  extensionContext = context;
  output = vscode.window.createOutputChannel("Repotory", { log: true });
  statusItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 60);
  statusItem.text = "$(repo) Repotory";
  statusItem.tooltip = "Repotory: click to load the session context";
  statusItem.command = "repotory.loadContext";
  void refreshLastHandoff();

  const chatConsole = new RepotoryChatConsoleProvider();
  const transport: Transport = {
    callTool,
    // The headless RPC bridge: the one place MCP deliberately does not carry
    // (project resolution, moving a note between projects).
    rpc: async (method, args) => {
      const response = (await postJson(serverUrl(), "/api/rpc", { method, args })) as {
        result?: unknown;
        error?: string;
      } | null;
      if (response?.error) throw new Error(response.error);
      return response?.result ?? null;
    },
    serverUrl,
    workspacePath,
    openExternal: async (url) => {
      await vscode.env.openExternal(vscode.Uri.parse(url));
    },
  };
  const session = new ChatSession(transport, (message) => chatConsole.post(message));
  chatConsole.onMessage = (msg) => session.handle(msg);
  chatConsole.onReady = () => session.init();
  session.onScopeChanged = (scope) => showScopeInStatusBar(scope);
  void session.refreshScope();
  context.subscriptions.push(
    vscode.workspace.onDidChangeWorkspaceFolders(() => void session.refreshScope()),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration(`${CONFIG_SECTION}.${MCP_PATH_KEY}`)) resetMcp();
    }),
  );

  const handoffTimer = setInterval(() => void refreshLastHandoff(), 60_000);
  context.subscriptions.push({ dispose: () => clearInterval(handoffTimer) });

  const register = (id: string, fn: () => Promise<void>) =>
    context.subscriptions.push(
      vscode.commands.registerCommand(id, async () => {
        try {
          await fn();
        } catch (err) {
          void vscode.window.showErrorMessage(
            `Repotory: ${err instanceof Error ? err.message : String(err)}`,
          );
        }
      }),
    );

  // A command APPENDS a turn to the conversation instead of replacing the
  // panel's content (ADR-0036): the panel is a history, and a command that
  // wiped it would make the history a lie.
  const askInConsole = async (mode: "ask" | "notes" | "pages", prompt: string): Promise<void> => {
    const query = await vscode.window.showInputBox({
      prompt,
      ignoreFocusOut: true,
    });
    if (!query) return;
    await session.run(query, mode);
    await vscode.commands.executeCommand(`${CONSOLE_VIEW_ID}.focus`);
  };

  register("repotory.loadContext", loadContext);
  register("repotory.handoff", writeHandoff);
  register("repotory.ask", () =>
    askInConsole("ask", "Repotory — ask the knowledge base (answer with sources)"),
  );
  register("repotory.search", () =>
    askInConsole("notes", "Repotory — search notes & todos (FTS5)"),
  );
  register("repotory.searchWiki", () =>
    askInConsole("pages", "Repotory — search the approved knowledge pages"),
  );
  register("repotory.registerMcp", () => registerMcp(context));
  register("repotory.diagnose", () => diagnose(context));
  register("repotory.setMcpPath", async () => {
    await pickMcpBinary();
  });
  register("repotory.openDesktop", () => openDesktop());

  context.subscriptions.push(
    statusItem,
    vscode.window.registerWebviewViewProvider(CONSOLE_VIEW_ID, chatConsole, {
      webviewOptions: { retainContextWhenHidden: true },
    }),
    { dispose: () => mcp?.stop() },
  );

  statusItem.show();
  void pingHeadless().then((ok) => {
    if (!ok) {
      statusItem.tooltip =
        "Headless service not reachable at " +
        serverUrl() +
        " — commands still work through repotory-mcp";
    }
  });
  void onboard(context);
}

export function deactivate(): void {
  mcp?.stop();
}
