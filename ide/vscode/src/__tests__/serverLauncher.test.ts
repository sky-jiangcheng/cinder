// Unit tests for the one-click local service start (serverLauncher.ts).
//
// The launcher spawns a real process in production; here every seam (spawn,
// ping, sleep, timeout, log path) is injected, so the resolution order, the
// command line, the health-wait branches and the stop-only-what-we-started
// rule are all checked without touching a real server.
import { strict as assert } from "node:assert";
import { test } from "node:test";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { ChildProcess } from "node:child_process";
import {
  ServerLauncher,
  buildServerArgs,
  isLoopbackUrl,
  portOf,
  resolveServerBinary,
  searchedLocations,
} from "../serverLauncher.js";

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "cinder-launcher-"));
}

/** A fake executable: created, marked executable, optionally with a web/. */
function fakeBinary(dir: string, withWeb = false): string {
  const bin = path.join(dir, "cinder-server");
  fs.writeFileSync(bin, "#!/bin/sh\n");
  fs.chmodSync(bin, 0o755);
  if (withWeb) {
    fs.mkdirSync(path.join(dir, "web"));
    fs.writeFileSync(path.join(dir, "web", "index.html"), "<!doctype html>");
  }
  return bin;
}

/** A fake child process the spawnFn returns. */
function fakeChild(exitCode: number | null = null): ChildProcess {
  const killed: string[] = [];
  const child = {
    exitCode,
    signalCode: null,
    pid: 12345,
    stdout: null,
    stderr: null,
    kill: (() => {
      killed.push("kill");
      return true;
    }) as unknown as ChildProcess["kill"],
  } as unknown as ChildProcess;
  return Object.assign(child, { _killed: killed });
}

test("resolveServerBinary: the setting wins over env", () => {
  const dir = tmpDir();
  const fromSetting = fakeBinary(dir, true);
  const fromEnvDir = tmpDir();
  const fromEnv = fakeBinary(fromEnvDir);
  const oldEnv = process.env.REPOTORY_SERVER_BIN;
  process.env.REPOTORY_SERVER_BIN = fromEnv;
  try {
    const bin = resolveServerBinary(() => fromSetting, []);
    assert.equal(bin?.file, fromSetting);
    assert.match(bin?.source ?? "", /repotory\.serverBinary/);
  } finally {
    if (oldEnv === undefined) delete process.env.REPOTORY_SERVER_BIN;
    else process.env.REPOTORY_SERVER_BIN = oldEnv;
  }
});

test("resolveServerBinary: env is used when the setting is empty", () => {
  const dir = tmpDir();
  const fromEnv = fakeBinary(dir);
  const oldEnv = process.env.REPOTORY_SERVER_BIN;
  process.env.REPOTORY_SERVER_BIN = fromEnv;
  try {
    const bin = resolveServerBinary(() => "", []);
    assert.equal(bin?.file, fromEnv);
    assert.match(bin?.source ?? "", /REPOTORY_SERVER_BIN/);
  } finally {
    if (oldEnv === undefined) delete process.env.REPOTORY_SERVER_BIN;
    else process.env.REPOTORY_SERVER_BIN = oldEnv;
  }
});

test("resolveServerBinary: a configured-but-missing path falls through to PATH", () => {
  const dir = tmpDir();
  const real = fakeBinary(dir);
  const oldEnv = process.env.REPOTORY_SERVER_BIN;
  delete process.env.REPOTORY_SERVER_BIN;
  const oldPath = process.env.PATH;
  process.env.PATH = dir;
  try {
    // The setting points at a file that does not exist: it must be skipped,
    // not returned and not thrown on — resolution continues to PATH.
    const bin = resolveServerBinary(() => path.join(dir, "does-not-exist"), []);
    assert.equal(bin?.file, real);
  } finally {
    process.env.PATH = oldPath ?? "";
    if (oldEnv !== undefined) process.env.REPOTORY_SERVER_BIN = oldEnv;
  }
});

