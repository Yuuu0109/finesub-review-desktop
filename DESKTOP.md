# FineSub 原生桌面客户端

客户端采用 MyGO 0.7.1 的 Go 原生 UI，使用系统原生文件和消息对话框、原生 Markdown 与差异组件。桌面构建没有 Vite、HTML 页面嵌入或 WebView2；`frontend/` 仍用于配套服务的网页访问。

## 使用与数据

支持 Windows 10 / 11 x64。下载正式 Release 的 `FineSub Review.exe` 后放在可写目录双击，不需要安装开发工具、FineSub 或 Python 服务。正式发布状态见 [Releases](https://github.com/Yuuu0109/finesub-review-desktop/releases)，测试状态见 [VALIDATION.md](VALIDATION.md)。

默认审核服务器为 `https://49.234.185.63`。自定义地址必须是 HTTPS 来源地址，不能包含 `/api`、用户信息、查询参数或片段；本地开发允许 HTTP 回环地址。服务器根路径提供 `/api/*`，目前不支持子路径部署。

访问口令进入普通成员身份；管理员登录后才显示管理操作。客户端向同一审核 API 发请求，保持 Cookie、CSRF 和服务器权限校验，拒绝自动跟随 HTTP 重定向。导入、裁决、发布和后台任务在云端完成。

设置和草稿存入系统用户数据目录，草稿按服务器、用户及审核对象隔离。会话经系统凭据存储持久化，遵守服务端过期与退出规则；Windows 使用 Credential Manager。不要把凭据存储失效与“已经保存登录”混为一谈，失败应在客户端显示。

便携 EXE 不代表所有数据都保存在 EXE 旁。切换服务器保留各自的数据；退出知识库撤销当前访问身份，服务端有效期过后需要重新进入。公共电脑完成工作后应退出。

**0.4.0 的本地草稿：** 原版使用 WebView localStorage，0.5.0 不自动读取该存储。升级前先在旧版提交或另存未完成内容；服务器历史记录保持不变。不要在原生客户端读不到草稿时清理旧 WebView 数据。

## 原生构建

`desktop/mygo.json` 定义版本、标识、图标和更新源。普通开发只需 Go 1.27.3：

```powershell
cd desktop
go test ./...
go build -trimpath -ldflags "-H=windowsgui" -o "FineSub Review-dev.exe" .
& ".\FineSub Review-dev.exe" --version
& ".\FineSub Review-dev.exe"
```

无需执行 `pnpm build` 或 `scripts/embed_frontend.mjs`。程序使用 Go 原生界面，不查找 EXE 附近或当前目录的 `dist`。单独复制 EXE 到其他目录即可运行。开发构建没有 MyGO CLI 写入的更新源和公钥，不能作为应用内更新测试的旧版本。

签名发布：

```powershell
go install github.com/egoist/mygo/cmd/mygo@v0.7.1
mygo build
cd ..
node scripts/verify_desktop.mjs
node scripts/package_desktop.mjs
```

`MYGO_UPDATER_PRIVATE_KEY` 为现有 Ed25519 私钥内容，不能提交到仓库或打入程序。MyGO 生成的更新签名与 Windows Authenticode 证书签名不同。

发布整理和校验脚本需要 Node.js 22+；应用运行、开发构建和 Go 测试不需要 Node。网页服务的 Node/pnpm 依赖只用于 `frontend/`。

## 更新与发布

使用官方 `github.com/egoist/mygo/plugins/updater/native`，提供原生更新窗口、进度、发布说明、稍后提醒和跳过版本。窗口支持中文，启动检查和手动检查由插件管理。程序退出和更新重启应使用同一保存保护流程。

更新源固定为公开仓库 `Yuuu0109/finesub-review-desktop` 的 `desktop-v*` 正式 Release。保留现有标识、公钥和标签前缀，才能让已安装版本信任后续更新。新版本的清单和归档必须位于同一个标签，附件不可在发布后原地替换。

产物位于 `desktop/build/windows-amd64/`，发布整理目录为 `output/desktop-<版本>/`：

- `FineSub Review.exe`：默认提供给用户的免安装程序。
- `finesub-review-setup-<版本>-amd64.exe`：可选安装器。
- `finesub-review-<版本>-windows-amd64.tar.gz`：应用内更新归档。
- `update-windows-amd64.json`：含 Ed25519 签名的更新清单。
- `install.ps1`：MyGO 生成的安装脚本。
- `SHA256SUMS`：整理后覆盖所有分发附件的校验和。

工作流对 PR 和 `codex/native-mygo` 分支只构建开发 EXE，不读取签名私钥。主分支、`desktop-v*` 标签和手动触发可构建签名包；Actions 始终只保存 artifact，不自动创建正式 Release。

发布流程：

1. 修改 `desktop/mygo.json` 版本，并在 `CHANGELOG.md` 增加同版本小节。
2. 在 Windows 完成 Go 测试、原生构建和实机验收。
3. 使用经过验收的提交生成签名包，运行校验和整理脚本。
4. 创建对应 `desktop-v<版本>` 的草稿 Release，上传整理目录的所有附件。
5. 核对匿名下载、清单地址和签名；用前一签名版本完成实际升级、重启及身份/草稿恢复。
6. 确认后发布正式 Release。

`verify_desktop.mjs` 检查版本、精确仓库/标签地址、归档大小、签名、篡改拒绝、Windows x64 PE 头和校验和。它不检查旧网页标记，也不能替代原生窗口的操作验收。

## 配套服务

桌面更新只更新客户端。FastAPI、worker、数据库和网页界面单独部署，见 [SERVER.md](SERVER.md)。客户端和服务端需要兼容的 API；FineSub 知识格式规则和兼容补丁仍按服务端文档使用。

