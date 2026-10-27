# FineSub 审核桌面版

桌面端使用 MyGo 0.7.1（Go 外壳 + 系统 WebView2）承载现有 Vue 界面，连接统一审核服务器。客户端不携带 Python、数据库或知识库，不提供离线审核及同步。

## 云服务器

继续使用项目根目录的 Compose 部署 FastAPI、worker 和 PostgreSQL，并通过 HTTPS 域名访问。参见 [README](README.md#postgresql--docker-部署)。`COOKIE_SECURE=true`，数据库无需开放公网端口。服务器域名根路径须提供 `/api/*`，首版不支持子路径部署。

桌面版通过 MyGo 的 fetch 插件发起请求：真正发出请求的是 Go 侧，它持共享 cookie jar、不跟随重定向、可读全部响应头，因此沿用服务端会话 Cookie、管理员权限和 CSRF 校验。无需添加跨域白名单，也无需将 Cookie 改成 `SameSite=None`。Cookie 由 Go 侧管理，不通过前端读取；不要复制或发布应用缓存目录。客户端只能请求 HTTPS API（本机调试允许 HTTP 回环地址），禁止跟随 API 重定向。界面由 Go 侧注册的 `mygo://` handler 提供，这是附加 CSP 的唯一办法——MyGo 自身没有 CSP 配置项。

## 使用

下载发布页的免安装程序 `FineSub Review.exe`，双击直接运行，不需要安装 FineSub。本说明针对免安装 EXE；`mygo build` 同时产出 NSIS 安装器，暂不随 Release 发布。需要系统已具备 WebView2 Runtime；本机已有。设置与会话仍保存在系统应用数据目录，免安装不代表所有数据都随 EXE 移动。

1. 启动客户端，自动连接默认服务器 `https://49.234.185.63`，无需填写地址。
2. 输入原有访问口令。管理员继续通过现有管理员入口登录。
3. 导入、裁决、发布仍由云端执行。下载会打开系统保存对话框。
4. 顶部“更换服务器”返回连接页；切换前保存编辑内容。自定义服务器地址会保存在本机，下次启动自动重连。连接失败时可修改地址重试。

服务器地址不应包含 `/api`、用户名、密码、查询参数或片段。本地调试可以填写 `http://127.0.0.1:8765`；先启动本机后端与 worker。未退出的有效会话可能在重启后恢复，公共电脑使用完毕请点击“退出知识库”。网页与桌面端的会话相互独立。

## Windows 开发与打包

本机轻量方案：使用仓库现有 GitHub Actions 的 **Windows desktop build**。Go 工具链、mygo CLI 和编译缓存由 Windows 云端 runner 提供，本机只需下载最终免安装 EXE；修改代码时可以仅运行 Node/Go 检查。构建与公开发布分开，Actions 不会自动创建 Release。

0.2.2 起使用美香主题图标。图标源为 `frontend/src/assets/icons/mika-source.png`，由 MyGo 从 PNG 生成 16/24/32/48/64/128/256 各尺寸并写入 EXE，不需要额外命令；`icon.ico` 仅留存，MyGo 构建不读取它，`source.svg` 为旧图标备份。

需要 Node.js 22.12+、pnpm 11 和 Go 1.27.3（`desktop/go.mod` 声明；`GOTOOLCHAIN=auto` 时自动获取）。**不再需要 Rust、MSVC 和 Visual Studio C++ Build Tools**。签名发布还需要 mygo CLI 0.7.1，用户侧仍需 WebView2 Runtime。

在仓库根目录运行：

```powershell
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend test:desktop
pnpm --dir frontend build
node scripts/embed_frontend.mjs           # 把 frontend/dist 拷进 desktop/ui/，供 go:embed
cd desktop
go test ./...
go build -trimpath -o finesub-review-desktop.exe .   # 未签名，应用内更新不可用
go install github.com/egoist/mygo/cmd/mygo@v0.7.1
mygo build                                # 签名发布版，需 MYGO_UPDATER_PRIVATE_KEY
cd ..
node scripts/verify_desktop.mjs
node scripts/package_desktop.mjs
```

`embed_frontend.mjs` 必须先跑：外壳把页面嵌进可执行文件，跳过这一步的产物一旦离开仓库就报「找不到前端」。它会先清空 `desktop/ui/`（保留 `.gitkeep`）再拷贝，避免 Vite 的旧哈希资源残留被一起嵌入。

产物在 `desktop/build/windows-amd64/`：`FineSub Review.exe`、`finesub-review-setup-<版本>-amd64.exe`、`finesub-review-<版本>-windows-amd64.tar.gz`（更新器安装时下载的对象）、`update-windows-amd64.json`（含 Ed25519 签名与发布说明）和 `install.ps1`；`desktop/build/SHA256SUMS` 记录校验和。`scripts/package_desktop.mjs` 把它们复制到 `output/desktop-<版本>/`，并先校验 `desktop/mygo.json` 与 `frontend/package.json` 版本一致、`CHANGELOG.md` 存在该版本小节，否则直接失败。Node 与 Go 仅用于开发和构建，最终用户无需安装；更新签名只校验下载的归档，没有 Windows Authenticode 证书签名，两者用途不同。

仓库提供 `.github/workflows/desktop.yml`，推送相关分支或手动触发时在 Windows 上执行前端测试与构建、`embed_frontend.mjs`、`go test -v ./...`、`go build`；非 PR 时再用 `MYGO_UPDATER_PRIVATE_KEY` 执行 `mygo build`、`scripts/verify_desktop.mjs` 和 `scripts/package_desktop.mjs`，结果保存为 Actions artifact，不自动发布 Release。PR 只跑到 `go build`，不读取签名密钥。`verify_desktop.mjs` 依次校验清单版本与归档地址、归档实际大小、清单签名的 Ed25519 验签（并确认改一字节的副本会被拒）、EXE 的 PE32+ / Windows x64 头、EXE 内是否含嵌入的前端，以及 `SHA256SUMS` 的每一项。

内嵌前端用的是 `assets/` 和 `index-` 两个串，**不能用 `FineSub`**：那个词来自 `desktop/main.go` 的 `const appName` 和窗口标题，没有嵌入任何页面时也照样存在，拿它做判别等于一条永远绿的检查。`assets/` 是 Vite 的输出目录名，`index-` 是 Vite 内容哈希入口名（如 `assets/index-B8OPzfxW.js`），两者都只在真正嵌入前端时出现。改动检查串以后要做反向验证：让它去查一个不存在的串，确认脚本确实变红。

## GitHub Releases 更新

更新源由 `desktop/mygo.json` 的 `updates` 决定：仓库 `Yuuu0109/finesub-review-desktop`、`tagPrefix` 为 `desktop-v`。客户端经 GitHub API 找到**最新一个前缀为 `desktop-v` 的非预发布 Release**，再读该标签下的 `update-windows-amd64.json`，清单指向同标签下的 tar.gz。启动时后台检查一次，也可点击“检查更新”，无需先连接审核服务器。相同或更低版本不会提示升级。用 `go build` 直接编出的开发构建没有写入更新地址和公钥，界面会说明应用内更新只在发布版可用，而不是谎称检查过了。

**检查更新失败时的实际表现**：错误显示在更新弹窗里（`role="alert"` 的红色错误行），不静默；只有真正收到「无新版」的响应才会显示“已是最新版本”，失败时不会。弹窗内还有“重新检查”按钮，缺省无自动重试。错误文本已由 Go 侧本地化并按成因分流：**「更新源暂时拒绝访问」（GitHub 每小时 60 次限额，建议约一小时后重试）**、**「更新源还没有发布新版本」**、**「无法连接更新源」（含代理/企业网络无法访问 `api.github.com` 的情形）**，三者给出的下一步动作不同。无法归类的错误显示为「检查更新失败」并保留 Go 侧原文，便于排查。安装阶段的失败另有措辞，并明确「未安装任何内容，当前版本未改变」。检查成功但清单结构不完整（缺 `version`/`url`/`signature`）同样报错，不当作无更新。

**已知风险：错误分流依赖 MyGo 的错误文本。** MyGo 0.7.1 的 `ErrNotPublished` 位于 internal 包，应用侧无法用 `errors.Is` 判断，因此按错误文本匹配。MyGo 升级若改动措辞，分流可能失效——届时应表现为「检查更新失败」并附原文，而不是误报成其他原因。

**已知限制：依赖 GitHub 与它的速率限制。** 未认证的 GitHub API 每小时 60 次（按 IP 计），每次检查消耗 1 次 API 请求（列 Release）加 1 次 `github.com` 的清单下载。为降低撞限概率，**启动时的自动检查会复用最近 24 小时内的结果**（存于用户数据目录的 `update-check.json`）；用户点“重新检查”或“下载并安装”时一律实时请求，且安装前会再核对一次来源，因此缓存的旧结论不会导致装错版本。`api.github.com` 在部分网络不可达，表现为「无法连接更新源」。当前**没有配置备用更新源**：`mygo` 支持把 `updates.url` 指向自己托管的固定清单地址（不改代码，只改 `mygo.json` 后重新构建），但那需要自行托管清单与归档，本项目尚未采用。

0.4.0 起的更新步骤：

1. 点击“下载并安装”，过程显示进度。`mygo` 的 Install 一步完成下载、Ed25519 验签和就地替换；Windows 上不延迟到退出，所以**关闭窗口不会撤销安装**，下次启动就是新版本。文案因此写“下载并安装”，不给一个取消后什么都不会取消的按钮。
2. 签名、大小或归档格式不符时 Install 直接报错，已安装文件保持不动；错误必须显示给用户，不能当作更新成功。
3. 安装成功后界面提示“更新已通过签名校验并安装”，点“立即重启”由 `mygo.App.Relaunch` 从原路径启动新版。重启前检查审核操作是否完成并保存草稿，未通过则不允许重启。
4. 安装目录需可写（`mygo` 会先在该目录试建临时项，不可写即视为更新不可用）。Windows 上可能留下 `.old-<pid>` 残留文件，属已知行为，可手动删除。
5. 应用内不提供“另存新版”：`mygo` 从自己的暂存目录直接换入，没有可另存的归档。需要保留旧版请到 Release 页手动下载。

客户端只认编入自身的 Ed25519 公钥：签名不符、公钥不符的更新一律拒绝，因此不要重新生成密钥对。

仓库及 Release 公开，客户端没有 GitHub 令牌。更新仅下载新版桌面程序，不迁移云端数据库，云服务器无需增加更新接口。

0.3.0 增加的 FineSub 0.6.0 / schema 6 知识规则需要配套更新审核 API 和 worker，再由管理员在知识库设置中选择。EXE 更新不会自动改变该设置或迁移 FineSub 知识库。旧节点回写使用独立兼容工具和对应版本引擎补丁，见 [兼容操作说明](scripts/COMPATIBILITY.md)。

后续发布步骤：

1. 同步提升三处版本号：`desktop/mygo.json` 的 `version`、`desktop/main.go` 的 `version` 常量、`frontend/package.json` 的版本，并在 `CHANGELOG.md` 写一个与版本号一致的二级标题小节（`mygo build` 的发布说明和 `package_desktop.mjs` 的校验都读它，缺了会失败；`package_desktop.mjs` 还会校验前两处版本一致）。
2. 推送到 `feat/desktop`，等待 Windows desktop build 成功，下载 `finesub-review-windows` artifact。交付目录 `output/desktop-<版本>/` 含：`FineSub Review.exe`、`finesub-review-setup-<版本>-amd64.exe`、`finesub-review-<版本>-windows-amd64.tar.gz`、`update-windows-amd64.json`、`install.ps1`、`SHA256SUMS`。
3. 在 GitHub 创建 **`desktop-v<版本>`** 标签的 Release，目标使用该次成功构建的提交，上传上述全部文件。确认附件齐全后发布为正式最新版（非预发布）——`mygo` 只解析非 draft、非 prerelease 的 `desktop-v*` Release，标签前缀写错客户端就找不到清单。不要覆盖已发布版本的文件。
4. 确认该标签下的 `update-windows-amd64.json` 与 tar.gz 可匿名下载，并在上一版客户端执行检查、下载、验签与重启验收。审核队列和发布候选 API 需配套更新服务端 API 与 worker；仅更新 EXE 不会部署服务端。

也可用 GitHub CLI 发布，以下路径和版本需替换为本次实际值：

```powershell
gh release create desktop-v0.4.0 --target 构建提交SHA --draft --title "FineSub Review 0.4.0 免安装版" --notes-file 发布说明文件
gh release upload desktop-v0.4.0 output/desktop-0.4.0/*
gh release edit desktop-v0.4.0 --draft=false --latest
```

签名私钥是 mygo 生成的 `mygo-update.key`（Ed25519），本机放在 `desktop/mygo-update.key`（已在 `.gitignore`），另存于 GitHub Actions 的 `MYGO_UPDATER_PRIVATE_KEY` Secret——值是私钥本身，不是路径。公钥写在 `desktop/mygo.json` 的 `updates.publicKey` 并编入客户端。必须安全备份，不能提交、随包分发或重新生成替换，否则已安装客户端无法信任后续更新。也不要写进可提交的 `.env`。

## 验收

0.4.0 已提交（`4ea7b16` 保存完整源码基线，`6286334` 清理过时的桌面端说明），未发布 Release。本机用 Go 1.27.3 与 mygo 0.7.1 跑通 `go build` 与 `mygo build`，**可产出免安装 EXE**：`desktop/build/windows-amd64/FineSub Review.exe`，12,482,560 字节（约 11.9 MiB），同目录还有安装器、tar.gz、签名清单和 `install.ps1`。真实更新链路（旧版→新版的应用内升级）仍需用两个 `desktop-v*` 版本在实机验收；浏览器侧的更新验收用模拟 IPC，不能代替原生结果。当前记录见 [VALIDATION.md](VALIDATION.md)。

2026-10-12 免安装版 0.2.1：[构建 38156586017](https://github.com/Yuuu0109/finesub-review/actions/runs/38156586017) 原生测试、构建及独立 EXE 签名全部通过。[Release](https://github.com/Yuuu0109/finesub-review/releases/tag/v0.2.1) 已发布，`-portable.exe` 为 18,842,112 字节，SHA-256 为 `23a61b79216836ec9edf18b03dac210c7469dcb92b434d4862325a17b37f16d1`。本机直接启动交付 EXE，真实 IPC 返回 0.2.1；签名验证、篡改拒绝、匿名下载一致性检查通过。更新下载、错误重试与保存提示通过模拟 IPC 验证，原生测试确认不覆盖已有文件。本机原生 GitHub 检查遇到连接失败（公开清单与 EXE 手动下载正常）；尚未完成真实跨版本下载验收。

- 网页构建与原有登录流程正常。
- 桌面连接失败可重试，拒绝公网 HTTP 与非审核服务器。
- 访问口令、管理员登录/退出、会话过期与重新进入正常。
- 上传多文件/ZIP、预检、后台任务、审核与发布正常。
- 原始上传文件、知识库 ZIP、审核归档可保存；取消保存不报错。
- 重启客户端后会话行为符合服务端有效期，切换服务器不混用页面数据。
- 无开发环境的 Windows 机器完成免安装启动、可写目录就地更新、草稿恢复；安装目录不可写时明确提示更新不可用。

前端测试和构建成功不等于原生安装包或云服务器验收成功。

2026-10-10 本地验证：前端类型检查/生产构建通过；桌面地址、API 路径、文件名与传输适配测试 4 项通过；后端访问权限和网页资源测试 10 项通过；Edge 无头浏览器完成连接、拒绝公网 HTTP、登录页、更换服务器、地址记忆和网页回退检查（模拟 IPC/API）。原生构建实际尝试因缺少 Cargo 失败，环境检查也缺少 MSVC；尚未生成安装包或完成真实云服务验收。

2026-10-11 0.2.0 验证：GitHub Actions [38125821222](https://github.com/Yuuu0109/finesub-review/actions/runs/38125821222) 原生测试、Windows 构建和签名成功，构建提交为 `5fa1a7091da3c26e65da55e14f3f7f3f031e5ec9`。[正式 Release](https://github.com/Yuuu0109/finesub-review/releases/tag/v0.2.0) 已发布。安装包 4,288,892 字节；SHA-256 为 `070a2773706d0465ad4b6d5c162a6dc219470194c4444668851910621a056379`。

本机验证了同次构建的原生程序启动、真实 Tauri 版本 IPC、访问公开 GitHub 更新源并显示“已是最新版本”。安装包签名和 SHA-256 校验通过，修改一个字节后签名验证失败；匿名下载的清单与安装包和本机产物一致。前端测试 4 项、后端回归 10 项通过，更新提示、确认下载、进度、错误重试及后台检查中打开窗口通过浏览器模拟 IPC 验证。尚未执行真实跨版本覆盖安装、卸载或云端业务验收。

实现依据：[MyGo 0.7.1](https://github.com/egoist/mygo)（`mygo build`、updater、fetch 插件与 `mygo://` 自定义协议）、[Ed25519](https://pkg.go.dev/crypto/ed25519)。
