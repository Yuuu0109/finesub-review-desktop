/**
 * Stage a MyGo desktop build for release.
 *
 * `mygo build` already writes everything a release needs into
 * desktop/build/windows-amd64/: the portable executable, the NSIS installer,
 * the tar.gz the updater installs from, the signed update manifest, install.ps1
 * and SHA256SUMS. This script only:
 *   - checks the signed manifest agrees with desktop/mygo.json;
 *   - confirms CHANGELOG.md has a section for the version, since `mygo build`
 *     takes the release notes from it;
 *   - copies the artifacts into output/desktop-<version>/, the folder the
 *     release instructions upload from.
 *
 * Run `mygo build` first (with MYGO_UPDATER_PRIVATE_KEY set to sign).
 */
import { copyFile, mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const config = JSON.parse(await readFile(join(root, "desktop/mygo.json"), "utf8"));
const version = config.version;
if (!/^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/.test(version)) throw new Error("Invalid release version");

// mygo build reads the release notes from this section, so a missing one means
// a release with empty notes.
const changelog = await readFile(join(root, "CHANGELOG.md"), "utf8");
const escaped = version.replace(/\./g, "\\.");
if (!new RegExp(`^## (?:桌面版 )?${escaped} —`, "m").test(changelog)) {
  throw new Error(`CHANGELOG.md has no section for ${version}`);
}

const source = join(root, "desktop", "build", "windows-amd64");
const out = join(root, "output", `desktop-${version}`);

let entries;
try {
  entries = await readdir(source);
} catch {
  throw new Error(`No build at ${source}; run \`mygo build\` in desktop/ first`);
}
if (!entries.includes("update-windows-amd64.json")) {
  throw new Error("The build has no update manifest; it was not signed (set MYGO_UPDATER_PRIVATE_KEY)");
}
const manifest = JSON.parse(await readFile(join(source, "update-windows-amd64.json"), "utf8"));
if (manifest.version !== version) throw new Error(`Update manifest is ${manifest.version}, expected ${version}`);
for (const name of ["FineSub Review.exe", `finesub-review-${version}-windows-amd64.tar.gz`, `finesub-review-setup-${version}-amd64.exe`, "install.ps1"]) {
  if (!entries.includes(name)) throw new Error(`Missing release artifact: ${name}`);
}

await mkdir(out, { recursive: true });
// Do not silently publish files left by an older staging operation.
for (const name of await readdir(out)) {
  if (name !== "SHA256SUMS" && !entries.includes(name)) throw new Error(`Unexpected file in release directory: ${name}`);
}
const staged = [];
for (const name of entries.sort()) {
  await copyFile(join(source, name), join(out, name));
  staged.push(name);
}
// Cover every distributed attachment, including the update manifest and script.
// A checksum file cannot contain its own checksum.
const sums = [];
for (const name of staged) {
  const digest = createHash("sha256").update(await readFile(join(out, name))).digest("hex");
  sums.push(`${digest}  ${name}`);
}
await writeFile(join(out, "SHA256SUMS"), sums.join("\n") + "\n");
console.log(`Prepared MyGo desktop release ${version}: ${out}`);
for (const line of sums) console.log(`  ${line}`);

