// One-click local service start for the VS Code extension (ADR-0063, A-light).
//
// The JetBrains plugin has been able to start a loopback `repotory-server`
// since the 2026-10-22 usability revision (ADR-0027 decision 4): the plugin
// MAY start one, but only when nothing already answers /health, only from a
// binary the user already has, only on a loopback URL, and it stops only the
// process it started itself. This file ports that pattern to the VS Code
// side, where the provider-configuration gap (ADR-0063 decision 2) is felt
// most: without a running server an IDE-only user cannot save an AI
// endpoint, so `repotory_ask` permanently falls back to keyword matching.
//
// The thin-client discipline is untouched: this module only spawns a local
// process; every answer still comes from the shared service layer over HTTP.
import { spawn, type ChildProcess } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { getJson } from "./headless.js";

/** Env var the resolution order consults second (after the setting). */
const SERVER_ENV = "REPOTORY_SERVER_BIN";

/** How long to wait for /health after spawning (same budget as JetBrains). */
const START_TIMEOUT_MS = 30_000;

/** How often the health probe is repeated while waiting. */
const HEALTH_POLL_MS = 250;

/** The port the headless service defaults to (`cmd/server`'s flag default). */
export const DEFAULT_SERVER_PORT = 18765;

/** Where a resolved server binary came from, for diagnostics text. */
export interface ServerBinary {
  file: string;
  source: string;
}

/** Result of [ServerLauncher.start]; never a bare boolean. */
export type StartOutcome =
  | {
      kind: "started";
      port: number;
      webUi: boolean;
      binary: ServerBinary;
      /** /health answered within the wait. A process that is up but silent is
       *  NOT the same as a working service. */
      healthy: boolean;
    }
  | /** Something already answered /health; nothing was spawned. */ { kind: "alreadyRunning" }
  | /** No executable was found in any documented location. */ { kind: "binaryMissing"; searched: string }
  | /** The spawn or the health wait failed; message is user-presentable. */ { kind: "failed"; message: string };

/** Expands a leading `~` so a path copied from a shell setting works verbatim. */
export function expandHome(value: string): string {
  const home = os.homedir();
  if (value === "~") return home;
  if (value.startsWith("~/") || value.startsWith("~\\")) return path.join(home, value.slice(2));
  return value;
}

/** True when the file exists and is executable (Windows: existence is enough). */
export function isExecutable(candidate: string): boolean {
  try {
    fs.accessSync(candidate, fs.constants.X_OK);
    return true;
  } catch {
    return process.platform === "win32" && fs.existsSync(candidate);
  }
}

/** Common install directories per platform (mirrors the JetBrains resolver). */
export function commonServerDirs(): string[] {
  const home = os.homedir();
  if (process.platform === "darwin") {
    return ["/opt/homebrew/bin", "/usr/local/bin", path.join(home, ".local", "bin")];
  }
  if (process.platform === "win32") {
    const local = process.env.LOCALAPPDATA ?? path.join(home, "AppData", "Local");
    return [
      path.join(home, "scoop", "shims"),
      path.join(local, "Programs", "repotory"),
      path.join(local, "repotory"),
    ];
  }
  return ["/usr/local/bin", "/usr/bin", path.join(home, ".local", "bin", "repotory"), "/home/linuxbrew/.linuxbrew/bin"];
}

/** The executable names the release bundle ships (ADR-0049: renamed with compat names). */
function serverBinaryNames(): string[] {
  return process.platform === "win32"
    ? ["cinder-server.exe", "repotory-server.exe"]
    : ["cinder-server", "repotory-server"];
}

/**
 * Documented resolution order; the first existing file wins.
 *
 * `settingGetter` reads the `repotory.serverBinary` setting (empty = unset).
 * `commonDirs` is injectable so tests stay hermetic on machines that happen
 * to have a server installed in a common directory. A configured-but-missing
 * path is NOT silently skipped: the caller reports it as a problem instead of
 * pretending otherwise.
 */
