#!/usr/bin/env node
/**
 * Verify a MyGo desktop build before it can be distributed. No private key needed.
 *
 * Checks, in the order a release would be compromised:
 *   1. the manifest names the version in mygo.json;
 *   2. the archive it points at exists and has the size it claims;
 *   3. the Ed25519 signature of the archive's SHA-256 verifies against the
 *      public key in mygo.json — the key compiled into the app;
 *   4. a tampered archive does not verify;
 *   5. the portable executable is a Windows x64 PE that embeds the frontend;
 *   6. SHA256SUMS lists the artifacts, with the right digests.
 *
 * Usage: node scripts/verify_desktop.mjs [folder]
 *        (default: desktop/build/windows-amd64)
 */
import assert from "node:assert/strict";
import { createHash, createPublicKey, verify } from "node:crypto";
import { readFile, readdir, stat } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const config = JSON.parse(await readFile(join(root, "desktop/mygo.json"), "utf8"));
const folder = resolve(process.argv[2] || join(root, "desktop", "build", "windows-amd64"));

const publicKey = config.updates?.publicKey;
assert.ok(publicKey, "mygo.json has no updates.publicKey");

const manifest = JSON.parse(await readFile(join(folder, "update-windows-amd64.json"), "utf8"));
assert.match(manifest.version, /^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/);

// 1. The artifact is the version the source says it is.
const allowPrevious = process.argv.includes("--allow-previous-version");
if (!allowPrevious) {
  assert.equal(
    manifest.version,
    config.version,
    `Artifact version ${manifest.version} differs from mygo.json ${config.version}`,
  );
}

// 2. The archive the manifest names.
const archiveName = `finesub-review-${manifest.version}-windows-amd64.tar.gz`;
assert.equal(
  decodeURIComponent(manifest.url.split("/").pop()),
  archiveName,
  `Manifest points at ${manifest.url}, expected ${archiveName}`,
);
const archive = await readFile(join(folder, archiveName));
assert.equal(archive.length, manifest.size, `Archive is ${archive.length} bytes, manifest says ${manifest.size}`);

// 3. The signature over its SHA-256, with the key compiled into the app.
//    Node wants an SPKI structure around the raw 32-byte Ed25519 key.
const raw = Buffer.from(publicKey, "base64");
assert.equal(raw.length, 32, `Public key is ${raw.length} bytes, not a raw Ed25519 key`);
const spkiPrefix = Buffer.from("302a300506032b6570032100", "hex");
const key = createPublicKey({ key: Buffer.concat([spkiPrefix, raw]), format: "der", type: "spki" });
const digest = createHash("sha256").update(archive).digest();
const signature = Buffer.from(manifest.signature, "base64");
assert.equal(signature.length, 64, `Signature is ${signature.length} bytes, not an Ed25519 signature`);
assert.ok(verify(null, digest, key, signature), "The manifest signature does not verify against updates.publicKey");

// 4. A tampered archive must not verify: this is what stops a substituted download.
const tampered = Buffer.from(archive);
tampered[Math.floor(tampered.length / 2)] ^= 0xff;
const tamperedDigest = createHash("sha256").update(tampered).digest();
assert.ok(!verify(null, tamperedDigest, key, signature), "A tampered archive still verified");

// 5. The portable executable itself.
const exeName = "FineSub Review.exe";
const exe = await readFile(join(folder, exeName));
assert.ok(exe.length >= 1024 * 1024, `${exeName} is only ${exe.length} bytes`);
assert.equal(exe.subarray(0, 2).toString(), "MZ", `${exeName} is not a Windows executable`);
const pe = exe.readUInt32LE(60);
assert.ok(pe <= exe.length - 26, `${exeName} has a PE header outside the file`);
assert.equal(exe.subarray(pe, pe + 4).toString(), "PE\0\0");
assert.equal(exe.readUInt16LE(pe + 4), 0x8664, `${exeName} is not Windows x64`);
assert.equal(exe.readUInt16LE(pe + 24), 0x20b, `${exeName} is not PE32+`);

// The shell embeds its pages: it serves them itself through a mygo:// handler so
// it can attach the CSP, and a portable copy must carry them inside the binary.
//
// The markers must be strings only the frontend brings. "FineSub" is NOT one of
// them: it appears in main.go's appName const and in the window title, so it is
// present even in a build with no pages at all — a check on it would never fail,
// which is worse than no check. These two were verified against a build whose
// ui/ held only .gitkeep:
//   assets/  the directory Vite emits its output into
//   index-   Vite's content-hashed entry name, e.g. assets/index-B8OPzfxW.js
const frontendMarkers = ["assets/", "index-"];
const missing = frontendMarkers.filter((marker) => !exe.includes(Buffer.from(marker, "utf8")));
assert.equal(
  missing.length,
  0,
  `${exeName} does not embed the frontend (missing ${missing.join(", ")}); run node scripts/embed_frontend.mjs before go build`,
);

// 6. SHA256SUMS covers the artifacts it lists, with the right digests.
const sumsPath = join(folder, "..", "SHA256SUMS");
const sums = (await readFile(sumsPath, "utf8")).trim().split(/\r?\n/);
const listed = new Map();
for (const line of sums) {
  const match = /^([a-f0-9]{64}) [ *](.+)$/.exec(line);
  assert.ok(match, `Invalid checksum line: ${line}`);
  assert.ok(!listed.has(match[2]), `Duplicate checksum entry: ${match[2]}`);
  listed.set(match[2], match[1]);
}
for (const [name, expected] of listed) {
  const candidates = [join(folder, name), join(folder, "..", name)];
  let found = null;
  for (const candidate of candidates) {
    try {
      if ((await stat(candidate)).isFile()) {
        found = candidate;
        break;
      }
    } catch {
      // try the next location
    }
  }
  assert.ok(found, `SHA256SUMS lists ${name}, which is not in the build`);
  const actual = createHash("sha256").update(await readFile(found)).digest("hex");
  assert.equal(actual, expected, `Checksum mismatch for ${name}`);
}

const artifacts = (await readdir(folder)).sort();
console.log(`verified ${manifest.version} from ${folder}`);
console.log(`  archive   ${archiveName} (${(archive.length / 1048576).toFixed(1)} MiB, signature ok)`);
console.log(`  portable  ${exeName} (${(exe.length / 1048576).toFixed(1)} MiB, Windows x64 PE, frontend embedded)`);
console.log(`  checksums ${listed.size} entries in SHA256SUMS`);
console.log(`  files     ${artifacts.join(", ")}`);
