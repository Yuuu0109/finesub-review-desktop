# 桌面版 0.4.0

2026-10-27。以下为本机对桌面客户端的实际执行结果，覆盖 Go 与前端测试、构建、图标与更新链路的可验证部分；已提交（`4ea7b16`），未推送、未发布 Release。

| 验证项 | 结果与边界 |
| --- | --- |
| Go 测试 | `go test -count=1 -v ./...`（`desktop/`）：10 项通过、0 失败、0 跳过 |
| 前端测试 | `pnpm --dir frontend test:desktop`：9 项通过、0 失败 |
| 前端构建 | `pnpm --dir frontend build`（`vue-tsc --noEmit` + Vite）通过 |
| 便携构建 | `node scripts/embed_frontend.mjs` → `go build -trimpath`：`desktop/ui/` 先清空再拷贝（保留 `.gitkeep`），Vite 旧哈希资源不残留；产物可启动，侧栏品牌图标正常 |
| 签名发布构建 | `mygo build`：产出 `desktop/build/windows-amd64/` 下的 EXE、NSIS 安装器、tar.gz、`update-windows-amd64.json`（Ed25519 已签名）和 `install.ps1`，以及 `desktop/build/SHA256SUMS` |
| 图标 | 图标位于 `frontend/src/assets/icons/`（`icon.ico`、`icon.png`、`mika-source.png`、`source.svg`）；MyGo 从 `mika-source.png` 生成 16–256 各尺寸并写入 EXE（exe 内 11 个 PNG 块） |
| 后端与云端 | 本轮**未**重跑后端回归、PostgreSQL 与容器验收。这些结果不受桌面客户端代码影响，但也没有在本轮复跑，不得当作本轮结果引用 |
| 真实更新链路 | **未验证**。需要先用 `desktop-v` 标签发布两个版本，才能在实机验证「旧版→新版」的应用内升级 |

本机证据：`desktop/build/windows-amd64/` 与 `desktop/build/SHA256SUMS`（不入库，`desktop/build/` 已在 `.gitignore`）。`desktop/ui/` 是 `frontend/dist` 的拷贝，同样不入库，只保留 `desktop/ui/.gitkeep` 供 `go:embed all:ui` 在干净检出中编译。免安装 EXE 实测 12,482,560 字节（约 11.9 MiB）。

`scripts/verify_desktop.mjs` 的内嵌前端判别：旧的 `exe.includes("FineSub")` **无效**——`FineSub` 来自 `desktop/main.go` 的 `const appName` 与窗口标题，无页面的构建里同样存在，评审与主程各自构建过只含 `.gitkeep` 的 EXE 对照确认。现改为同时检查 `assets/`（Vite 输出目录名）与 `index-`（Vite 内容哈希入口名），两者在无前端 EXE 中均不存在。改动这类检查串后必须做反向验证（让它查一个不存在的串，确认脚本变红），否则又交付一条永远绿的检查。

## 本批改动范围

本批为 0.4.0 的桌面客户端与相关页面调整，已随 `4ea7b16`（完整源码基线）提交，分支 `feat/desktop`。

桌面客户端相关：`.github/workflows/desktop.yml`、`.gitignore`、`.dockerignore`、`frontend/package.json`、`frontend/pnpm-lock.yaml`、`frontend/pnpm-workspace.yaml`、`scripts/package_desktop.mjs`、`scripts/verify_desktop.mjs`、`scripts/embed_frontend.mjs`。

前端与页面：`frontend/src/App.vue`、`frontend/src/DesktopRoot.vue`、`frontend/src/components/Brand.vue`、`frontend/src/components/DesktopUpdate.vue`、`frontend/src/components/DownloadLink.vue`、`frontend/src/main.ts`、`frontend/src/pages/Entries.vue`、`frontend/src/pages/Imports.vue`、`frontend/src/pages/ReviewPage.vue`、`frontend/src/style.css`、`frontend/src/transport.ts`、`frontend/src/assets/icons/`、`frontend/tests/transport.test.ts`、`frontend/tests/transport-browser.test.ts`。

`desktop/`：`go.mod`、`go.sum`、`main.go`、`main_test.go`、`mygo.json`、`ui/.gitkeep`（`ui/` 下其余内容是构建时从 `frontend/dist` 拷入的，不入库）。

上方 2026-10-22 的记录在此前已经作为 0.3.0 提交（未发布 Release），其内容保持原样作为历史。