export function resolveServerBinary(
  settingGetter: () => string,
  commonDirs: string[] = commonServerDirs(),
): ServerBinary | null {
  const configured = settingGetter().trim();
  if (configured) {
    const file = expandHome(configured);
    if (isExecutable(file)) return { file, source: "设置 repotory.serverBinary" };
  }

  const fromEnv = (process.env[SERVER_ENV] ?? "").trim();
  if (fromEnv) {
    const file = expandHome(fromEnv);
    if (isExecutable(file)) return { file, source: `环境变量 ${SERVER_ENV}` };
  }

  const dirs = [
    ...(process.env.PATH ?? "").split(path.delimiter).filter(Boolean),
    ...commonDirs,
  ];
  const seen = new Set<string>();
  for (const dir of dirs) {
    if (!dir || seen.has(dir)) continue;
    seen.add(dir);
    for (const name of serverBinaryNames()) {
      const candidate = path.join(dir, name);
      if (isExecutable(candidate)) return { file: candidate, source: dir };
    }
  }
  return null;
}

/** Human-readable list for the "binary missing" message. */
export function searchedLocations(): string {
  return [
    "设置 repotory.serverBinary",
    `环境变量 ${SERVER_ENV}`,
    "PATH",
    ...commonServerDirs(),
  ].join("\n");
}

/**
 * Builds the server command line: `--port` always, `--web-dist` when the
 * release bundle's `web/` sits next to the binary (the bundle lays them
 * together, so deep links work without the user passing the flag by hand).
 */
export function buildServerArgs(binaryFile: string, port: number): string[] {
  const args = [binaryFile, "--port", String(port)];
  const webDist = path.join(path.dirname(binaryFile), "web");
  if (fs.existsSync(path.join(webDist, "index.html"))) {
    args.push("--web-dist", webDist);
  }
  return args;
}

/**
 * True when the URL's host is loopback — the plugin only starts LOCAL services.
 *
 * `URL.host` includes the port (`127.0.0.1:18765`); the loopback test must
 * read `hostname`, or every ported URL would be rejected.
 */
export function isLoopbackUrl(url: string): boolean {
  try {
    // `URL.hostname` keeps the brackets around an IPv6 literal ("[::1]").
    const { hostname } = new URL(url);
    return hostname === "" || hostname === "127.0.0.1" || hostname === "localhost" || hostname === "::1" || hostname === "[::1]";
  } catch {
    return false;
  }
}

/** Parses the port out of a server URL, defaulting to the service default. */
export function portOf(url: string): number {
  try {
    const { port } = new URL(url);
    return port ? Number(port) : DEFAULT_SERVER_PORT;
  } catch {
    return DEFAULT_SERVER_PORT;
  }
}

export interface ServerLauncherOptions {
  /** Injected for tests; defaults to spawning with output appended to logPath. */
  spawnFn?: (command: string[], logPath: string) => ChildProcess;
  /** Injected for tests; defaults to a GET /health probe. */
  pingFn?: (url: string) => Promise<boolean>;
  /** Injected for tests; defaults to a real sleep. */
  sleepFn?: (ms: number) => Promise<void>;
  /** Where the server's output is appended (diagnostics on startup failure). */
  logPath?: string;
  /** Health-wait budget; injectable so tests do not wait 30 real seconds. */
  timeoutMs?: number;
  /** Common install directories for binary resolution; injectable for tests. */
  commonDirs?: string[];
}

/** Owns the optional `repotory-server` child process. */
export class ServerLauncher {
  private process: ChildProcess | null = null;
  private ownedByExtension = false;
  private readonly spawnFn: (command: string[], logPath: string) => ChildProcess;
  private readonly pingFn: (url: string) => Promise<boolean>;
  private readonly sleepFn: (ms: number) => Promise<void>;
  private readonly logPath: string;
  private readonly timeoutMs: number;
  private readonly commonDirs: string[];

