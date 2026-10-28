# FineSub Review Desktop

基于 **MyGO 0.7.1 原生 UI** 的 Windows 知识库审核客户端。界面由 Go 绘制，直接连接统一审核 API；桌面运行和编译不依赖 Vue、Node.js、Python 或 WebView2。

当前开发版本为 **0.5.0**。是否可用于正式交付以 [验收记录](VALIDATION.md) 为准；本仓库的构建产物不会自动发布为正式 Release。

## 下载与使用

正式版本从 [Releases](https://github.com/Yuuu0109/finesub-review-desktop/releases) 下载 `FineSub Review.exe`，放在用户可写目录后双击运行。Windows 10 / 11 x64，原生界面使用 Direct3D 11。

1. 默认连接 `https://49.234.185.63`，也可设置自己的 HTTPS 审核服务器。
2. 输入团队访问口令；管理员可继续登录自己的管理员账号。
3. 浏览知识库、提交审核意见，或执行获授权的导入、修订、裁决和发布。
4. “检查更新”打开 MyGO 原生更新窗口。签名发布版可下载更新、验签、安装并重启；开发构建不支持应用内更新。

客户端需要联网访问审核服务。审核数据保存在服务器，本地仅保存客户端设置、草稿和受保护的会话信息，不提供离线审核同步。

**从 0.4.0 切换：** 旧 WebView 中未提交的 localStorage 草稿不自动导入原生存储；请先在旧版提交或另存。服务器上的知识库、成员和审核记录不因更换客户端而删除。更新不会部署 API、worker 或迁移服务器数据库。

详细使用、会话与更新说明见 [DESKTOP.md](DESKTOP.md)。

## 开发

只编译桌面客户端需要 Go 1.27.3：

```powershell
cd desktop
go test ./...
go build -trimpath -ldflags "-H=windowsgui" -o "FineSub Review-dev.exe" .
& ".\FineSub Review-dev.exe"
```

开发构建直接创建原生窗口，不需要先构建前端或复制 `frontend/dist`。`desktop/mygo.json` 是客户端版本和发布配置的唯一来源，`--version` 可在不打开窗口的情况下读取版本。

签名发布使用 MyGO CLI：

```powershell
go install github.com/egoist/mygo/cmd/mygo@v0.7.1
mygo build
```

私钥通过 `MYGO_UPDATER_PRIVATE_KEY` 提供。现有更新公钥必须保持；不要重新生成密钥覆盖它。发布校验和整理工具 `scripts/verify_desktop.mjs`、`scripts/package_desktop.mjs` 使用 Node.js 22+，它们不参与桌面程序运行或普通 Go 编译。

## 仓库结构

| 路径 | 职责 |
|---|---|
| `desktop/` | Go 原生界面、客户端服务、设置/草稿存储和原生更新 |
| `backend/` | 配套审核 API、worker、权限和服务器数据 |
| `frontend/` | 配套服务的网页界面，仅使用浏览器请求，不含桌面 IPC 或更新窗口 |
| `scripts/` | 发布校验、服务端维护和 FineSub 格式兼容工具 |
| `.github/workflows/desktop.yml` | Windows Go 测试、原生构建及签名产物检查 |

服务器部署、备份、知识格式和原生知识库兼容说明保存在 [SERVER.md](SERVER.md)。已有服务端功能与安全校验仍由服务端负责；桌面界面不会绕过它们。

## 验证与交付

Windows CI 对 PR 和开发分支执行 Go 测试、原生 EXE 构建及版本检查。正式分支、版本标签或手动工作流可进一步生成签名发布产物，工作流只上传 Actions artifact。

正式发布前还需要在 Windows 实机完成首次启动、中文输入、权限操作、重启后的身份/草稿恢复和跨版本更新验收。代码编译通过或签名正确不代表这些使用流程已经验收。当前证据和待测项见 [VALIDATION.md](VALIDATION.md)。