# 审核流程与免安装更新 — 2026-10-22

代码基线为 `feat/desktop@f579532dbc06554834c95f94189cb3b4141e1380`，本轮修改版本为 0.3.0，当时尚未提交、推送、部署或发布 Release。测试只使用独立数据库、临时上传和知识库副本。

| 本轮验证 | 结果与边界 |
| --- | --- |
| 后端完整回归 | 91 项通过、无失败或跳过；包括审核权限/CSRF、版本冲突、筛选游标、关联删除及排队变化、导入/发布/回退、schema 5/6 隔离原生读写、证据与人工认可记录保持 |
| 前端单元测试 | 7 项通过；地址/传输边界、筛选与游标、关联组选择、持久存储与配额失败 |
| 类型检查与构建 | `pnpm build` 通过；修复条件动态加载遗漏桌面 CSS，拆开两个启动分支 |
| 真实浏览器业务流程 | 访问口令、管理员登录、上传/worker 预检/确认、批次与分类筛选、差异对比、草稿跨刷新恢复、409 冲突、裁决及意见后继续、worker 发布和 ZIP 下载全部通过，无页面异常 |
| 草稿与布局 | 实际存储失败会阻止更新；1440 / 900 / 390px 无页面水平溢出，窄屏可访问来源、负责人和历史版本，已检查截图 |
| 桌面更新界面 | 模拟 IPC 通过；连接前后保持同一更新状态、进度及互斥、验签失败、网络失败、重试、下载后确认重启、草稿阻止、页面恢复和另存回退；实际字体 14px、按钮内边距正常 |
| schema 6 页面与格式 | 实际页面切换 0.6.0、刷新后保留设置、上传 common 事实短句、修订和裁决通过；独立 0.5.1 规则仍拒绝只支持术语章节中的短句 |
| 页面文案 | 登录、工作台、导入、审核、发布、设置与更新弹窗移除宣传语、操作介绍和重复提示；状态、校验错误、冲突及删除/回退确认保留。登录与主要弹窗截图已复查 |
| schema 6 原生往返 | 0.6.0 原生编辑器创建隔离库，3 主体、15 正文节点、5 索引项；正文/索引语义一致、渲染重读幂等，style 不参加名称匹配 |
| schema 6 历史记录 | 三份真实旧条目在数据库副本上回写；另用原生引擎生成认可记录，验证旧 ack、未改行提示、待确认别名保持，审核值成为新人工版本；只改注释无新修订 |
| 版本与误用保护 | 发布包声明规则/schema/引擎，旧清单仍按 schema 5 读取；引擎、包、库错配及未知 schema 在打开原生写库前拒绝。dry-run 在 SQLite 快照执行 |
| 交付校验脚本 | 用已有 0.2.3 签名包验证 Windows x64、版本/地址、SHA-256、签名和篡改拒绝通过；这不是 0.3.0 产物验证 |
| Windows 原生程序 | **待验证**。本机未安装 Rust/MSVC；新依赖锁文件、原生测试、0.3.0 编译、真实原位置替换和重启需批准提交后由云端构建及实机验证 |

已补齐当前本机 schema 6 知识库的兼容验证。0.6.0 参考源码对应 `eb642f25e7bc83cb18c4e7a4e0289d410c643fbc`，schema、apply/envelope 和三份预设均逐文件核对该提交；新补丁只应用于隔离引擎。真实库通过 SQLite 在线备份生成测试快照，原有 204 个文件内容哈希保持；只读 SQLite 连接生成了空 WAL 辅助文件，没有修改知识数据。schema 5 使用历史副本独立回归，两版的三个样例核验声明均保持 6→6、10→10、7→7，节点类型、匹配项、归属边、核验/分享政策及证据历史保持。

注（2026-10-27）：上表「交付校验脚本」与「Windows 原生程序」两行按当前构建方式更新——本机用 `go build` / `mygo build` 直接构建，`scripts/verify_desktop.mjs` 校验产物（清单、Ed25519 验签、PE 头、内嵌前端、SHA256SUMS）。上表数值仍是 2026-10-22 当次执行记录，没有重跑。

文案精简后重新通过 7 项前端测试、类型检查、生产构建、真实浏览器业务流程及模拟 IPC 更新验证；未改后端逻辑，上表 91 项后端结果沿用本轮此前的 schema 6 完整回归。截图等待提示消失、回到页首并关闭过渡动画后保存。