  constructor(options: ServerLauncherOptions = {}) {
    this.spawnFn =
      options.spawnFn ??
      ((command, logPath) => {
        const exe = command[0];
        if (!exe) throw new Error("empty server command");
        const out = fs.createWriteStream(logPath, { flags: "a" });
        const child = spawn(exe, command.slice(1), {
          stdio: ["ignore", "pipe", "pipe"],
        }) as ChildProcess;
        child.stdout?.pipe(out);
        child.stderr?.pipe(out);
        return child;
      });
    this.pingFn =
      options.pingFn ??
      (async (url: string) => {
        try {
          await getJson(url, "/health");
          return true;
        } catch {
          return false;
        }
      });
    this.sleepFn = options.sleepFn ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
    this.logPath = options.logPath ?? path.join(os.tmpdir(), "cinder-server.log");
    this.timeoutMs = options.timeoutMs ?? START_TIMEOUT_MS;
    this.commonDirs = options.commonDirs ?? commonServerDirs();
  }

  /**
   * Starts a loopback server for `serverUrl`, or reports why it could not.
   *
   * Never fights a running instance: /health is probed first. `settingGetter`
   * reads the `repotory.serverBinary` setting (the module stays free of the
   * vscode API so it is testable without an editor). Blocks up to
   * START_TIMEOUT_MS while waiting for the service to answer.
   */
  async start(
    serverUrl: string,
    settingGetter: () => string,
    onProgress?: (line: string) => void,
  ): Promise<StartOutcome> {
    if (await this.pingFn(serverUrl)) return { kind: "alreadyRunning" };

    if (!isLoopbackUrl(serverUrl)) {
      return {
        kind: "failed",
        message: `只启动本地服务：${serverUrl} 不是回环地址。请自行启动该服务，或把 repotory.serverUrl 改回 127.0.0.1。`,
      };
    }

    const binary = resolveServerBinary(settingGetter, this.commonDirs);
    if (!binary) return { kind: "binaryMissing", searched: searchedLocations() };

    const port = portOf(serverUrl);
    const command = buildServerArgs(binary.file, port);
    const webUi = command.includes("--web-dist");

    try {
      const child = this.spawnFn(command, this.logPath);
      this.process = child;
      this.ownedByExtension = true;

      onProgress?.(`已启动 ${path.basename(binary.file)}，等待服务就绪…`);
      const deadline = Date.now() + this.timeoutMs;
      while (Date.now() < deadline) {
        if (await this.pingFn(serverUrl)) {
          onProgress?.("服务已就绪。");
          return { kind: "started", port, webUi, binary, healthy: true };
        }
        if (child.exitCode !== null || child.signalCode !== null) {
          const tail = logTail(this.logPath);
          if (/address already in use/i.test(tail)) {
            return {
              kind: "failed",
              message: `端口 ${port} 已被其他进程占用，服务未能启动。停掉那个进程（或换一个端口）再试。`,
            };
          }
          return {
            kind: "failed",
            message: `repotory-server 启动时退出（退出码 ${child.exitCode ?? child.signalCode}）。` +
              (tail ? ` 最后输出：${tail}` : "") +
              ` 日志：${this.logPath}`,
          };
        }
        await this.sleepFn(HEALTH_POLL_MS);
      }
      // The process is alive but /health never answered: not a working
      // service. Say so instead of handing back a promise the next call breaks.
      onProgress?.("进程在运行，但服务尚未应答。");
      return { kind: "started", port, webUi, binary, healthy: false };
    } catch (err) {
      return {
        kind: "failed",
        message: `无法启动 ${binary.file}：${err instanceof Error ? err.message : String(err)}`,
      };
    }
  }

  /** Kills the child only when this extension spawned it. */
  stopOwned(): void {
    const child = this.process;
    if (!child || !this.ownedByExtension) return;
    this.ownedByExtension = false;
    this.process = null;
    try {
      if (child.exitCode === null && child.signalCode === null) {
        child.kill();
      }
    } catch {
      /* the process is already gone */
    }
  }
}

/** The last few meaningful lines of the server log. */
function logTail(logPath: string, maxLines = 4): string {
  try {
    const stat = fs.statSync(logPath);
    const window = Math.min(stat.size, 4096);
    const fd = fs.openSync(logPath, "r");
    try {
      const buf = Buffer.alloc(window);
      fs.readSync(fd, buf, 0, window, stat.size - window);
      return buf
        .toString("utf8")
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean)
        .slice(-maxLines)
        .join(" / ");
    } finally {
      fs.closeSync(fd);
    }
  } catch {
    return "";
  }
}
