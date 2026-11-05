#!/usr/bin/env node
// Keep the shipped VSIX able to run `repotory-init` in ANY workspace.
//
// repotory-init is declared `"private": true` and nothing publishes or installs
// it globally, so the built extension could only ever find it inside the Cinder
// repository itself. Every real user — working in their own project — got "the
// one-command installer lives in the Cinder repository", which made
// `Cinder: Register MCP Server` dead code outside this repo.
//
// The single source of truth stays at ../../scripts/repotory-init/index.mjs; this
// copies it beside the compiled output so `vsce package` bundles it. Run from the
// `compile` script, so it cannot be forgotten. CI re-runs it and fails on drift.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const src = path.resolve(here, "..", "..", "..", "scripts", "repotory-init", "index.mjs");
const dest = path.resolve(here, "..", "repotory-init", "index.mjs");

if (!fs.existsSync(src)) {
  console.error(`sync-init: source not found: ${src}`);
  process.exit(1);
}

fs.mkdirSync(path.dirname(dest), { recursive: true });
const next = fs.readFileSync(src);
const prev = fs.existsSync(dest) ? fs.readFileSync(dest) : null;
if (prev?.equals(next)) {
  console.log("sync-init: repotory-init/index.mjs already up to date");
} else {
  fs.writeFileSync(dest, next);
  console.log(`sync-init: copied ${path.relative(process.cwd(), src)} -> ${path.relative(process.cwd(), dest)}`);
}