本机证据：`test-results/workbench-295o2E/`（精简后真实页面、登录/导入/裁决/发布弹窗、发布包、schema 6 设置与审核截图）、`test-results/desktop-ui-2g0N4B/`（精简后模拟 IPC、截图与样式）、`test-results/native-preserved-finesub-0.5.1.json`、`test-results/native-preserved-finesub-0.6.0.json`、`test-results/native-schema6.json`、`test-results/native-schema6-human-marks.json`、`test-results/schema6-source-integrity.json`、`test-results/schema6-full.xml`。以上测试产物不提交到仓库。

## 待批准的提交范围（2026-10-22 历史批次，已提交）

以下包含本次追加兼容修改、文案精简和此前尚未提交的审核、桌面优化。分支 `feat/desktop`。**这一批已作为 0.3.0 提交，下面的清单是当时的分组记录，不再是待批准状态。** 测试产物、知识库快照、原生引擎副本和密钥均不在这份清单中。

### 1. 审核工作流与页面

建议提交信息：`[feat](review):优化连续审核与持久草稿`

整文件：

- `backend/app/services.py`
- `backend/app/worker.py`
- `backend/tests/conftest.py`
- `backend/tests/test_review_queue.py`
- `frontend/src/App.vue`
- `frontend/src/api.ts`
- `frontend/src/context.ts`
- `frontend/src/components/WelcomePanel.vue`
- `frontend/src/pages/Dashboard.vue`
- `frontend/src/pages/Entries.vue`
- `frontend/src/pages/Imports.vue`
- `frontend/src/pages/Releases.vue`
- `frontend/src/pages/ReviewPage.vue`
- `frontend/src/workflow.css`
- `frontend/src/workflow.ts`
- `frontend/tests/workflow.test.ts`
- `scripts/workbench_fixture.py`

按代码块：`backend/app/main.py` 的队列/对比/候选接口；`frontend/src/main.ts` 的工作流样式导入；`README.md` 与 `CHANGELOG.md` 的审核说明。验证依据为后端全量回归、前端测试及真实页面流程。服务端 API/worker 与前端需要配套更新。

### 2. 免安装程序更新

建议提交信息：`[feat](desktop):完善免安装签名更新与原位重启`

整文件（该批次已随 0.3.0 提交）：

- `.github/workflows/desktop.yml`
- `frontend/package.json`
- `frontend/src/DesktopRoot.vue`
- `frontend/src/components/DesktopUpdate.vue`
- `scripts/package_desktop.mjs`
- `scripts/verify_desktop.mjs`
- `scripts/verify_desktop_ui.mjs`

按代码块：`frontend/src/main.ts` 的入口加载修复；`DESKTOP.md`、`CHANGELOG.md` 的桌面更新说明。依赖第 1 组的持久草稿。前端构建、模拟 IPC 和已有包的验签检查通过；真实替换重启仍需实机验收，不能当成 EXE 交付完成。

注（2026-10-27）：清单中的 `scripts/verify_desktop_ui.mjs` 是按当时实现写的，当前客户端已改用 `window.mygo` 调用面，**该脚本正在重写、尚未重新执行，其结果不可引用**（详见本文件「复现浏览器检查」一节）。

### 3. FineSub 0.6.0 / schema 6 兼容

建议提交信息：`[feat](knowledge):支持schema6知识库与保留历史的兼容回写`

整文件：

- `backend/app/compat.py`
- `backend/app/node_compat.py`
- `backend/app/node_markdown.py`
- `backend/app/node_presets_0_6.json`
- `backend/tests/test_preserved_values.py`
- `backend/tests/test_schema6_knowledge.py`
- `backend/tests/native_schema6_probe.py`
- `frontend/src/pages/Settings.vue`
- `frontend/src/pages/Streamer.vue`
- `scripts/COMPATIBILITY.md`
- `scripts/apply_compat_release.py`
- `scripts/native_check.py`
- `scripts/preserved_values.py`
- `scripts/engine-preserved-values-0.6.0.patch`
- `scripts/verify_workbench.mjs`
- `VALIDATION.md`

按代码块：`backend/app/main.py` 的设置版本枚举；`README.md`、`DESKTOP.md`、`CHANGELOG.md` 的 schema 6 说明。浏览器验收脚本同时覆盖第 1 组，最终验证报告汇总三组，因此此组建议最后提交。91 项后端回归、两版原生副本回写及 schema 6 页面流程通过；应用到实际知识库仍需对应引擎补丁、备份和显式 `--apply`，没有自动迁移。

