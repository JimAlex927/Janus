// Shared POSIX/PowerShell build. Refreshes the tracked Go embed before compiling.
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const frontend = join(root, "frontend");
function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, stdio: "inherit", ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status}): ${result.stderr || "see output above"}`);
  return result.stdout?.trim();
}
const capture = (command, args) => run(command, args, { stdio: "pipe", encoding: "utf8" });
const sha256 = path => createHash("sha256").update(readFileSync(path)).digest("hex");
let base = (process.env.JANUS_UI_BASE_URL || "").trim();
if (base && (!/^\/[A-Za-z0-9._/-]*$/.test(base) || base.includes("//") || base.split("/").some(s => s === "." || s === ".."))) {
  throw new Error("JANUS_UI_BASE_URL must be an absolute path without empty or dot segments");
}
base = base.replace(/\/$/, "");
const goos = process.env.GOOS || capture("go", ["env", "GOOS"]);
const goarch = process.env.GOARCH || capture("go", ["env", "GOARCH"]);
let output = resolve(process.env.JANUS_OUTPUT || join(root, "bin", "janus"));
if (goos === "windows" && !output.endsWith(".exe")) output += ".exe";
const stage = mkdtempSync(join(tmpdir(), "janus-build-"));
const ldflags = `-s -w -buildid= -X janus/internal/admin.uiBaseURL=${base}`;
try {
  // Only constant arguments go through Windows' npm command interpreter.
  if (process.env.JANUS_INSTALL_DEPS === "1" || !existsSync(join(frontend, "node_modules"))) {
    run(process.platform === "win32" ? "npm.cmd" : "npm", ["ci"], { cwd: frontend, shell: process.platform === "win32" });
  }
  run(process.execPath, [join(frontend, "node_modules/typescript/bin/tsc"), "-b"], { cwd: frontend });
  const ui = join(stage, "ui");
  run(process.execPath, [join(frontend, "node_modules/vite/bin/vite.js"), "build", "--outDir", ui], { cwd: frontend });
  // Refresh the tracked embed so plain `go build` / `go run` serve the same UI.
  const embed = join(root, "internal", "admin", "ui");
  rmSync(join(embed, "assets"), { recursive: true, force: true });
  mkdirSync(join(embed, "assets"), { recursive: true });
  cpSync(ui, embed, { recursive: true });
  const source = join(stage, "source");
  mkdirSync(source);
  for (const entry of ["go.mod", "go.sum", "cmd", "internal", "pkg"]) {
    if (existsSync(join(root, entry))) cpSync(join(root, entry), join(source, entry), {
      recursive: true, filter: path => path !== join(root, "internal/admin/ui"),
    });
  }
  cpSync(ui, join(source, "internal/admin/ui"), { recursive: true });
  mkdirSync(dirname(output), { recursive: true });
  run("go", ["build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", output, "./cmd/janus"], {
    cwd: source, env: { ...process.env, CGO_ENABLED: "0", GOOS: goos, GOARCH: goarch },
  });
  if (process.env.JANUS_UPX === "1") run("upx", ["--best", "--lzma", output]);
  function hashTree(dir, treeRoot = dir, hashes = {}) {
    for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) hashTree(path, treeRoot, hashes);
      else hashes[relative(treeRoot, path).replaceAll("\\", "/")] = sha256(path);
    }
    return hashes;
  }
  const assets = hashTree(ui);
  const sourceTreeSHA256 = createHash("sha256").update(JSON.stringify(hashTree(source))).digest("hex");
  const manifest = {
    commit: capture("git", ["rev-parse", "HEAD"]),
    dirty: capture("git", ["status", "--porcelain"]).length > 0,
    go: capture("go", ["version"]), node: process.version, goos, goarch,
    uiBase: base || "/", ldflags, cgoEnabled: false,
    upx: process.env.JANUS_UPX === "1", binarySHA256: sha256(output),
    frontendLockSHA256: sha256(join(frontend, "package-lock.json")),
    goSumSHA256: sha256(join(source, "go.sum")), sourceTreeSHA256, assets,
  };
  writeFileSync(`${output}.manifest.json`, JSON.stringify(manifest, null, 2) + "\n");
  console.log(`built ${output} (${statSync(output).size} bytes)\nsha256 ${manifest.binarySHA256}\nmanifest ${output}.manifest.json`);
} finally {
  rmSync(stage, { recursive: true, force: true });
}
