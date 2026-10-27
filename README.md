# FineSub 知识库审核站

独立部署的协作审核网站。管理员导入知识库，普通成员浏览并提交审核记录；正文修订、通过、驳回和发布均由管理员决定。意见数量和赞同比例不会自动改变裁决状态。

另提供连接云端审核服务的 Tauri 2 Windows 桌面客户端，复用本项目界面和 API。配置服务器、开发、打包与验收方法见 [桌面版说明](DESKTOP.md)。

## 已实现

- 两种角色：管理员、普通成员。成员输入访问口令进入；进入后可选择管理员账号登录，获得管理权限。支持会话、账号停用、密码修改、服务端权限和 CSRF 校验。
- ZIP、Markdown、目录、多文件、原生知识/翻译纠错 JSONL 的后台预检导入。重复内容跳过，冲突阻止覆盖，原始上传文件和来源保留。
- 主播档案、通用知识、翻译纠错记录及精选清单。全文/别名搜索、筛选、分页、任务认领与管理员分配。
- Markdown 阅读、原文、版本差异、历史修订。成员意见、引用、建议文本、证据、回复、@账号通知以及意见处理结果。
- 管理员按内容版本独立裁决。修订生成新版本，旧意见和旧决定保留为历史。
- 完整知识库发布、审核归档、回退、持久化后台任务、失败重试、审计记录。
- 本地 SQLite、部署 PostgreSQL、Docker 镜像及可选 Caddy HTTPS 配置。

首版采用单个团队知识库；登录成员可查看该知识库全部内容。多知识库隔离、Git 自动同步、AI 建议、邮件/第三方通知及音视频播放未启用。

## 本机使用

环境：Python 3.12、Node.js 22.12+ 或 24+、pnpm 11。依赖已固定在 `backend/requirements.lock` 与 `frontend/pnpm-lock.yaml`。

```powershell
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r backend/requirements.lock
cd frontend
pnpm install --frozen-lockfile
pnpm build
cd ..\backend
..\.venv\Scripts\python.exe -m app.cli init-admin
..\.venv\Scripts\python.exe -m app.cli set-passphrase
cd ..
.\.venv\Scripts\python.exe run.py
```

打开 `http://127.0.0.1:8765`。首次初始化以隐藏输入方式设置管理员密码，没有固定默认密码。也可执行 `init-admin --generate`，随机账号凭据仅写入本项目的 `data/initial-account.txt`。

首页只需输入访问口令。可在 `backend` 执行 `..\.venv\Scripts\python.exe -m app.cli set-passphrase --generate`，随机口令写入 `data/access-passphrase.txt`；数据库只保存口令哈希。手动设置口令至少 10 个字符。访问状态保留 30 天，管理员登录保留 12 小时；管理员退出后恢复原普通成员身份，普通成员点击“退出知识库”后需重新输入口令。

每个浏览器获得独立成员身份，可点击左下角头像修改审核署名。昵称仅用于署名，不验证真实身份。清除 Cookie、退出知识库、访问过期或更换口令后再次进入会建立新身份，旧审核记录继续保留。普通成员无需创建账号；原有账号和审核记录保留，但账号登录入口仅接受管理员账号。

更换口令使用同一 `set-passphrase` 命令，会使全部访问会话失效（包括管理员的访问状态），所有人需重新输入口令。手动更换不会更新之前生成的口令文本文件，应以新设置的口令为准。不要将访问口令文件和管理员凭据放入前端或提交至版本库。

`run.py` 同时运行 API 和独立 worker，按 Ctrl+C 结束两者。修改 Python 源码后需要重启；修改前端后重新 `pnpm build` 并刷新网页。

若启动时报 `[WinError 10013] 以一种访问权限不允许的方式做了一个访问套接字的尝试`，说明 8765 落在 Windows 的动态端口保留区间内（Docker、WSL、Hyper-V 会预留成片端口）。用 `netsh interface ipv4 show excludedportrange protocol=tcp` 可以看到当前保留段；换一个段外端口即可，例如 `run.py --port 9100`。把该区间释放出来（需要管理员权限重启 winnat）也能恢复 8765。

