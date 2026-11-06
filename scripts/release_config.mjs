import assert from 'node:assert/strict';
import { readFile, writeFile, appendFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

// Run before building, so a mismatched tag never produces a signed release.
export function releaseMetadata(config, pkg, source, changelog, tag = '') {
  assert.match(config.version, /^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/, 'Invalid release version');
  assert.equal(pkg.version, config.version, 'Frontend and desktop versions differ');
  const compiled = source.match(/\bversion\s*=\s*"([^"]+)"/);
  assert.equal(compiled?.[1], config.version, 'Go and desktop versions differ');
  const releaseTag = `${config.updates.tagPrefix}${config.version}`;
  if (tag) assert.equal(tag, releaseTag, 'Tag and desktop version differ');
  const sections = changelog.split(/^## /m).slice(1);
  const section = sections.find(s => s.startsWith(`${config.version} —`));
  assert.ok(section, 'Missing release notes');
  const notes = section.slice(section.indexOf('\n') + 1).trim();
  assert.ok(notes, 'Empty release notes');
  return { version: config.version, tag: releaseTag, notes };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const root = resolve(import.meta.dirname, '..');
  const read = p => readFile(join(root, p), 'utf8');
  const [config, pkg, source, changelog] = await Promise.all([
    read('desktop/mygo.json'), read('frontend/package.json'), read('desktop/main.go'), read('CHANGELOG.md'),
  ]);
  const metadata = releaseMetadata(JSON.parse(config), JSON.parse(pkg), source, changelog,
    process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : '');
  await writeFile(join(root, 'release-notes.txt'), metadata.notes + '\n');
  if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, `tag=${metadata.tag}\nversion=${metadata.version}\n`);
  console.log(metadata.tag);
}