三个分组共享文件须按上述代码块暂存；批准前后均需重新核对差异，不能整文件混入其他组。原生 EXE 的构建/实机限制保持明确。用户可批准全部三组或指定编号；本清单本身不授权提交、推送、部署或公开 Release。

复现浏览器检查前先在 frontend 执行 `pnpm build`，然后在仓库根目录运行：

```powershell
# 使用已有 Playwright 与 Edge；PLAYWRIGHT_MODULE 可以指向本机已安装模块。
$env:PLAYWRIGHT_MODULE = '填写 Playwright 模块目录'
$env:PLAYWRIGHT_CHANNEL = 'msedge'
$env:PYTHONUTF8 = '1'
node scripts/verify_workbench.mjs
node scripts/verify_desktop_ui.mjs   # 正在按当前外壳重写，重写完成前不要当作已通过的验收
# `mygo build` 之后执行（读 desktop/build；package_desktop.mjs 只是复制到 output/）：
node scripts/verify_desktop.mjs
```

**`scripts/verify_desktop_ui.mjs` 当前处于重写状态，其结果不可引用。** 该脚本原先模拟的是旧外壳的 IPC（`window.__TAURI_INTERNALS__`、`plugin:http|fetch`、`check_desktop_update` 等命令），并断言旧文案「下载更新」「稍后」「正在下载 50%」与写死的 `v0.3.0`；当前客户端调用的是 `window.mygo` 的 `shell.CheckUpdate`/`shell.DownloadUpdate`/`shell.ApplyUpdate`/`shell.Version`，文案与版本号也已不同。脚本与实际调用面是两套接口，直接运行必然全红，**它的通过与否不能说明桌面更新界面是否可用**。重写后需要 mock `window.mygo`、断言当前文案与版本，并实际执行过才可引用。上面表格中 2026-10-22 的「桌面更新界面」一行记录的是那次执行的结果，早于当前实现，不能作为现状。

0.3.0 的审核队列、对比和候选检查需要服务端 API 与 worker 同步更新。仅更新 EXE 不会更新云端服务。普通成员仍只提交版本绑定意见，管理员独立裁决；原生更新和公开 Release 验收不在上述浏览器结果之内。

# 历史：知识库适配验收 — 2026-10-01

对照仓库为 `https://github.com/Yuuu0109/nonoka-sub-x`，分支 `main`，提交 `af32b0145f70a98199b246704076c384bfda95ee`；其中 `third_party/finesub/VERSION` 为 0.5.1，原生存储 schema 为 5。本次修改在隔离副本与独立 SQLite 数据库中验证，不写真实知识数据，不迁移业务数据库，不执行推送、部署或 Docker 重建。

## 追加：保留旧用途的兼容保存

本轮实现已有版本的定点兼容保存，贯通修订、预检、确认、裁决与导出。当前严格格式的新文件不会因包内自带基线而获例外；旧章节、行数、顺序、标记及匹配身份保持，含误听提示的旧行暂不允许修改。旧式档案身份行即使能通过当前语法，也走保留路径，避免整篇保存隐式重写旧类型。

| 本轮验证 | 结果 | 范围 |
| --- | --- | --- |
| 后端完整回归 | 75 项通过、无跳过 | 原有 56 项及新增 19 项；原文/简介/值修订、旧式但可读正文、身份及误听限制、新增严格、导出基线追踪、不信任上传基线、重复导入、CAS |
| 前端检查及生产构建 | 通过 | Vue/TypeScript 与 Vite runner 构建；兼容修订说明、发布提示和兼容包下载标签 |
| 三个真实样例的原生副本 | 通过 | 旧节点 ID/类型、原章节、归属边、匹配项及不相关载荷保持；prompt 含修订值；核验声明保持 6→6、10→10、7→7；共享政策保持 |
| 原生证据与并发 | 通过 | 旧证据仍绑定旧哈希，未改值的证据保持；改值获得新哈希并重新进入当前核验；原生用户编辑正常追加来源证据；过期编辑与其他当前修改一起原子回滚 |
| 专用工具 CLI | 通过 | 默认 dry-run 不改变复制数据库；未打补丁引擎拒绝且未写入；副本应用成功；陈旧基线拒绝；未收录的其他 rendered 修改保留并阻止应用 |
| 隔离浏览器 | 通过 | 三份真实旧基线；422 后刷新草稿恢复、409 保护与人工合并、旧正文 ZIP 更新、三份独立裁决、worker 发布和兼容包下载；390px 移动端提示/导航/主播页，无 JS 错误或水平溢出 |
| 引擎补丁 | 副本验证，未安装 | 两个目标文件；固定提交上 `git apply --check` 通过；用户 nonoka 安装和真实知识库不改，原生默认文本编辑器和收割路径保持原行为 |