前端依赖使用 hoisted 布局（见 `frontend/pnpm-workspace.yaml`）：在未启用 Windows 开发者模式的机器上，pnpm 默认的 isolated 布局会因为无法创建符号链接而安装失败。

前后端开发分离时，后端仍使用 8765；在 `frontend` 执行 `pnpm dev` 打开 5173。Vite 会将 `/api` 代理到后端。

本次交付的本机管理员凭据位于 `data/initial-account.txt`。现有 `../knowledge` 以只读方式复制入网站，70 个审核项均为待裁决，没有自动通过或发布。

## 日常流程

1. 管理员向成员提供访问口令。成员输入口令进入，点击头像设置审核署名；管理员进入后点击右上角“管理员登录”，使用原管理员账号登录。
2. 在“导入批次”上传 ZIP 或多个文件。ZIP 可以包含顶级目录；分类以 `common/`、`streamer/`、`style/`、`translation/` 识别。单文件上传时选择分类。
3. 查看新增、修改、重复、错误、重命名、删除结果，检查差异并确认导入。
4. 成员进入词条，提交“建议通过 / 建议修改 / 建议驳回 / 证据不足”记录。记录只是意见，不改变管理员决定。
5. 管理员可以回复、标记意见处理结果，并在“修订”中生成新的候选版本。
6. 管理员填写理由并作出最终决定，不需要等待成员投票或满足审核人数门槛。
7. 在“发布版本”选择已通过的最新版本，生成完整知识库包。历史正式知识会保留。

管理员标记“采纳意见”只更新处理记录，不自动替换正文。普通成员没有导入、正文修订、裁决、导出或账号管理权限。各 API 均检查权限，隐藏按钮不是唯一防线。

## 格式与兼容

