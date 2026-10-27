// Run against a newly created, loopback-only database; never accepts a service URL.
import { createRequire } from 'node:module';
import { mkdtemp, mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = resolve(import.meta.dirname, '..');
await mkdir(join(root, 'test-results'), { recursive: true });
const evidence = await mkdtemp(join(root, 'test-results', 'workbench-'));
const server = createServer();
await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
const port = server.address().port;
await new Promise(resolve => server.close(resolve));
const base = `http://127.0.0.1:${port}`;
const passphrase = randomUUID(), password = randomUUID();
const python = process.env.REVIEW_TEST_PYTHON || join(root, '.venv', process.platform === 'win32' ? 'Scripts/python.exe' : 'bin/python');
const child = spawn(python, [join(root, 'scripts/workbench_fixture.py')], { cwd: root, windowsHide: true,
  env: { ...process.env, PYTHONDONTWRITEBYTECODE: '1', PYTHONUNBUFFERED: '1', COOKIE_SECURE: 'false', REVIEW_DATA_DIR: evidence,
    REVIEW_TEST_PORT: String(port), REVIEW_TEST_PASSPHRASE: passphrase, REVIEW_TEST_PASSWORD: password }, stdio: ['ignore', 'pipe', 'pipe'] });
let logs = ''; child.stderr.on('data', d => logs += d); child.stdout.on('data', d => logs += d);
let browser, page;
const checks = [], errors = [], responses = [];
async function capture(name) {
  await page.locator('.toast').waitFor({ state: 'hidden' });
  await page.evaluate(() => scrollTo({ top: 0, behavior: 'instant' }));
  await page.screenshot({ path: join(evidence, name), fullPage: true, animations: 'disabled' });
}
const document = name => `# ${name}\n节目中需要核实的用语。\n\n## 档案\n本名: ${name}\n类型: 梗\n\n## 用法\n- 来源：第 25 期 12:34。\n\n## 元数据\n最近更新日期: 2026-10-22\n`;
try {
  for (let i = 0; i < 100; i++) {
    if (child.exitCode !== null) throw new Error('Fixture failed: ' + logs);
    if (await fetch(base + '/api/health').then(r => r.ok).catch(() => false)) break;
    await new Promise(resolve => setTimeout(resolve, 150));
  }
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}) });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  page = await context.newPage();
  page.on('pageerror', e => errors.push(e.message));
  page.on('response', r => { if (r.url().includes('/api/')) responses.push([r.status(), r.url()]); });
  await page.goto(base);
  await page.getByLabel('访问口令', { exact: true }).waitFor();
  await capture('login-desktop.png');
  await page.getByLabel('访问口令', { exact: true }).fill(passphrase);
  await page.getByRole('button', { name: '进入知识库', exact: true }).click();
  await page.getByRole('button', { name: '管理员登录', exact: true }).click();
  await page.getByLabel('管理员账号', { exact: true }).fill('review-test-admin');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await page.getByRole('heading', { name: '工作台', exact: true }).waitFor();
  checks.push('成员进入与管理员登录');
  await page.getByRole('button', { name: '导入知识库', exact: true }).first().click();
  await page.getByRole('button', { name: /上传.*知识|新建导入|上传文件/ }).first().click();
  await capture('import-dialog.png');
  await page.getByLabel('批次名称').fill('连续审核验收');
  await page.locator('input[type=file]:not([webkitdirectory])').setInputFiles(['问候语甲', '问候语乙', '问候语丙'].map(name => ({ name: name + '.md', mimeType: 'text/markdown', buffer: Buffer.from(document(name)) })));
  await page.getByRole('button', { name: '上传并预检', exact: true }).click();
  await page.getByRole('button', { name: '确认导入', exact: true }).first().waitFor();
  await page.getByRole('button', { name: '确认导入', exact: true }).first().click();
  await capture('import-confirm.png');
  await page.locator('dialog').getByRole('button', { name: '确认导入', exact: true }).click();
  await page.getByRole('button', { name: '审核这一批' }).click();
  await page.getByText('当前导入批次', { exact: true }).waitFor();
  await page.getByLabel('分类筛选').selectOption('common');
  await page.getByRole('button', { name: '开始审核', exact: true }).click();
  await page.getByRole('button', { name: '作出决定', exact: true }).waitFor();
  checks.push('真实文件上传、worker 预检、确认导入、按批次进入审核');
  const firstUrl = page.url();
  const firstTitle = await page.locator('.review-heading h1').textContent();
  await page.getByRole('button', { name: '修订', exact: true }).click();
  await capture('revision-desktop.png');
  await page.getByLabel('Markdown 正文').fill(document(firstTitle.trim()) + '\n- 草稿来源补充。\n');
  await page.getByRole('button', { name: '版本差异', exact: true }).click();
  await page.getByLabel('对比版本', { exact: true }).selectOption('published');
  await page.getByRole('button', { name: '修订', exact: true }).click();
  assert.match(await page.getByLabel('Markdown 正文').inputValue(), /草稿来源补充/);
  assert.equal(await page.evaluate(() => window.dispatchEvent(new Event('desktop-before-update', { cancelable: true }))), true);
  assert.equal(await page.evaluate(() => {
    const setItem = Storage.prototype.setItem;
    try {
      Storage.prototype.setItem = () => { throw new DOMException('Test quota', 'QuotaExceededError'); };
      return window.dispatchEvent(new Event('desktop-before-update', { cancelable: true }));
    } finally { Storage.prototype.setItem = setItem; }
  }), false);
  await page.evaluate(() => sessionStorage.clear());
  await page.reload();
  await page.getByRole('button', { name: '修订', exact: true }).click();
  assert.match(await page.getByLabel('Markdown 正文').inputValue(), /草稿来源补充/);
  assert.equal(await page.getByRole('button', { name: '作出决定', exact: true }).isDisabled(), true);
  // Simulate another administrator revising while this editor holds an older draft.
  const me = await (await context.request.get(base + '/api/auth/me')).json();
  const entryId = firstUrl.match(/review\/([^?]+)/)[1];
  const detail = await (await context.request.get(base + '/api/entries/' + entryId)).json();
  const concurrent = await context.request.post(base + '/api/entries/' + entryId + '/revisions', { headers: { 'X-CSRF-Token': me.csrf }, data: { base_revision: detail.entry.id, content: document(firstTitle.trim()) + '\n- 并行修订。\n', source: '并行管理员' } });
  assert.equal(concurrent.status(), 200);
  await page.getByRole('button', { name: '保存为新版本', exact: true }).click();
  await page.getByText('内容已被其他管理员修改，请刷新后合并', { exact: true }).waitFor();
  assert.match(await page.getByLabel('Markdown 正文').inputValue(), /草稿来源补充/);
  await page.reload();
  await page.getByRole('button', { name: '修订', exact: true }).click();
  await page.getByRole('button', { name: '已合并，继续修订', exact: true }).waitFor();
  assert.equal(await page.getByRole('button', { name: '保存为新版本', exact: true }).isDisabled(), true);
  page.once('dialog', d => d.accept());
  await page.getByRole('button', { name: '丢弃草稿', exact: true }).click();
  await page.getByRole('button', { name: '版本差异', exact: true }).click();
  await capture('review-desktop.png');
  checks.push('持久草稿、对比不覆盖编辑、409 冲突保护、旧基线提示');
  await page.getByRole('button', { name: '作出决定', exact: true }).click();
  await page.getByLabel('决定理由').fill('已核对节目原句与对应时间码。');
  await capture('decision-dialog.png');
  await page.getByRole('button', { name: '确认并继续', exact: true }).click();
  await page.waitForURL(url => url.href !== firstUrl);
  assert.match(page.url(), /batch_id=/);
  assert.match(page.url(), /category=common/);
  await page.getByLabel('意见说明').fill('需要核对正式字幕的写法。');
  const secondUrl = page.url();
  await page.getByRole('button', { name: '提交并继续', exact: true }).click();
  await page.waitForURL(url => url.href !== secondUrl);
  await page.getByRole('button', { name: '作出决定', exact: true }).waitFor();
  checks.push('管理员裁决与成员记录后继续同一筛选队列');
  await page.getByRole('button', { name: '返回审核队列', exact: true }).click();
  assert.match(page.url(), /batch_id=/);
  assert.equal(await page.getByLabel('分类筛选').inputValue(), 'common');
  await Promise.all([page.waitForResponse(r => r.url().includes('reviewed=no') && r.status() === 200), page.getByRole('button', { name: '我未审核', exact: true }).click()]);
  await page.screenshot({ path: join(evidence, 'queue-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: /发布已通过知识/ }).click();
  await page.getByLabel(/选择全部可发布变更/).check();
  await page.getByRole('button', { name: /创建发布 ·/ }).click();
  await page.getByLabel('版本名称').fill('审核流程验收发布');
  await capture('publish-dialog.png');
  await page.getByRole('button', { name: '确认并生成发布包' }).click();
  await page.getByRole('link', { name: '知识库包', exact: true }).waitFor({ timeout: 15000 });
  const download = page.waitForEvent('download');
  await page.getByRole('link', { name: '知识库包', exact: true }).click();
  await (await download).saveAs(join(evidence, 'review-release.zip'));
  checks.push('发布条件检查、worker 生成正式包、真实下载');
  await page.goto(base + '/#/dashboard');
  await page.getByRole('heading', { name: '工作台', exact: true }).waitFor();
  await page.locator('.workflow-overview').waitFor();
  await page.locator('.toast').waitFor({ state: 'hidden' });
  await capture('dashboard-desktop.png');
  for (const width of [900, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(firstUrl);
    await page.locator('.review-heading h1').waitFor();
    await capture(`review-${width}.png`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `overflow at ${width}px`);
    if (!await page.locator('.review-metadata').evaluate(el => el.open))
      await page.getByText('来源、负责人和版本历史', { exact: true }).click();
    assert.equal(await page.getByLabel('分配审核负责人').isVisible(), true);
    await capture(`review-${width}-metadata.png`);
    await page.getByText('来源、负责人和版本历史', { exact: true }).click();
  }
  checks.push('1440 / 900 / 390 像素布局、窄屏来源与版本历史');
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(base + '/#/settings');
  await page.getByRole('button', { name: '知识库设置', exact: true }).click();
  const profile = page.getByLabel('导出兼容规则');
  assert.match(await profile.textContent(), /FineSub 0\.6\.0 · schema 6/);
  assert.match(await profile.textContent(), /FineSub 0\.5\.1 · schema 5/);
  await profile.selectOption('finesub-0.6.0');
  await Promise.all([page.waitForResponse(r => r.url().endsWith('/api/settings') && r.request().method() === 'PUT' && r.status() === 200), page.getByRole('button', { name: '保存设置', exact: true }).click()]);
  await page.reload();
  await page.getByRole('button', { name: '知识库设置', exact: true }).click();
  assert.equal(await page.getByLabel('导出兼容规则').inputValue(), 'finesub-0.6.0');
  await capture('schema6-settings.png');
  await page.goto(base + '/#/imports');
  await page.getByRole('button', { name: /上传.*知识|新建导入|上传文件/ }).first().click();
  await page.getByLabel('批次名称').fill('schema 6 页面验收');
  const modern = '# 新版知识验收\n新版事实短句验收。\n\n## 档案\n- [本名] 新版知识验收\n- [别名] 新版验收\n\n## 背景\n<!-- 人改版历史仅作提示 -->\n- [说明] 乐队成员来自同一所学校。\n\n## 元数据\n最近更新日期: 2026-10-22\n';
  await page.locator('input[type=file]:not([webkitdirectory])').setInputFiles({ name: '新版知识验收.md', mimeType: 'text/markdown', buffer: Buffer.from(modern) });
  await page.getByRole('button', { name: '上传并预检', exact: true }).click();
  await page.getByRole('button', { name: '确认导入', exact: true }).first().waitFor();
  await page.getByRole('button', { name: '确认导入', exact: true }).first().click();
  await page.locator('dialog').getByRole('button', { name: '确认导入', exact: true }).click();
  await page.getByRole('button', { name: '审核这一批' }).click();
  await page.getByRole('button', { name: '开始审核', exact: true }).click();
  await page.locator('.review-heading h1').filter({ hasText: '新版知识验收' }).waitFor();
  await page.getByRole('button', { name: '修订', exact: true }).click();
  assert.match(await page.getByLabel('Markdown 正文').inputValue(), /来自同一所学校/);
  await page.getByLabel('Markdown 正文').fill(modern.replace('同一所学校', '同一所高中'));
  await page.getByRole('button', { name: '保存为新版本', exact: true }).click();
  await page.getByRole('button', { name: '作出决定', exact: true }).waitFor({ state: 'visible' });
  await page.getByRole('button', { name: '作出决定', exact: true }).click();
  await page.getByLabel('决定理由').fill('已核对新版事实短句。');
  const [decision] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/entries/') && r.url().endsWith('/decision') && r.request().method() === 'POST'),
    page.locator('dialog').getByRole('button', { name: /确认并继续|确认决定/ }).click(),
  ]);
  assert.equal(decision.status(), 200);
  await page.locator('dialog').waitFor({ state: 'hidden' });
  await capture('schema6-review.png');
  checks.push('schema 5/6 设置、刷新持久化、新版事实短句上传/修订/裁决');
  assert.deepEqual(errors, []);
  await writeFile(join(evidence, 'result.json'), JSON.stringify({ checks, pageErrors: errors }, null, 2));
  console.log(JSON.stringify({ status: 'PASS', evidence, checks }, null, 2));
} catch (error) {
  await page?.screenshot({ path: join(evidence, 'failure.png'), fullPage: true }).catch(() => {});
  await writeFile(join(evidence, 'failure.txt'), String(error) + '\n' + JSON.stringify({ errors, responses }, null, 2) + '\n' + logs);
  console.error('Evidence:', evidence);
  throw error;
} finally {
  await browser?.close();
  if (child.exitCode === null) {
    if (process.platform === 'win32') await new Promise(resolve => spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' }).once('close', resolve));
    else child.kill('SIGTERM');
  }
}