test("resolveServerBinary: PATH is consulted after setting and env", () => {
  const dir = tmpDir();
  const fromPath = fakeBinary(dir);
  const oldEnv = process.env.REPOTORY_SERVER_BIN;
  delete process.env.REPOTORY_SERVER_BIN;
  const oldPath = process.env.PATH;
  process.env.PATH = dir;
  try {
    const bin = resolveServerBinary(() => "", []);
    assert.equal(bin?.file, fromPath);
  } finally {
    process.env.PATH = oldPath ?? "";
    if (oldEnv !== undefined) process.env.REPOTORY_SERVER_BIN = oldEnv;
  }
});

test("resolveServerBinary: nothing found returns null and a search list", () => {
  const oldEnv = process.env.REPOTORY_SERVER_BIN;
  delete process.env.REPOTORY_SERVER_BIN;
  const oldPath = process.env.PATH;
  process.env.PATH = tmpDir(); // empty dir on PATH
  try {
    const bin = resolveServerBinary(() => "", []);
    assert.equal(bin, null);
    const searched = searchedLocations();
    assert.match(searched, /repotory\.serverBinary/);
    assert.match(searched, /REPOTORY_SERVER_BIN/);
    assert.match(searched, /PATH/);
  } finally {
    process.env.PATH = oldPath ?? "";
    if (oldEnv !== undefined) process.env.REPOTORY_SERVER_BIN = oldEnv;
  }
});

test("buildServerArgs: --port always, --web-dist only with a web/ next to the binary", () => {
  const withWeb = tmpDir();
  const binWithWeb = fakeBinary(withWeb, true);
  const args = buildServerArgs(binWithWeb, 19000);
  assert.deepEqual(args.slice(0, 3), [binWithWeb, "--port", "19000"]);
  assert.ok(args.includes("--web-dist"));

  const withoutWeb = tmpDir();
  const binNoWeb = fakeBinary(withoutWeb, false);
  const args2 = buildServerArgs(binNoWeb, 18765);
  assert.ok(!args2.includes("--web-dist"));
});

test("isLoopbackUrl and portOf", () => {
  assert.equal(isLoopbackUrl("http://127.0.0.1:18765"), true);
  assert.equal(isLoopbackUrl("http://localhost:18765"), true);
  assert.equal(isLoopbackUrl("http://[::1]:18765"), true);
  assert.equal(isLoopbackUrl("http://example.com:18765"), false);
  assert.equal(isLoopbackUrl("not a url"), false);
  assert.equal(portOf("http://127.0.0.1:19000"), 19000);
  assert.equal(portOf("http://127.0.0.1"), 18765);
});

test("start: an already-answering service is not fought", async () => {
  const launcher = new ServerLauncher({
    pingFn: async () => true,
    spawnFn: () => {
      throw new Error("must not spawn");
    },
  });
  const outcome = await launcher.start("http://127.0.0.1:18765", () => "");
  assert.equal(outcome.kind, "alreadyRunning");
});

test("start: a non-loopback URL is refused", async () => {
  const launcher = new ServerLauncher({ pingFn: async () => false });
  const outcome = await launcher.start("http://example.com:18765", () => "");
  assert.equal(outcome.kind, "failed");
  assert.match((outcome as { message: string }).message, /回环/);
});

test("start: a missing binary reports the searched locations", async () => {
  // PATH must be isolated too: the machine's PATH can contain a directory
  // with a real server, which would defeat the "missing" branch.
  const emptyPath = tmpDir();
  const oldPath = process.env.PATH;
  process.env.PATH = emptyPath;
  const launcher = new ServerLauncher({ pingFn: async () => false, commonDirs: [] });
  try {
    const outcome = await launcher.start("http://127.0.0.1:18765", () => "");
    assert.equal(outcome.kind, "binaryMissing");
    assert.match((outcome as { searched: string }).searched, /repotory\.serverBinary/);
  } finally {
    process.env.PATH = oldPath ?? "";
  }
});