- 默认 `legacy-preserve` 保留现有正文和分类，包括旧知识库的“企划”等类型。
- `finesub-0.5.1` 对照 `Yuuu0109/nonoka-sub-x` 的 `main` 提交 `af32b0145f70a98199b246704076c384bfda95ee`（FineSub 0.5.1、schema 5），需在“知识库设置”中主动选择。新建内容保持当前严格规则，支持 `[字段] 正文`、四列术语、空占位及 HTML 格式提示。已有旧条目可以依据审核库中的已存版本定点兼容保存；不把旧节点全部转换成新 `note` 或移入“待归类”。这不改变未打补丁的原生默认编辑器。
- 当前原生知识源为 `knowledge.sqlite`；本站审核和导出 Markdown 投影。同一上传根同时有 `rendered/` 与历史 `common/streamer/style/` 时，优先当前投影并提示跳过的档案。仅有 SQLite 而无关联投影时拒绝用历史文件代替。不会读取或修改上传数据库，也不导出原生节点 ID、历史、分享 bundle 或签名。
- `finesub-0.6.0` 对照 `Yuuu0109/nonoka-sub-x@eb642f25e7bc83cb18c4e7a4e0289d410c643fbc`（FineSub 0.6.0、schema 6）。common 普通章节接受四列术语与事实短句，支持“重分类理由”元数据，人工版本注释只作为历史显示。规则和原生回写已用隔离 schema 6 副本验证；源库没有迁移或修改知识数据。
- 当前格式发布包附 `finesub-review-profile.json`，注明规则、目标 schema 和引擎来源。验证/兼容导入会拒绝版本错配；已有 schema 5 包继续支持。切换设置不会自动升级原生数据库或重写正文。
- `style/名称.md` 保留完整风格正文，按原生规则校验约定和正反例；style 不参加 common/streamer 名称匹配，不生成 `style/index.md`。
- 可切换 `finesub-code-v17`，按核对过的本地 FineSub 校验代码限制 common 类型为“游戏、梗、事件、人物”。这不是对任意 FineSub 版本的兼容保证。
- `common/index.md` 和 `streamer/index.md` 作为上传原件保存，不作为可编辑知识正文；导出时从获准正文重建。common 索引中的原有类型会随修订保留，支持原生索引的本名和别名；没有明确类型的正文不会被自动改成其他类型。
- `translation/common-mistake.md` 拆成 `Mxxxx` 记录及独立精选清单；导出时组装。精选最多 30 条，必须引用已经纳入本次发布的编号。
- `translation/good-example.md` 按 `Gxxxx` 记录拆分及组装。
- 上述 translation 归档行为适用于旧兼容配置；FineSub 0.5.1 / 0.6.0 已移除这些读取器，严格配置会逐条提示不支持，不自动迁移为 style。
- JSONL 支持 `create_entry / append_lines / replace_section / edit_lines / rename_entry / delete_entry`，也支持 `add_mistake / set_featured / add_example` 及相应 XML 包裹块。
- `edit_lines` 需要 `base_sha256` 对应基础正文，并将同一文件的行改动合并到一个提案；缺少快照依据时拒绝应用。新建词条需要说明未并入现有词条的 `reason`。
- JSONL 内相关操作按变更组处理，首次发布必须一起通过与发布。翻译提案新增编号在预检时从现有/保留编号之后分配；若并行批次冲突，确认导入会拒绝覆盖，重新预检即可。
- 不接受任意脚本生成的自定义 JSON；请转换为 Markdown 或规范 JSONL。
- 导入文件必须为 UTF-8；最多 200 个上传文件，ZIP 最多 2000 个条目，单个解压文件最多 2 MB，每批原始上传/解压内容最多 30 MB。
- 当前节点正文另有 1,000,000 UTF-8 字节限制。格式错误显示在对应预检条目中，保留正文与原始上传文件；可只确认同批有效条目。确认和裁决时会重新按当前配置校验。
- 正文修订、成员意见和决定理由保存在本机 localStorage，按用户和版本隔离；桌面端还按服务器隔离，可跨刷新或程序重启恢复。旧标签页草稿会迁入持久存储。保存仍使用编辑时的版本；遇到并行修改会保留草稿，须对照当前正文并手动合并。草稿只在本机，清除应用/浏览器数据会丢失；存储失败时页面明确提示，更新重启也会被阻止。
- 知识列表支持“我未审核”，只统计本人对当前版本提交的审核记录，回复和其他成员的意见不算。分类、批次、关联组、负责人范围随审核页面保留；“提交并继续”和裁决后的继续操作使用稳定版本游标，处理后不跳过下一条。返回队列恢复原筛选，新修订仍需重新审核。
- 差异可对照上一版本或当前正式版本，切换对比不重置编辑草稿。未保存正文修订时先保存或丢弃，再作出裁决。来源、负责人和历史版本在可展开区域中，窄屏同样可以访问。
- 发布页提前列出基线变化、格式校验和关联组未通过等阻塞原因，选择关联组时自动联动；正式发布仍由后端和 worker 复核。已发布的删除项满足所在组的发布要求，不再阻塞同组后续修订。

发布结果的 `verification: adapter-validation` 代表本站适配器校验，不代表服务器上已安装 FineSub。可对导出包执行真正的原生读取检查：

```powershell
.\.venv\Scripts\python.exe scripts/verify_finesub.py --finesub-src 路径\nonoka-sub-x\third_party\finesub\src --archive 路径\知识库包.zip --profile finesub-0.6.0
```

当前配置会在临时目录创建与规则相符的 schema 5 或 6 存储，调用指定源码的原生编辑器，再核对正文节点、别名/误听、索引元数据、渲染重读幂等性及 style 不参加名称匹配。也可省略 `--profile`，由新版发布包中的版本声明选择规则；声明与引擎 schema 不匹配时拒绝。部分拒绝或暂存也视为失败；临时数据库不会写回真实知识库。旧配置使用对应原生导入器；指定版本缺少 translation 读取器时明确失败，不把归档成功报告为当前原生读取成功。此检查不执行翻译任务、Git 操作或分享操作。

