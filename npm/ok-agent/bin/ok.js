#!/usr/bin/env node
// OK agent — platform-native binary launcher.
// npm installs one optional platform package; this script finds it,
// execs it, and streams stdio transparently.

import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const { platform, arch } = process;

const map = {
  "darwin-arm64":  "@ok-agent/cli-darwin-arm64",
  "darwin-x64":    "@ok-agent/cli-darwin-x64",
  "linux-arm64":   "@ok-agent/cli-linux-arm64",
  "linux-x64":     "@ok-agent/cli-linux-x64",
  "win32-arm64":   "@ok-agent/cli-win32-arm64",
  "win32-x64":     "@ok-agent/cli-win32-x64",
};

const key = `${platform}-${arch === "x64" ? "x64" : arch}`;
const pkg = map[key];
if (!pkg) {
  console.error(`ok: no prebuilt binary for ${platform}/${arch}. Try "go install github.com/NB-Agent/ok/cmd/ok@latest".`);
  process.exit(1);
}

// Resolve the optional dependency — npm places it next to the main package.
let binDir;
try {
  // The optional dep's package.json sits under node_modules/<pkg>/; from
  // there we reach into its own node_modules/.bin-like layout.
  const modRoot = join(HERE, "..", "..", pkg);
  // Prebuilt binaries ship under bin/ inside the dep.
  binDir = join(modRoot, "bin");
} catch {
  // Fallback: resolve from this package's own node_modules (npm workspaces).
  binDir = join(HERE, "..", "node_modules", pkg, "bin");
}

const exe = platform === "win32" ? "ok.exe" : "ok";
const exePath = join(binDir, exe);

if (!existsSync(exePath)) {
  console.error(`ok: binary not found at ${exePath}. Try reinstalling: npm i -g ok-agent`);
  process.exit(1);
}

try {
  execFileSync(exePath, process.argv.slice(2), { stdio: "inherit" });
} catch (e) {
  process.exit(e.status ?? 1);
}
