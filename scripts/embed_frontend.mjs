#!/usr/bin/env node
/**
 * Copy the built frontend into desktop/ui/ so `go:embed all:ui` can embed it.
 *
 * The Go shell serves its own pages (it registers a mygo:// handler to attach
 * the Content-Security-Policy, which takes precedence over mygo's built-in
 * frontend handler). That means a portable build has to carry the pages inside
 * the executable, and the files have to be in desktop/ui/ before `go build`.
 *
 * Run this before every `go build` that is meant to be portable:
 *
 *   node scripts/embed_frontend.mjs
 *   cd desktop && go build -o finesub-review-desktop.exe .
 *
 * ui/ is cleaned first: a stale asset from an earlier build would otherwise be
 * embedded and served alongside the new ones, and Vite's file names are hashed,
 * so leftovers never get overwritten.
 */
import { cp, mkdir, readdir, rm, stat } from "node:fs/promises";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const dist = join(root, "frontend", "dist");
const target = join(root, "desktop", "ui");
const keep = ".gitkeep";

async function exists(path) {
  try {
    await stat(path);
    return true;
  } catch {
    return false;
  }
}

async function main() {
  if (!(await exists(join(dist, "index.html")))) {
    console.error(`没有找到前端构建产物：${dist}\n请先运行 pnpm --dir frontend build`);
    process.exit(1);
  }

  await mkdir(target, { recursive: true });

  // Clean, keeping .gitkeep: `go:embed all:ui` needs the directory to hold a
  // file in a checkout that has not embedded anything.
  let removed = 0;
  for (const entry of await readdir(target)) {
    if (entry === keep) continue;
    await rm(join(target, entry), { recursive: true, force: true });
    removed += 1;
  }

  await cp(dist, target, { recursive: true });

  const files = [];
  async function walk(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) await walk(path);
      else files.push(relative(target, path));
    }
  }
  await walk(target);
  files.sort();

  if (!files.includes("index.html")) {
    console.error("拷贝后 ui/ 里没有 index.html，前端构建产物不完整");
    process.exit(1);
  }

  const bytes = (
    await Promise.all(
      files.filter((f) => f !== keep).map(async (f) => (await stat(join(target, f))).size),
    )
  ).reduce((sum, size) => sum + size, 0);

  console.log(`cleaned ${removed} stale entr${removed === 1 ? "y" : "ies"} from desktop/ui/`);
  console.log(`embedded ${files.filter((f) => f !== keep).length} files (${(bytes / 1024).toFixed(1)} KiB) from frontend/dist`);
  for (const file of files) {
    if (file !== keep) console.log(`  ${file}`);
  }
}

await main();