### 旧知识定点兼容

在当前配置下，审核库已有的 common/streamer 旧正文（旧式档案身份字段或不符合当前行体/章节规则）可保留原文、修订简介及现有节点值。章节、行数、顺序、标记、名称、别名、读音和术语前三列保持；含“误听”提示的旧行暂不支持修订。无法可靠映射时明确拒绝并保留草稿。没有已有版本的新文件仍严格校验，上传包自己声明的基线不能授予例外。无需修改真实知识库来启用这一审核路径，也没有自动切换现有站点配置。

兼容发布包含 `finesub-review-compat.json`，保存已有版本的正文基线、目标哈希及版本。审核站内可重新预检；原生应用需使用 `scripts/apply_compat_release.py` 和对应 schema 5 / 6 的受限引擎补丁。默认 dry-run 使用临时数据库快照；`--apply` 才提交原生用户修订。schema 6 的历史人工认可记录保留，只有修改过的值成为新人工版本，其他行不会被自动认可。普通 `verify_finesub.py` 会明确拒绝把兼容包导入空库：只有 Markdown 无法恢复原节点类型、证据及匹配历史。引擎补丁已在隔离副本验证，没有安装到用户 nonoka；默认原生编辑器及投影收割路径仍按原规则运行。

支持范围、补丁目标、备份、dry-run、原子冲突回滚及运行方法见 [旧知识兼容操作说明](scripts/COMPATIBILITY.md)。原项目 Python 服务下次正常重启后加载后端修改；前端构建后刷新页面。本次不重启既有服务、迁移数据、推送或部署。

## PostgreSQL / Docker 部署

```powershell
Copy-Item .env.example .env
# 修改 .env 中的 POSTGRES_PASSWORD 为随机字母数字密码。
docker compose up -d --build
docker compose exec app python -m app.cli init-admin
docker compose exec app python -m app.cli set-passphrase
```

默认仅绑定本机 `http://127.0.0.1:8766`，数据库不暴露宿主机端口。数据库卷为 Compose 项目自己的 `postgres-data`，不连接其他项目数据库。管理员初始化命令须在终端交互设置密码。

公网 HTTPS 部署：设置 `REVIEW_DOMAIN` 为真实域名、`COOKIE_SECURE=true`，将 DNS 指向服务器，确认 80/443 可用，再执行：

```powershell
docker compose -f compose.yml -f compose.https.yml up -d --build
```

也可以使用现有反向代理连接本机 8766。HTTPS/DNS/证书签发需在实际目标服务器验证，不能以本地构建结果代替。

没有域名、使用公网 IP，且 80 已被现有 Nginx 等服务占用时，可将 `.env` 中的 `REVIEW_DOMAIN` 设置为公网 IP，并使用独立的仅 443 配置：

```bash
docker-compose -f compose.yml -f compose.ip-https.yml pull caddy
docker-compose -f compose.yml -f compose.ip-https.yml up -d caddy
docker logs --tail=100 finesub-review-caddy-1
```

此配置与 `compose.https.yml` 二选一，不要同时叠加。它保留现有 80 端口服务，仅发布 443，使用 Let’s Encrypt `shortlived` IP 证书和 TLS-ALPN-01 验证，自动续期，并强制启用安全会话 Cookie。服务器公网 TCP 443 必须可达且未被其他服务占用；此方案不适用于私有 IP。验证 `curl -fsS https://公网IP/api/health` 成功后，EXE 可填写同一 HTTPS 地址。使用 Docker Compose 插件的服务器可将 `docker-compose` 换为 `docker compose`。

IP 配置中的 `default_sni {$REVIEW_DOMAIN}` 用于不发送 SNI 的客户端，在 Docker 端口映射下选择正确的公网 IP 证书。已有部署更新此文件后，重新创建 Caddy 容器并检查接口：

