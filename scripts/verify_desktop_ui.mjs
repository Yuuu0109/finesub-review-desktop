// Browser checks of the desktop update dialog, driven through a mocked
// `window.mygo` runtime: the page code runs for real, no native code does.
//
// What this can prove: the dialog states, the wording, which calls the page makes
// and in what order, and that the page never installs or restarts on its own.
// What it cannot prove: that Go verifies the signature or replaces the files —
// that lives in desktop/main.go and desktop/main_test.go.
//
// Needs the built frontend (frontend/dist) and Playwright:
//   PLAYWRIGHT_MODULE=<path to playwright or playwright-core> [PLAYWRIGHT_EXECUTABLE=<chromium>] \
//     node scripts/verify_desktop_ui.mjs
import { createRequire } from 'node:module';
import { createServer } from 'node:http';
import { readFile, mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { resolve, join, extname, sep } from 'node:path';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = resolve(import.meta.dirname, '..'), dist = join(root, 'frontend/dist');
// The version comes from the desktop manifest, which is what the page reads
// through shell.Version; the update the mock offers is the next patch. Keeping it
// out of this file means a release bump needs no edit here, and there is no
// fourth copy of the number to forget.
const { version } = JSON.parse(await readFile(join(root, 'desktop/mygo.json'), 'utf8'));
const [major, minor, patch] = version.split('.');
const newer = `${major}.${minor}.${Number(patch) + 1}`;
const serverAddress = 'https://review.example.test';
const routeKey = `${serverAddress}:finesub-update-route`;
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
const errors = [], checks = [], notes = [];
try {
  browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE } : {}),
    ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {})
  });
  page = await browser.newPage({ viewport: { width: 1200, height: 900 } });
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(({ version, newer, serverAddress }) => {
    localStorage.setItem('finesub-review.server', serverAddress);
    // The scenario the mock answers with, and what the page was seen asking for.
    window.scenario = { check: 'new', install: 'ok' };
    window.counts = { startup: 0, check: 0, install: 0, apply: 0 };
    window.requests = [];
    const listeners = new Map();
    window.emitGo = (name, payload) => listeners.get(name)?.(payload);

    const toBase64 = text => btoa(String.fromCharCode(...new TextEncoder().encode(text)));

    // A channel as the bridge hands it to Go: values arrive in order, the reader
    // takes them one at a time, and the end of the queue ends the iteration.
    // @mygo-plugins/fetch passes one to plugin:fetch.Fetch for the response body.
    function channel() {
      const queued = [], waiting = [];
      let ended = false;
      const end = () => {
        if (ended) return;
        ended = true;
        for (const resolve of waiting.splice(0)) resolve({ value: undefined, done: true });
      };
      return {
        close: end,
        push(value) {
          if (ended) return;
          if (waiting.length > 0) waiting.shift()({ value, done: false });
          else queued.push({ value, done: false });
        },
        end,
        [Symbol.asyncIterator]: () => ({
          next() {
            if (queued.length > 0) return Promise.resolve(queued.shift());
            if (ended) return Promise.resolve({ value: undefined, done: true });
            return new Promise(resolve => waiting.push(resolve));
          }
        })
      };
    }

    // The reply CheckUpdate/CheckUpdateStartup resolve with. Up to date is null,
    // because Go returns a nil *UpdateInfo and the bridge marshals that as JSON
    // null; a zero struct would arrive as a truthy object with an empty version
    // number, which the dialog would show as "新版本" with a download button.
    // That Go side is pinned by TestCheckUpdateReturnsNilWhenUpToDate.
    function checkAnswer() {
      if (window.scenario.check === 'rate-limit')
        throw new Error('更新源暂时拒绝访问：GitHub 接口对同一地址每小时限 60 次，本次请求已用尽。请约一小时后重试；当前版本可正常使用: mygo: checking for updates: GET https://api.github.com/repos/x/y/releases: 403 Forbidden');
      if (window.scenario.check === 'network')
        throw new Error('无法连接更新源，请检查网络后重试；如使用代理或企业网络，可能无法访问 api.github.com: mygo: checking for updates: Get "https://api.github.com/repos/x/y/releases": dial tcp 20.205.243.168:443: i/o timeout');
      if (window.scenario.check === 'latest') return null;
      return { version: newer, notes: '本版说明仅用于验收。', prepared: false };
    }
    const gate = name => new Promise(resolve => { window[name] = resolve; });

    window.mygo = {
      locale: 'zh-CN',
      dir: 'ltr',
      preferences: {},
      channel,
      on: (name, fn) => { listeners.set(name, fn); return () => listeners.delete(name); },
      window: { minimize: async () => {}, toggleMaximize: async () => {}, isMaximized: async () => false, close: async () => {} },
      once: () => () => {},
      async call(method, ...args) {
        if (method === 'plugin:fetch.Fetch') {
          const [request, parts] = args;
          const path = new URL(request.url).pathname;
          window.requests.push(new URL(request.url).pathname + new URL(request.url).search);
          const answer = (status, statusText, body) => {
            parts.push({ status, statusText, url: request.url, headers: [['Content-Type', 'application/json']] });
            parts.push({ chunk: toBase64(JSON.stringify(body)) });
            parts.end();
          };
          if (path === '/api/bootstrap') {
            // Held until the test releases it, so the update check is observed
            // while the shell is still connecting.
            if (!window.connectionFinished)
              await new Promise(resolve => { window.finishConnect = () => { window.connectionFinished = true; resolve(); }; });
            answer(200, 'OK', { initialized: true, access_configured: true });
            return;
          }
          const user = { id: 'test-admin', name: '美香', username: 'mika', role: 'admin' };
          if (path === '/api/auth/enter') { window.signedIn = true; answer(200, 'OK', { user, csrf: 'fixture' }); return; }
          if (window.signedIn) {
            const entry = { id: 'revision-1', entry_id: 'entry-1', title: '木漏れ日', summary: '树叶间洒落的阳光', category: 'common', kind: '用语', number: 1, status: 'pending', review_count: 2, created_at: '2026-11-06T10:00:00Z', updated_at: '2026-11-06T10:00:00Z' };
            const data = {
              '/api/users': [user], '/api/notices': [], '/api/settings': { name: 'FineSub 知识库' },
              '/api/auth/me': { user, csrf: 'fixture' },
              '/api/dashboard': { total: 128, counts: { pending: 24, needs_changes: 6 }, pending_publish: 12, reviews: 86, recent_batches: [] },
              '/api/entries': { items: [entry], total: 1 },
            };
            if (path in data) { answer(200, 'OK', data[path]); return; }
          }
          answer(401, 'Unauthorized', { detail: '请先输入访问口令' });
          return;
        }
        if (method === 'shell.Version') return version;
        if (method === 'shell.CheckUpdateStartup') { window.counts.startup++; await gate('finishStartupCheck'); return checkAnswer(); }
        if (method === 'shell.CheckUpdate') { window.counts.check++; return checkAnswer(); }
        if (method === 'shell.DownloadUpdate') {
          window.counts.install++;
          await gate('finishInstall');
          if (window.scenario.install === 'signature')
            throw new Error(`更新包签名校验未通过，未安装任何内容，当前版本未改变。请重试；若反复失败请到发布页手动下载: mygo: installing the update: the update is not signed with the app's key`);
          return;
        }
        if (method === 'shell.ApplyUpdate') { window.counts.apply++; throw new Error('重启失败：无法启动新版本（模拟）'); }
        if (method === 'shell.SaveDownload') return 'C:\\Downloads\\saved.zip';
        if (method === 'shell.OpenExternal') return;
        throw new Error('Unexpected call: ' + method);
      }
    };
  }, { version, newer, serverAddress });

  const dialog = () => page.locator('.desktop-update-body').innerText();
  async function see(text) {
    try { await page.getByText(text, { exact: true }).first().waitFor({ timeout: 5000 }); }
    catch { throw new Error(`expected ${JSON.stringify(text)}; the dialog reads:\n${await dialog().catch(() => '(no dialog)')}`); }
  }
  async function alert(text) {
    try { await page.getByRole('alert').filter({ hasText: text }).first().waitFor({ timeout: 5000 }); }
    catch { throw new Error(`expected an alert with ${JSON.stringify(text)}; the dialog reads:\n${await dialog().catch(() => '(no dialog)')}`); }
  }
  const gated = name => page.waitForFunction(n => typeof window[n] === 'function', name).then(() => page.evaluate(n => window[n](), name));
  const click = name => (name === '关闭' ? page.locator('dialog[open]') : page).getByRole('button', { name, exact: true }).click();

  await page.goto(`http://127.0.0.1:${server.address().port}`);
  // The shell checks in the background at launch, before it has connected: the
  // button says so while the answer is outstanding.
  await click(`v${version} · 检查中`);
  await see('正在检查更新');
  await click('关闭');
  assert.equal(await page.locator('dialog[open]').count(), 0);
  await gated('finishStartupCheck');
  await click(`v${version} · 有新版本`);
  assert.equal(await page.evaluate(() => window.counts.install), 0, 'the page must not download on its own');
  await click('关闭');
  assert.equal(await page.locator('dialog[open]').count(), 0);

  await gated('finishConnect');
  await page.getByLabel('访问口令', { exact: true }).waitFor();
  // Connecting mounts the app; it must not re-run the check or forget its result.
  assert.equal(await page.evaluate(() => window.counts.startup), 1, 'the launch check must run once');
  await click(`v${version} · 有新版本`);
  checks.push('启动时后台检查、连接前后保留同一更新状态、不自动下载');

  await page.locator('.update-version b').filter({ hasText: `v${newer}` }).waitFor();
  await click('下载并安装');
  await page.waitForFunction(() => typeof window.finishInstall === 'function');
  await see('正在下载');
  await page.evaluate(() => window.emitGo('desktop-update-progress', { downloaded: 5242880, total: 10485760 }));
  assert.equal(await page.locator('progress').getAttribute('value'), '50');
  await see('5.0 MB / 10.0 MB');
  // Delta fallback resets both the byte counter and the new total.
  await page.evaluate(() => window.emitGo('desktop-update-progress', { downloaded: 0, total: 20971520 }));
  assert.equal(await page.locator('progress').getAttribute('value'), '0');
  await page.evaluate(() => window.emitGo('desktop-update-progress', { downloaded: 20971520, total: 20971520 }));
  await see('正在校验并安装');
  assert.equal(await page.getByRole('button', { name: '关闭弹窗', exact: true }).isDisabled(), true);
  assert.equal(await page.getByRole('button', { name: '关闭', exact: true }).last().isDisabled(), true);
  checks.push('真实事件订阅更新进度、增量回退归零、100%进入验签状态');
  assert.equal(await page.locator('progress').count(), 1);
  assert.equal(await page.getByRole('button', { name: '下载并安装', exact: true }).isDisabled(), true);
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('dialog[open]').count(), 1, 'closing must be refused while installing');
  checks.push('下载中按钮禁用、Esc 不能关闭弹窗');

  await page.evaluate(() => window.scenario.install = 'signature');
  await gated('finishInstall');
  await alert('签名校验未通过');
  await alert('未安装任何内容，当前版本未改变');
  assert.match(await page.locator('.update-version p').innerText(), new RegExp(`v${version}`), 'the version shown must stay the installed one');
  checks.push('安装失败提示「未安装任何内容，当前版本未改变」，版本号不变');

  await page.evaluate(() => window.scenario.check = 'rate-limit');
  await click('重新检查');
  await alert('更新源暂时拒绝访问');
  await page.evaluate(() => window.scenario.check = 'network');
  await click('重新检查');
  await alert('无法连接更新源');
  await page.evaluate(() => window.scenario.check = 'latest');
  await click('重新检查');
  // Go answers "up to date" with the zero value; the page must read that as no
  // update at all, and offer nothing to install.
  await see('已是最新版本');
  assert.equal(await page.getByRole('button', { name: '下载并安装', exact: true }).count(), 0);
  checks.push('限流、网络错误各自可读，重试后回到已是最新');

  await page.evaluate(() => { window.scenario.check = 'new'; window.scenario.install = 'ok'; });
  await click('重新检查');
  await page.locator('.update-version b').filter({ hasText: `v${newer}` }).waitFor();
  await click('下载并安装');
  await page.waitForFunction(() => typeof window.finishInstall === 'function');
  await gated('finishInstall');
  await see('已安装 · 重启生效');
  assert.equal(await page.evaluate(() => window.counts.apply), 0, 'installing must not restart by itself');
  assert.equal(await page.getByRole('button', { name: '稍后重启', exact: true }).count(), 1);
  checks.push('已安装状态保留，用户选择稍后重启或立即重启');

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
  await page.screenshot({ path: join(evidence, 'update-installed.png'), fullPage: true });

  // A pending draft cancels the restart: the event is cancelable and the page
  // must not go on to call ApplyUpdate when it was cancelled.
  await page.evaluate(() => {
    window.blockUpdate = event => event.preventDefault();
    window.addEventListener('desktop-before-update', window.blockUpdate);
  });
  await click('立即重启');
  await alert('操作未完成或草稿未能保存');
  assert.equal(await page.evaluate(() => window.counts.apply), 0);
  await page.evaluate(() => {
    window.removeEventListener('desktop-before-update', window.blockUpdate);
    location.hash = '#/review/test-entry?status=pending';
  });
  await click('立即重启');
  await alert('重启失败');
  assert.equal(await page.evaluate(() => window.counts.apply), 1);
  assert.equal(await page.evaluate(key => localStorage.getItem(key), routeKey), '#/review/test-entry?status=pending');
  await page.screenshot({ path: join(evidence, 'restart-failed.png'), fullPage: true });
  checks.push('草稿未保存时阻止重启、重启前记住路由并在失败后可恢复');

  await click('稍后重启');
  await click(`v${version} · 重启更新`);
  await see('已安装 · 重启生效');
  assert.equal(await page.evaluate(() => window.counts.install), 2, 'reopening must not install again');
  await click('稍后重启');
  await page.evaluate(() => { location.hash = '#/dashboard'; });
  await page.getByLabel('访问口令', { exact: true }).fill('test-passphrase');
  await click('进入知识库');
  await page.getByRole('heading', { name: '工作台', exact: true }).waitFor();
  await page.screenshot({ path: join(evidence, 'dashboard.png'), fullPage: true });
  await page.keyboard.press('Control+k');
  assert.equal(await page.getByRole('textbox', { name: '搜索知识库', exact: true }).evaluate(el => el === document.activeElement), true);
  const before = await page.evaluate(() => window.requests.length);
  await page.getByRole('textbox', { name: '搜索知识库', exact: true }).fill('木漏れ日');
  assert.equal(await page.evaluate(() => window.requests.length), before, 'typing should not send requests');
  await page.keyboard.press('Enter');
  await page.getByRole('heading', { name: '知识库', exact: true }).waitFor();
  await page.waitForFunction(() => window.requests.some(p => p.includes('/api/entries?') && new URLSearchParams(p.split('?')[1]).get('q') === '木漏れ日'));
  await page.getByRole('button', { name: '木漏れ日', exact: true }).waitFor();
  for (const [width, height] of [[1280, 860], [900, 640], [390, 844]]) {
    await page.setViewportSize({ width, height });
    const overflow = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth,
      elements: [...document.querySelectorAll('body *')].filter(el => el.getBoundingClientRect().right > innerWidth + 1)
        .map(el => ({ tag: el.tagName, className: String(el.className), right: el.getBoundingClientRect().right })).slice(0, 25) }));
    assert.ok(overflow.scrollWidth <= width, `overflow at ${width}: ${JSON.stringify(overflow)}`);
    await page.screenshot({ path: join(evidence, `entries-${width}.png`), fullPage: true });
  }
  checks.push('Ctrl+K聚焦、提交搜索、1280/900/390布局无页面横向溢出');
  assert.deepEqual(errors, []);
  await writeFile(join(evidence, 'result.json'), JSON.stringify({
    mode: 'mocked window.mygo, no native execution', version, offered: newer, checks, notes, pageErrors: errors
  }, null, 2));
  console.log(JSON.stringify({ status: 'PASS', version, offered: newer, evidence, checks, notes, nativeReplacement: 'NOT TESTED' }, null, 2));
} catch (error) {
  await page?.screenshot({ path: join(evidence, 'failure.png'), fullPage: true }).catch(() => {});
  await writeFile(join(evidence, 'failure.txt'), String(error) + '\n' + JSON.stringify(errors));
  console.error('Evidence:', evidence); throw error;
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}

