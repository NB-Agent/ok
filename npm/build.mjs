// OK agent npm build & publish script.
// Cross-compiles the Go binary for 6 platform targets, stages each as a
// separate npm package, then publishes the main + sub-packages.
//
// Usage:
//   node npm/build.mjs v1.0.1            # dry-run stage
//   node npm/build.mjs v1.0.1 --publish  # stage + publish to npm
//
// Requires Go in PATH and NPM_TOKEN in the environment for publish.
import { execFileSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..");
const STAGE = join(HERE, ".stage");

const TARGETS = [
  { node: "darwin-arm64", goos: "darwin", goarch: "arm64" },
  { node: "darwin-x64",   goos: "darwin", goarch: "amd64" },
  { node: "linux-arm64",  goos: "linux",  goarch: "arm64" },
  { node: "linux-x64",    goos: "linux",  goarch: "amd64" },
  { node: "win32-arm64",  goos: "windows",goarch: "arm64" },
  { node: "win32-x64",    goos: "windows",goarch: "amd64" },
];

const tag = process.argv[2] ?? process.env.GITHUB_REF_NAME;
if (!tag) {
  console.error("usage: node npm/build.mjs <tag>   (e.g. v1.0.1)");
  process.exit(1);
}
const version = tag.replace(/^v/, "");
const publish = process.argv.includes("--publish");

rmSync(STAGE, { recursive: true, force: true });
mkdirSync(STAGE, { recursive: true });

const subPackages = [];
for (const t of TARGETS) {
  const name = `@ok-agent/cli-${t.node}`;
  const dir = join(STAGE, `cli-${t.node}`);
  const exe = t.goos === "windows" ? "ok.exe" : "ok";
  mkdirSync(join(dir, "bin"), { recursive: true });

  console.log(`build ${t.goos}/${t.goarch} -> ${name}`);
  execFileSync(
    "go",
    [
      "build",
      "-trimpath",
      "-ldflags", `-s -w -X main.version=${tag}`,
      "-o", join(dir, "bin", exe),
      "./cmd/ok",
    ],
    {
      cwd: ROOT,
      stdio: "inherit",
      env: { ...process.env, CGO_ENABLED: "0", GOOS: t.goos, GOARCH: t.goarch },
    },
  );

  writeFileSync(
    join(dir, "package.json"),
    JSON.stringify({
      name,
      version,
      description: `OK agent prebuilt binary for ${t.node}.`,
      os: [t.goos === "windows" ? "win32" : t.goos],
      cpu: [t.goarch === "amd64" ? "x64" : "arm64"],
      files: ["bin/"],
      license: "Apache-2.0",
      repository: {
        type: "git",
        url: "git+https://github.com/NB-Agent/ok.git",
      },
    }, null, 2) + "\n",
  );
  subPackages.push({ name, dir });
}

const mainDir = join(STAGE, "ok-agent");
mkdirSync(mainDir, { recursive: true });
cpSync(join(HERE, "ok-agent", "bin"), join(mainDir, "bin"), { recursive: true });
if (existsSync(join(ROOT, "README.md"))) {
  cpSync(join(ROOT, "README.md"), join(mainDir, "README.md"));
}

const mainPkg = JSON.parse(
  readFileSync(join(HERE, "ok-agent", "package.json"), "utf8"),
);
mainPkg.version = version;
for (const key of Object.keys(mainPkg.optionalDependencies)) {
  mainPkg.optionalDependencies[key] = version;
}
writeFileSync(
  join(mainDir, "package.json"),
  JSON.stringify(mainPkg, null, 2) + "\n",
);

if (!publish) {
  console.log(`\nstaged ${version} in ${STAGE} (dry run; pass --publish to publish)`);
  process.exit(0);
}

// Publish under 'next' dist-tag by default; use 'latest' for stable releases.
const distTag = version.includes("-") ? "next" : "latest";
const publishArgs = ["publish", "--access", "public", "--tag", distTag];

for (const sub of subPackages) {
  console.log(`publish ${sub.name}@${version} (${distTag})`);
  execFileSync("npm", publishArgs, { cwd: sub.dir, stdio: "inherit" });
}
console.log(`publish ok-agent@${version} (${distTag})`);
execFileSync("npm", publishArgs, { cwd: mainDir, stdio: "inherit" });
