// These fixtures exercise release verification and staging, not a running app.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { copyFile, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const source = resolve(import.meta.dirname);
const hash = (data) => createHash("sha256").update(data).digest("hex");

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "finesub-release-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const folder = join(root, "desktop/build/windows-amd64");
  await mkdir(folder, { recursive: true });
  await mkdir(join(root, "scripts"));
  for (const name of ["verify_desktop.mjs", "package_desktop.mjs"]) {
    await copyFile(join(source, name), join(root, "scripts", name));
  }
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const config = {
    version: "0.5.0",
    updates: {
      publicKey: publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64"),
      github: "example/review",
      tagPrefix: "desktop-v",
    },
  };
  await writeFile(join(root, "desktop/mygo.json"), JSON.stringify(config));
  await writeFile(join(root, "CHANGELOG.md"), "# Changes\n\n## 0.5.0 — test fixture\n");

  // Minimal PE headers, not an executable program or evidence of UI rendering.
  const exe = Buffer.alloc(1024 * 1024);
  exe.write("MZ");
  exe.writeUInt32LE(128, 60);
  exe.write("PE\0\0", 128);
  exe.writeUInt16LE(0x8664, 132);
  exe.writeUInt16LE(0x20b, 152);
  const archive = Buffer.from("signed archive fixture");
  const archiveName = "finesub-review-0.5.0-windows-amd64.tar.gz";
  const manifest = {
    version: "0.5.0",
    url: `https://github.com/example/review/releases/download/desktop-v0.5.0/${archiveName}`,
    size: archive.length,
    signature: sign(null, createHash("sha256").update(archive).digest(), privateKey).toString("base64"),
  };
  const files = {
    "FineSub Review.exe": exe,
    [archiveName]: archive,
    "finesub-review-setup-0.5.0-amd64.exe": Buffer.from("installer fixture"),
    "install.ps1": Buffer.from("# installer fixture\n"),
    "update-windows-amd64.json": Buffer.from(JSON.stringify(manifest)),
  };
  for (const [name, bytes] of Object.entries(files)) await writeFile(join(folder, name), bytes);
  await writeFile(join(root, "desktop/build/SHA256SUMS"), Object.entries(files).map(([name, bytes]) => `${hash(bytes)}  windows-amd64/${name}\n`).join(""));
  return { root, folder, archiveName, manifest, files };
}

function run(f, script) {
  return spawnSync(process.execPath, [join(f.root, "scripts", script)], { encoding: "utf8" });
}

test("native release verification accepts signed artifacts without HTML markers", async (t) => {
  const f = await fixture(t);
  const result = run(f, "verify_desktop.mjs");
  assert.equal(result.status, 0, result.stderr);
});

test("changed update archive is rejected by signature verification", async (t) => {
  const f = await fixture(t);
  const changed = Buffer.from(f.files[f.archiveName]);
  changed[0] ^= 1;
  await writeFile(join(f.folder, f.archiveName), changed);
  const result = run(f, "verify_desktop.mjs");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /signature does not verify/);
});

test("a valid filename on a different release source is rejected", async (t) => {
  const f = await fixture(t);
  f.manifest.url = f.manifest.url.replace("example/review", "elsewhere/review");
  await writeFile(join(f.folder, "update-windows-amd64.json"), JSON.stringify(f.manifest));
  const result = run(f, "verify_desktop.mjs");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /configured repository/);
});

test("checksums may not silently omit the portable executable", async (t) => {
  const f = await fixture(t);
  await writeFile(join(f.root, "desktop/build/SHA256SUMS"), `${hash(f.files[f.archiveName])}  ${f.archiveName}\n`);
  const result = run(f, "verify_desktop.mjs");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /SHA256SUMS must include FineSub Review/);
});

test("staging checksums cover every distributed attachment", async (t) => {
  const f = await fixture(t);
  const result = run(f, "package_desktop.mjs");
  assert.equal(result.status, 0, result.stderr);
  const folder = join(f.root, "output/desktop-0.5.0");
  const checksums = (await readFile(join(folder, "SHA256SUMS"), "utf8")).trim().split("\n");
  const files = (await readdir(folder)).filter((name) => name !== "SHA256SUMS");
  assert.equal(checksums.length, files.length);
  for (const name of files) {
    assert.ok(checksums.includes(`${hash(await readFile(join(folder, name)))}  ${name}`), name);
  }
  const verified = spawnSync(process.execPath, [join(f.root, "scripts/verify_desktop.mjs"), folder], { encoding: "utf8" });
  assert.equal(verified.status, 0, verified.stderr);
});

test("staging rejects unexpected files instead of publishing stale attachments", async (t) => {
  const f = await fixture(t);
  const folder = join(f.root, "output/desktop-0.5.0");
  await mkdir(folder, { recursive: true });
  await writeFile(join(folder, "old-version.exe"), "old build");
  const result = run(f, "package_desktop.mjs");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Unexpected file/);
});