上表的当前完整回归取代此前 56 项作为本轮最终代码验证范围。运行时设置仍由用户原业务库保存，本轮没有切换配置或重启原服务；选择 `finesub-0.5.1` 后，已有旧版本可走兼容路径。后端需原服务下次正常重启加载修改。

原生应用的独立边界、备份及运行方法见 [COMPATIBILITY.md](scripts/COMPATIBILITY.md)。兼容导出只声明正文基线及目标哈希，不凭 Markdown 重建旧类型和历史；须用支持受限补丁的专用导入器更新已有 schema 5 知识库。混合包其他新格式文件不由专用工具应用，默认原生编辑器未因此变成兼容编辑器。

本机本轮证据：`C:\Users\YU\Documents\Codex\2026-10-01\task\legacy-compat-20261001\final-full-tests.xml`、`ui-result.json`、`compat-review-desktop.png`、`compat-release-desktop.png`、`compat-release-mobile.png`，以及项目 `test-results/native-preserved.json`。原生写入和证据追加只在复制数据库发生；真实库和三份原正文保持。

以下是同日此前的当前文本适配与暂存候选诊断记录。

| 验证项 | 结果 | 精确范围 |
| --- | --- | --- |
| 后端完整测试 | 56 项通过、无跳过 | 旧工作流、当前语法/章节、索引类型保留、style 无匹配索引、逐条错误及原文保留、配置切换重新校验、CAS 冲突、原生名称身份、真实投影往返、原生验证器 |
| Vue / TypeScript 与生产构建 | 通过 | `vue-tsc --noEmit`、Vite `build --configLoader runner`；runner 避免本机默认配置加载器扫描受限父目录 |
| 隔离浏览器流程 | 通过 | Edge headless、全新浏览器上下文、1440px 桌面；访问口令、管理员登录、选择当前配置、ZIP 上传/worker 预检/确认、style 筛选、修订、刷新恢复草稿、409 保留草稿、旧基线提示与人工确认、三分类裁决、发布和下载 |
| 当前原生写入与重读 | 通过 | 浏览器下载包由指定版本的原生编辑器写入临时 schema 5 存储；3 主体、14 正文节点、5 索引项；核对节点载荷/别名/误听/索引、渲染幂等、style 不匹配名称；部分拒绝回归明确失败 |
| 移动端页面与导航 | 通过 | 390×844px；等待侧栏过渡完成后检查发布页/主播页截图，导航切换正常、无水平溢出、无 JavaScript 页面错误。主播阅读正文忽略 HTML 格式提示，原文与导出仍保留 |
| 真实知识投影往返 | 通过（保留配置） | 只读 `../knowledge/rendered`，46 common + 1 streamer；保持正文，按索引保留类型。没有声称这些旧正文全部符合当前严格格式 |
| 业务数据和运行服务 | 未改动 | 未连接业务数据库执行测试；未重启原运行服务。Python 修改需原服务下次正常重启后加载 |

当前严格规则的只读审计发现 3 份真实正文与当前原生保存规则不兼容；这不表示正文事实错误或原生读取失败。本次保留全部原文，不自动迁移或丢弃：

- `../knowledge/rendered/common/MyGO!!!!!.md`：“3rd 专辑《致並跡》（联网搜索 2026-08-19）”第 27–31 行及“乐队编制”第 35 行是散文行，该 common 默认章节规则只接受四列术语。
- `../knowledge/rendered/common/小日向美香1st写真集「目光」.md`：“拍摄幕后（#19/#20 补充）”第 27–36 行是散文行，而对应规则只接受四列术语。
- `../knowledge/rendered/streamer/小日向美香.md`：旧章节“说话风格”“喜好 / 特点”“重要经历”不在当前 streamer 预设；“人际关系”第 109–125 行不是四列术语。

