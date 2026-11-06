import { test } from 'node:test';
import assert from 'node:assert/strict';
import { releaseMetadata } from './release_config.mjs';
const config = { version: '0.4.1', updates: { tagPrefix: 'desktop-v' } };
const pkg = { version: '0.4.1' };
const source = 'const ( version = "0.4.1" )';
const notes = '# Changes\n\n## 0.4.1 — 2026-11-06\n\n- Updated\n\n## 0.4.0 — older\n\n- Old';
test('release metadata selects only the matching release notes', () => {
  assert.deepEqual(releaseMetadata(config, pkg, source, notes, 'desktop-v0.4.1'),
    { version: '0.4.1', tag: 'desktop-v0.4.1', notes: '- Updated' });
});
test('reject mismatched executable, package and tag versions', () => {
  assert.throws(() => releaseMetadata(config, { version: '0.4.0' }, source, notes));
  assert.throws(() => releaseMetadata(config, pkg, 'version = "0.4.0"', notes));
  assert.throws(() => releaseMetadata(config, pkg, source, notes, 'desktop-v0.4.2'));
});
test('reject missing and empty release notes', () => {
  assert.throws(() => releaseMetadata(config, pkg, source, '# Nothing'));
  assert.throws(() => releaseMetadata(config, pkg, source, '## 0.4.1 — date\n\n## 0.4.0 — date\n- old'));
});
