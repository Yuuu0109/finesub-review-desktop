// Browser checks of desktop UI state only; native EXE replacement is tested separately.
import { createRequire } from 'node:module';
import { createServer } from 'node:http';
import { readFile, mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { resolve, join, extname, sep } from 'node:path';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = resolve(import.meta.dirname, '..'), dist = join(root, 'frontend/dist');
await mkdir(join(root, 'test-results'), { recursive: true });
const evidence = await mkdtemp(join(root, 'test-results/desktop-ui-'));
const server = createServer(async (request, response) => {
  const pathname = new URL(request.url, 'http://localhost').pathname;
  const path = resolve(dist, '.' + (pathname === '/' ? '/index.html' : decodeURIComponent(pathname)));
  if (!path.startsWith(dist + sep)) { response.writeHead(403).end(); return; }
  try {
    response.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png' })[extname(path)] || 'application/octet-stream');
    response.end(await readFile(path));
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
let browser, page;
const errors = [], checks = [];
try {
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}) });
  page = await browser.newPage({ viewport: { width: 1200, height: 900 } });
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(() => {
    window.isTauri = true;
    localStorage.setItem('finesub-review.server', 'https://review.example.test');
    window.updateMode = 'new'; window.downloadMode = 'fail';
    window.checkCount = 0; window.downloadCount = 0; window.applyCount = 0;
    const requests = new Map(), bodies = new Map(); let counter = 0;
    window.__TAURI_INTERNALS__ = { transformCallback: () => 1, unregisterCallback: () => {}, invoke: async (command, args) => {
      if (command === 'plugin:app|version') return '0.3.0';
      if (command === 'plugin:http|fetch') { requests.set(++counter, args.clientConfig); return counter; }
      if (command === 'plugin:http|fetch_send') {
        const request = requests.get(args.rid), path = new URL(request.url).pathname;
        if (path === '/api/bootstrap' && !window.connectionFinished) {
          await new Promise(resolve => window.finishConnect = () => { window.connectionFinished = true; resolve(); });
        }
        const ok = path === '/api/bootstrap';
        bodies.set(args.rid, Array.from(new TextEncoder().encode(JSON.stringify(ok ? { initialized: true, access_configured: true } : { detail: '请先输入访问口令' }))));
        return { status: ok ? 200 : 401, statusText: ok ? 'OK' : 'Unauthorized', url: request.url, headers: [['Content-Type', 'application/json']], rid: args.rid };
      }
      if (command === 'plugin:http|fetch_read_body') {
        const bytes = bodies.get(args.rid); bodies.delete(args.rid); return bytes ? [...bytes, 0] : [1];
      }
      if (command === 'plugin:http|fetch_cancel' || command === 'plugin:http|fetch_cancel_body') return;
      if (command === 'check_desktop_update') {
        window.checkCount++;
        if (window.checkCount === 1) await new Promise(resolve => window.finishCheck = resolve);
        if (window.updateMode === 'error') throw new Error('更新源暂不可用，请稍后重试');
        return window.updateMode === 'new' ? { version: '0.3.1', notes: '更新交互验收', prepared: false } : null;
      }
      if (command === 'download_desktop_update') {
        window.downloadCount++;
        args.onProgress.onmessage({ downloaded: 50, total: 100 });
        await new Promise(resolve => window.finishDownload = resolve);
        if (window.downloadMode === 'fail') throw new Error('下载或签名校验失败，当前程序未改变');
        args.onProgress.onmessage({ downloaded: 100, total: 100 });
        return;
      }
      if (command === 'apply_desktop_update') {
        window.applyCount++;
        throw new Error('未能替换当前程序，可重试或另存新版：模拟文件占用');
      }
      if (command === 'save_desktop_update') return 'C:\\Downloads\\FineSub-Review-0.3.1-portable.exe';
      throw new Error('Unexpected IPC: ' + command);
    }};
  });
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.getByRole('button', { name: 'v0.3.0 · 检查中', exact: true }).click();
  await page.getByText('正在检查更新…', { exact: true }).waitFor();
  await page.getByRole('button', { name: '稍后', exact: true }).click();
  await page.evaluate(() => window.finishCheck());
  await page.getByRole('button', { name: 'v0.3.0 · 有新版本', exact: true }).waitFor();
  assert.equal(await page.locator('dialog').count(), 0);
  assert.equal(await page.evaluate(() => window.downloadCount), 0);
  await page.evaluate(() => window.finishConnect());
  await page.getByLabel('访问口令', { exact: true }).waitFor();
  assert.equal(await page.evaluate(() => window.checkCount), 1);
  assert.equal(await page.getByRole('button', { name: 'v0.3.0 · 有新版本', exact: true }).count(), 1);
  checks.push('后台检查、连接前后保留同一更新状态、不自动下载');
  await page.getByRole('button', { name: 'v0.3.0 · 有新版本', exact: true }).click();
  await page.getByText('新版本 0.3.1', { exact: true }).waitFor();
  await page.getByRole('button', { name: '下载更新', exact: true }).click();
  await page.getByText('正在下载 50%', { exact: true }).waitFor();
  assert.equal(await page.getByRole('button', { name: '下载更新', exact: true }).isDisabled(), true);
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('dialog[open]').count(), 1);
  await page.evaluate(() => window.finishDownload());
  await page.getByRole('alert').filter({ hasText: '签名校验失败' }).waitFor();
  await page.evaluate(() => window.updateMode = 'error');
  await page.getByRole('button', { name: '重新检查', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: '更新源暂不可用' }).waitFor();
  await page.evaluate(() => window.updateMode = 'latest');
  await page.getByRole('button', { name: '重新检查', exact: true }).click();
  await page.getByText('已是最新版本。', { exact: true }).waitFor();
  checks.push('进度、操作互斥、签名失败、网络错误、重试与最新版本状态');
  await page.evaluate(() => { window.updateMode = 'new'; window.downloadMode = 'success'; });
  await page.getByRole('button', { name: '重新检查', exact: true }).click();
  await page.getByRole('button', { name: '下载更新', exact: true }).click();
  await page.getByText('正在下载 50%', { exact: true }).waitFor();
  await page.evaluate(() => window.finishDownload());
  await page.getByText('更新已通过签名校验', { exact: true }).waitFor();
  assert.equal(await page.evaluate(() => window.applyCount), 0);
  await page.getByRole('button', { name: '稍后', exact: true }).click();
  await page.getByRole('button', { name: 'v0.3.0 · 重启更新', exact: true }).click();
  const styles = await page.evaluate(() => {
    const pane = document.querySelector('.desktop-update-body');
    const button = document.querySelector('.desktop-update-body .button.primary');
    return { pane: pane.outerHTML.slice(0, 180), display: getComputedStyle(pane).display,
      font: getComputedStyle(pane).fontSize, buttonPadding: getComputedStyle(button).padding,
      sheets: Array.from(document.styleSheets).map(s => s.href) };
  });
  await writeFile(join(evidence, 'styles.json'), JSON.stringify(styles, null, 2));
  assert.equal(styles.display, 'grid', 'Desktop CSS must load with the desktop entry');
  assert.ok(parseFloat(styles.font) >= 13, 'Dialog must not inherit the compact title bar font');
  assert.ok(parseFloat(styles.buttonPadding.split(' ')[1]) >= 12, 'Dialog buttons need normal padding');
  await page.screenshot({ path: join(evidence, 'update-ready.png'), fullPage: true });
  await page.evaluate(() => {
    window.blockUpdate = event => event.preventDefault();
    window.addEventListener('desktop-before-update', window.blockUpdate);
  });
  await page.getByRole('button', { name: '重启更新', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: '草稿未能保存' }).waitFor();
  assert.equal(await page.evaluate(() => window.applyCount), 0);
  await page.evaluate(() => {
    window.removeEventListener('desktop-before-update', window.blockUpdate);
    location.hash = '#/review/test-entry?status=pending';
  });
  await page.getByRole('button', { name: '重启更新', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: '模拟文件占用' }).waitFor();
  assert.equal(await page.evaluate(() => localStorage.getItem('https://review.example.test:finesub-update-route')), '#/review/test-entry?status=pending');
  assert.equal(await page.evaluate(() => window.applyCount), 1);
  await page.getByRole('button', { name: '另存新版', exact: true }).click();
  await page.getByText(/已另存新版：.*FineSub-Review-0.3.1-portable.exe/).waitFor();
  assert.equal(await page.getByRole('button', { name: '重启更新', exact: true }).isEnabled(), true);
  await page.screenshot({ path: join(evidence, 'update-fallback.png'), fullPage: true });
  checks.push('下载后确认重启、草稿失败阻止更新、恢复路由、替换失败可另存');
  assert.deepEqual(errors, []);
  await writeFile(join(evidence, 'result.json'), JSON.stringify({ mode: 'mock IPC, no native execution', checks, pageErrors: errors }, null, 2));
  console.log(JSON.stringify({ status: 'PASS', evidence, checks, nativeReplacement: 'NOT TESTED' }, null, 2));
} catch (error) {
  await page?.screenshot({ path: join(evidence, 'failure.png'), fullPage: true }).catch(() => {});
  await writeFile(join(evidence, 'failure.txt'), String(error) + '\n' + JSON.stringify(errors));
  console.error('Evidence:', evidence); throw error;
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