严格配置下错误归属到对应预检条目并带章节/行号，错误条目的正文可预览、原上传包可下载，同批有效条目可单独确认。默认旧保留配置没有被自动切换。当前 FineSub 已删除旧 translation 读取器，旧翻译内容可归档，但不能报告为当前原生读取通过。

## 三个历史正文的读写语义复核

2026-10-01 追加复核仍对照上述固定提交，原生操作全部在真实数据库的临时副本上执行。三份文件均与当前 `KnowledgeRepo.entry_text` 的输出完全一致，原生解析和 `entry_injection_text` 提示词读取通过。但 `edit_subject` 默认 `on_invalid="reject"` 会拒绝原文重新保存，甚至只修改简介也会拒绝。严格预检对这三个条目的章节和行体限制与原生默认写入规则一致，本次没有发现这三个报错来自审核校验器额外收紧；准确结论是“可读，但当前原生默认保存拒绝”，不能据此声称原事实无效。

原生 `harvest_rendered_edits` 对未修改的投影不做写入；临时副本中仅改简介后，它采用 `on_invalid="stage"`，分别把 6、10、77 行暂存到“待归类”。这会改变节点类型、章节政策和后续处理行为，不能称为只改排版。直接把最终暂存正文写回旧节点还会遇到旧 `fact` 类型的章节约束；临时副本使用原生“先暂存、再补原章节标记”的两步编辑才能完成。真实库未执行这两步。

候选均保留原章节来源标记与全部原正文；原生提示词仍包含所有原正文（比较时只忽略列表符号规范化），主体索引、别名及正文匹配项一致，三份条目的现有节点可见性均为 `local`。但是提示词章节结构和以下功能发生变化：

| 条目 | 原有待核验声明数 → 候选数 | 全部暂存后的修复候选数 | 原生章节映射后的修复候选数 |
| --- | --- | --- | --- |
| MyGO!!!!! | 6 → 0 | 8 | 8 |
| 小日向美香1st写真集「目光」 | 10 → 0 | 11 | 11 |
| 小日向美香 | 7 → 16 | 70 | 35 |

“待核验声明”是原生 `unverified_claims` 的实际枚举结果，本次没有执行联网核验或共享。两个 common 条目原有 16 条可外部核验声明被暂存后不再进入该枚举，章节自动共享政策也从 `inherit` 变为 `local`。主播条目整篇重新保存使 9 条旧 `fact` 改为当前 `note`，生日、家庭成员、开场白失误统计等原文因此新增到后续外部核验范围；当前可见性仍为本地。临时副本中的原章节标记也是暂存节点，所以修复候选数量包含这些标记，不等于问题正文行数。

原生 `phase_b.SECTION_MAP` 明确支持“说话风格”“喜好 / 特点”归入“特点”；按该映射试验后，主播的暂存修复候选从 70 降为 35，但上述核验范围变化仍存在，并且映射后的“特点”采用 `inherit` 自动共享政策。“重要经历”的原生映射仍是“待归类”，散文式人际关系也没有无需新增术语字段的可靠自动转换。两个 common 条目的散文没有证据充分的四列术语映射；没有编造原文、中文名或别名，也没有把它们全部改塞“档案”。

上述批量暂存候选已弃用，仅作诊断对照。随后按用户要求实现保留用途的定点兼容，结果见本文件追加验收；真实 `knowledge.sqlite`、三份 rendered 正文及 manifest 均未写入。不再要求用户接受上述暂存导致的政策变化。本次没有执行整库 Phase B 迁移、直接 SQL 修改、推送、部署或服务重建。

复核依据：固定提交中的 `node/repo.py`（读取）、`node/edit.py`（默认拒绝与投影收割暂存）、`node/presets/common.toml` 和 `streamer.toml`（章节政策）、`node/phase_b.py`（明确章节映射）、`verify.py`（仅当前 `note` / `term` 参与核验）。本机证据位于 `C:\Users\YU\Documents\Codex\2026-10-01\task\format-fix-20261001\diagnosis.json`、`semantic-impact.json` 及 `candidate-native-mapping`；这些是诊断副本和候选，不是已应用数据。这里只追加验证说明，既有 56 项测试和浏览器验收仍是上表的执行记录，没有因文档修改重复运行完整测试。

原生检查的复现入口：