```bash
docker-compose -f compose.yml -f compose.ip-https.yml up -d --no-deps --force-recreate caddy
curl -fsS https://公网IP/api/health
```

`Caddyfile.ip` 以单文件绑定挂载。使用 Git、`sed -i` 或编辑器替换文件后，容器可能仍挂载旧文件，仅执行 `caddy reload` 无法读取新内容。重新创建 Caddy 可刷新挂载，并沿用原有证书数据卷。

配置依据：[Caddy 默认 SNI](https://caddyserver.com/docs/caddyfile/options#default-sni)。

来源文档：[Vue](https://vuejs.org/guide/quick-start.html)、[FastAPI 上传](https://fastapi.tiangolo.com/tutorial/request-files/)、[SQLAlchemy](https://docs.sqlalchemy.org/en/20/orm/quickstart.html)、[Docker Compose](https://docs.docker.com/compose/)、[Caddy 反向代理](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)。

## 数据、备份与恢复

为了让首版备份保持一致，正文、原始文件和发布包都存入同一个数据库，上传限额控制数据库体积；后续大规模部署可迁移对象存储。原始本地 `knowledge` 不会被网站写入。SQLite 数据位于 `data/review.db`；PostgreSQL 数据位于独立 Docker 卷。

SQLite 使用在线备份 API，避免直接复制活跃 WAL 数据库：

```powershell
.\.venv\Scripts\python.exe scripts/backup_sqlite.py
```

备份包含账号、审核记录、原始上传和发布产物，输出完整性检查与 SHA-256。恢复前停止网站，先把现有数据库连同 WAL/SHM 文件移入新的保留目录，再把备份复制为 `data/review.db`，启动后核对账号、词条数量、意见和发布包。不要覆盖尚未备份的现有数据库。

PostgreSQL 备份（在容器内生成，避免 PowerShell 二进制重定向损坏）：

```powershell
docker compose exec db pg_dump -U review -d finesub_review -Fc -f /tmp/finesub-review-backup.dump
docker compose cp db:/tmp/finesub-review-backup.dump ./finesub-review-backup.dump
```

恢复演练应创建一个新的独立数据库，用 `pg_restore` 导入后核对，再考虑切换连接；不要直接覆盖当前库。配置和镜像版本也需与备份一并保存。数据库中包含密码哈希与会话，备份应限制访问。

后台任务持久化在数据库中。处理中断超过 10 分钟的任务会重新排队；失败任务可由管理员重试。内容确认与发布通过数据库事务避免半成品，发布期间内容或裁决变化会导致发布失败，需重新创建发布。

## 测试

```powershell
cd backend
..\.venv\Scripts\python.exe -m pytest tests -q
```

默认测试仅使用项目内 `test-results/db/test.db`，不会清空正常数据库。PostgreSQL 集成测试需指定独立测试数据库，数据库名必须以 `_test` 结尾：

```powershell
$env:TEST_DATABASE_URL='postgresql+psycopg://用户名:密码@127.0.0.1:端口/finesub_review_test'
..\.venv\Scripts\python.exe -m pytest tests -q
Remove-Item Env:TEST_DATABASE_URL
```

原生测试需显式提供对应源码和隔离知识库夹具：schema 5 使用 `FINESUB_REFERENCE_SRC`、`FINESUB_COMPAT_SRC`、`REVIEW_KNOWLEDGE_TEST_ROOT`；schema 6 使用 `FINESUB6_REFERENCE_SRC`、`FINESUB6_COMPAT_SRC`、`REVIEW_SCHEMA6_TEST_ROOT`。`REFERENCE` 为原版，`COMPAT` 为打过对应补丁的副本。完整命令见 [兼容操作说明](scripts/COMPATIBILITY.md)；未提供对应环境时测试明确跳过。

测试会清空指定的测试数据库；禁止指向其他业务数据库。若父目录存在 `knowledge`，还会只读执行真实知识库往返测试。浏览器验收应再检查管理员导入/裁决/发布、普通成员记录、移动端布局、键盘操作与 Markdown 渲染。