test("start: a healthy start returns started with the built command", async () => {
  const dir = tmpDir();
  const bin = fakeBinary(dir, true);
  const logPath = path.join(dir, "server.log");
  let spawned: string[][] = [];
  let pings = 0;
  const launcher = new ServerLauncher({
    logPath,
    timeoutMs: 1000,
    pingFn: async () => {
      pings += 1;
      // The pre-check ping is false; the first poll ping answers.
      return pings >= 2;
    },
    spawnFn: (command) => {
      spawned.push(command);
      return fakeChild();
    },
  });
  const outcome = await launcher.start("http://127.0.0.1:18765", () => bin);
  assert.equal(outcome.kind, "started");
  const started = outcome as { port: number; webUi: boolean; healthy: boolean };
  assert.equal(started.port, 18765);
  assert.equal(started.webUi, true);
  assert.equal(started.healthy, true);
  assert.deepEqual(spawned[0]?.slice(0, 3), [bin, "--port", "18765"]);
  assert.ok(spawned[0]?.includes("--web-dist"));
});

test("start: a process that exits during startup fails with its log tail", async () => {
  const dir = tmpDir();
  const bin = fakeBinary(dir);
  const logPath = path.join(dir, "server.log");
  fs.writeFileSync(logPath, "panic: something broke\n");
  const launcher = new ServerLauncher({
    logPath,
    timeoutMs: 1000,
    pingFn: async () => false,
    spawnFn: () => fakeChild(1),
  });
  const outcome = await launcher.start("http://127.0.0.1:18765", () => bin);
  assert.equal(outcome.kind, "failed");
  const failed = outcome as { message: string };
  assert.match(failed.message, /退出/);
  assert.match(failed.message, /something broke/);
});

test("start: 'address already in use' is named instead of a bare exit code", async () => {
  const dir = tmpDir();
  const bin = fakeBinary(dir);
  const logPath = path.join(dir, "server.log");
  fs.writeFileSync(logPath, "listen tcp: address already in use\n");
  const launcher = new ServerLauncher({
    logPath,
    timeoutMs: 1000,
    pingFn: async () => false,
    spawnFn: () => fakeChild(1),
  });
  const outcome = await launcher.start("http://127.0.0.1:18765", () => bin);
  assert.equal(outcome.kind, "failed");
  assert.match((outcome as { message: string }).message, /端口 18765 已被其他进程占用/);
});

test("start: an alive-but-silent process is not reported as healthy", async () => {
  const dir = tmpDir();
  const bin = fakeBinary(dir);
  const logPath = path.join(dir, "server.log");
  const launcher = new ServerLauncher({
    logPath,
    timeoutMs: 60,
    pingFn: async () => false,
    sleepFn: async (ms) => new Promise((r) => setTimeout(r, ms)),
    spawnFn: () => fakeChild(),
  });
  const outcome = await launcher.start("http://127.0.0.1:18765", () => bin);
  assert.equal(outcome.kind, "started");
  assert.equal((outcome as { healthy: boolean }).healthy, false);
});

test("stopOwned: kills only the process this launcher started", async () => {
  const dir = tmpDir();
  const bin = fakeBinary(dir);
  const logPath = path.join(dir, "server.log");
  const killed: string[] = [];
  let child: ChildProcess | null = null;
  let pings = 0;
  const launcher = new ServerLauncher({
    logPath,
    timeoutMs: 1000,
    pingFn: async () => {
      // The pre-check ping is false; the first poll ping answers.
      pings += 1;
      return pings >= 2;
    },
    spawnFn: () => {
      const c = fakeChild();
      child = c;
      (c as unknown as { kill: () => boolean }).kill = () => {
        killed.push("kill");
        return true;
      };
      return c;
    },
  });
  // A launcher that never started anything: stopOwned is a no-op.
  const idle = new ServerLauncher({ pingFn: async () => false });
  idle.stopOwned();

  const outcome = await launcher.start("http://127.0.0.1:18765", () => bin);
  assert.equal(outcome.kind, "started");
  assert.ok(child);
  launcher.stopOwned();
  assert.deepEqual(killed, ["kill"]);
  // Stopping twice must not kill again.
  launcher.stopOwned();
  assert.deepEqual(killed, ["kill"]);
});