```powershell
$env:FINESUB_REFERENCE_SRC='路径\nonoka-sub-x\third_party\finesub\src'
$env:REVIEW_KNOWLEDGE_TEST_ROOT='路径\knowledge'
cd backend
..\.venv\Scripts\python.exe -m pytest tests -q -p no:cacheprovider
cd ..
.\.venv\Scripts\python.exe scripts/verify_finesub.py --finesub-src $env:FINESUB_REFERENCE_SRC --archive 路径\知识库包.zip --profile finesub-0.5.1
```

本次未重新验证 PostgreSQL、Docker、公网、分享签名、完整翻译任务和既有用户浏览器会话。以下为首版历史结果，不能作为当前版本或本次执行结果引用。

# 首版历史验收记录

日期：2026-09-17。版本：0.1.0。

## 已验证

| 验证项 | 结果 | 范围 |
| --- | --- | --- |
| Vue / TypeScript 检查及生产构建 | 通过 | Vue 3、Vite 7；产物位于 frontend/dist |
| SQLite 集成测试 | 23 项通过 | 权限、CSRF、记录与裁决分离、版本失效、重复导入、冲突、发布、回退、删除、纠错与精选、危险 ZIP、JSONL、账号停用、任务恢复 |
| PostgreSQL 集成测试 | 同一套 23 项通过 | 独立 PostgreSQL 17 测试数据库，未使用其他项目数据库 |
| Docker 镜像构建 | 通过 | finesub-review:0.1.0 |
| Compose 本机启动 | 通过 | 独立验收项目的 API、PostgreSQL、worker 启动成功，API/数据库健康检查通过 |
| 容器内真实 HTTP 工作流 | 通过 | 真实上传、worker 预检、确认导入、成员意见、管理员裁决、worker 发布、ZIP 下载；成员裁决和下载返回 403 |
| 当前真实知识库往返 | 通过 | 普通知识正文保持一致；翻译纠错拆分后重新组装 |
| FineSub 原生读取 | 通过 | 指定本地 FineSub 源码读取导出包：46 个 common、1 个 streamer、22 条纠错、22 条精选 |
| 本机实际站点 | 通过 HTTP 验证 | http://127.0.0.1:8765；登录、知识列表、详情、设置、批次、通知、审计和前端静态资源 |
| 一致性备份 | 通过 | SQLite 在线 backup API，integrity_check 与 foreign_key_check 通过 |
| 独立恢复启动 | 通过 | 从备份恢复到新文件，启动新 API 进程，登录后读到 70 个知识项及原始上传文件 |

本机正式预览只创建了管理员并复制现有知识作为 70 个待裁决项；没有为用户内容自动提交审核意见、通过决定或发布。临时容器验收使用独立测试账号和测试词条。

## 验收中发现并修复

- HTTP 响应先于数据库事务提交导致的连续请求竞态：数据库、认证与权限依赖使用 function scope，在发送成功响应前提交事务。真实容器工作流和恢复实例均重新验证通过。
- 已发布删除提案重复出现在待发布列表：为词条保存当前已发布的修订标记，同时将删除变更纳入审核归档。
- 发布排队后正文修订或决定变化：worker 再次检查选中版本，拒绝发布已过期的内容。
- JSONL 连续重命名：维持最初词条身份；按行修改必须提供基础内容哈希。

## 尚未证明的范围

- 内置浏览器打开本机地址时返回 ERR_BLOCKED_BY_CLIENT，因此本轮没有完成浏览器点击流程、截图视觉检查和移动端视觉验收。前端构建通过不等于视觉验收通过。
- 公网域名、HTTPS 证书签发、实际目标服务器部署尚未进行。Caddy / HTTPS 配置已提供。
- FineSub 验证覆盖原生知识读取，不包含一次完整的字幕翻译任务，也不代表所有 FineSub 版本都兼容。
- PostgreSQL 已完成业务流程验证；恢复启动演练使用 SQLite 备份。生产部署应另外执行 PostgreSQL 的 pg_dump / pg_restore 恢复演练。

## 复验入口

- 后端：backend/tests/test_workflow.py
- 本机 HTTP：scripts/verify_local.py
- 原生读取：scripts/verify_finesub.py
- 容器实际工作流：scripts/verify_container.py，仅可在独立验收部署中使用
- 在线备份：scripts/backup_sqlite.py
- 备份恢复启动：scripts/verify_restore.py

运行方式、数据位置和部署配置见 README.md。
