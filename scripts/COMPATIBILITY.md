# 旧知识定点兼容操作说明

审核站的 FineSub 0.5.1 / 0.6.0 配置支持有基线的旧正文定点保存。原生知识应用是独立步骤，需要本文对应版本的受限引擎补丁和专用工具。本次仅交付工具与隔离验证，没有修改用户 nonoka 安装或真实知识数据。

## 支持版本与审核入口

对照 `Yuuu0109/nonoka-sub-x` 的两份固定源码：

| 审核配置 | 存储版本 | 参考提交 | 受限补丁 |
| --- | --- | --- | --- |
| `finesub-0.5.1` | schema 5 | `af32b0145f70a98199b246704076c384bfda95ee` | `engine-preserved-values.patch` |
| `finesub-0.6.0` | schema 6 | `eb642f25e7bc83cb18c4e7a4e0289d410c643fbc` | `engine-preserved-values-0.6.0.patch` |

其他提交须重新审阅、执行 `git apply --check` 和副本验证，不能仅凭版本号相同视为已验证。0.6.0 的 common 普通章节支持 note 与 term，元数据新增“重分类理由”；人工修改/认可历史通过注释渲染，不能将注释中的旧正文导入为新节点。两版规则分别固定，升级规则不会降低旧版的四列术语检查。

在管理设置的“知识库设置”选择与原生知识库匹配的版本。本次没有修改业务库中保存的配置，也没有重启原服务；后端代码需原服务下次正常重启加载。`legacy-preserve` 仍是原默认配置，其保留规则与当前严格配置不同。

信任边界是审核库中已经存下来的版本：当前 common/streamer 条目有旧式 `本名:` 等档案身份行，或原正文不符合当前章节/行体规则时，保留原结构并允许有限的值修订。首次建立旧基线须由管理员通过旧保留配置核实并导入，之后切换当前配置；下载包内的基线 JSON 不能自动授权创建旧格式新条目。

允许简介和可唯一定位的已有值编辑。原文不变可直接保留。禁止在兼容路径新增、删除、重排节点或章节，修改标题、字段名、日期前缀、关系目标、标记、名称、别名、类型、读音和术语前三列。术语只能改第四列描述；已有含“误听”提示的行暂不支持修改，以保留匹配用途。元数据日期由原生工具维护，不在审核正文中手动更新。新建条目及现代正文继续当前严格规则；style 和旧 translation 不走这条旧节点兼容路径。

仅改排版、保留原事实并不能证明用途不变。此方案不把 common 散文改成凭空补字段的术语，不把旧章节批量塞入“待归类”，不自动执行 Phase B 迁移。

## 交付文件及原生边界

- 两份 `engine-preserved-values*.patch`：补丁目标仅为 nonoka 中 `third_party/finesub/src/finesub/llm/knowledge/node/envelope.py`、`apply.py`；只应用对应版本的一份。
- `preserved_values.py`：根据已有原生节点建立带版本绑定的值编辑 Envelope，依据原节点类型定位值字段，保持其他载荷、归属边、匹配项和可见性。
- `apply_compat_release.py`：命令行入口，默认 dry-run；必须指定引擎源码、已有知识库和兼容包。
- `finesub-review-profile.json`：随当前格式发布包生成，声明审核规则、原生 schema、引擎来源。
- `finesub-review-compat.json`：随兼容知识库包生成，schema 2 清单增加目标规则和存储版本，包含原正文、原/目标 SHA256；不包含原生节点 ID，也不重建历史或签名。历史 schema 1 清单继续识别为 FineSub 0.5.1/schema 5，不能套用到 schema 6。

新增原生操作 `update_preserved_value` 只接受已有且带版本绑定的 `note.text`、`term.desc`、旧 `fact.value`、`event.description`、`relation.description`。类型、其他载荷、匹配项、归属边和可见性不能改变；身份字段与派生误听变化也拒绝。创建旧类型仍被禁止。对应版本的结构、章节限额、唯一性、引用和 CAS 校验继续执行；0.6.0 的整条目 token 预算属于注入限制，不额外把它改成写入禁令。

引擎默认 `edit_subject` 和 `harvest_rendered_edits` 没有改成兼容模式；单独安装补丁也不会把普通文本编辑/启动收割自动切换到此路径。请通过专用工具应用兼容条目，不直接覆盖 rendered 文件后让默认收割处理它们。

工具仅处理 manifest 声明的兼容条目。混合发布包的新格式文件和索引不会由该工具应用；须另行按当前原生流程导入，不能把本工具的成功当作完整混合包已写入。

## 先在完整副本验证

先确定实际运行的 nonoka、FineSub 模块目录及 Python/容器启动方式。本轮 schema 6 参考源码位于本机 Nonoka 源码仓库，但补丁仅应用于审核项目的隔离引擎副本，不代表运行中的安装已升级。

准备 nonoka 和知识库的完整隔离副本；暂停副本的其他写入者。先使用已有 SQLite 在线备份工具，备份会包含已提交的 WAL 内容，且检查数据库完整性和外键；再保留 rendered 及其 manifest。不要在知识库目录内部创建备份目录，不覆盖已有备份。

下面的变量须填成**副本**路径；本轮没有对真实库执行这些应用命令：

```powershell
$reviewRoot = 'E:\mikan\晒太阳\finesub-review'
$nonokaCopy = '填写nonoka隔离副本绝对路径'
$knowledgeCopy = '填写已有schema6知识库隔离副本绝对路径'
$backupRoot = '填写副本旁全新备份目录绝对路径'
$archive = '填写下载的兼容知识库包绝对路径'
$python = Join-Path $reviewRoot '.venv\Scripts\python.exe'
$patch = Join-Path $reviewRoot 'scripts\engine-preserved-values-0.6.0.patch'
# schema 5 使用 FineSub 0.5.1 源码和 engine-preserved-values.patch。

if (Test-Path -LiteralPath $backupRoot) { throw '备份目录已存在，请改用新目录' }
New-Item -ItemType Directory -Path $backupRoot
& $python (Join-Path $reviewRoot 'scripts\backup_sqlite.py') --source (Join-Path $knowledgeCopy 'knowledge.sqlite') --output (Join-Path $backupRoot 'knowledge.sqlite')
if ($LASTEXITCODE -ne 0) { throw '数据库备份失败，停止' }
Copy-Item -LiteralPath (Join-Path $knowledgeCopy 'rendered') -Destination (Join-Path $backupRoot 'rendered') -Recurse

git -C $nonokaCopy apply --check $patch
if ($LASTEXITCODE -ne 0) { throw '补丁与该副本不匹配，停止' }
git -C $nonokaCopy apply $patch
if ($LASTEXITCODE -ne 0) { throw '副本补丁未应用，停止' }

$finesubSrc = Join-Path $nonokaCopy 'third_party\finesub\src'
& $python (Join-Path $reviewRoot 'scripts\apply_compat_release.py') --finesub-src $finesubSrc --knowledge-root $knowledgeCopy --archive $archive
if ($LASTEXITCODE -ne 0) { throw 'dry-run失败，不应用' }
# 核对返回的 changed_nodes 和 operations 后，才应用到同一副本。
& $python (Join-Path $reviewRoot 'scripts\apply_compat_release.py') --finesub-src $finesubSrc --knowledge-root $knowledgeCopy --archive $archive --apply
```

没有 `--apply` 时先用 SQLite 在线备份建立临时快照，再在该快照中初始化原生引擎和预检，不对源库运行迁移、收割或生成修订。SQLite 只读连接仍可能生成空 WAL/共享内存辅助文件，这不改变数据库知识内容。工具在打开原生存储之前核对补丁、发布包规则和数据库 schema，拒绝 5/6 错配及未知版本，不自动升级或降级。当前原生投影与包中基线必须一致；比较忽略格式提示/人工版本注释、空行和显示元数据，其余主体、简介、章节及原始节点行逐项核对。两个完全相同的旧行无法唯一定位，或值无法无损映射、节点跨主体共用时，工具拒绝应用。

已有 rendered 文件含未收录修改时，工具拒绝并保留文件，不调用默认收割。兼容包的字段/目标哈希不符也拒绝。所有兼容条目的值更新组成一个 Envelope；修改节点或主体发生版本冲突时，原子模式整批回滚，不提交其中“还没冲突”的部分。成功后刷新原生投影并更新 manifest，不修改其他节点。

应用后读取相同主体，核对旧节点 ID/类型/章节、未编辑载荷、匹配项及 prompt projection，并检查数据库完整性。备份至少留到这些核对通过。实际知识库若需从备份恢复，应先停止所有写入者并保留失败现场，按已确认路径恢复，不能在仍有 WAL 写入时只覆盖一个数据库文件。本工具没有自动恢复或删除行为。

## 证据与验证

修改值应产生新 `value_hash`，旧值的证据仍绑定旧哈希，不能复制为新值已核验。原生 `user` 修订会正常追加用户编辑来源证据；本工具沿用这一机制。未修改值的证据、节点类型和核验/共享政策保持，未来核验依据新当前值枚举。本轮没有联网核验或共享。

schema 6 的 `line_acks` 保留原记录。修改过的正文值以原生 `user` 修订成为新人工版本，未来模型再改动时会显示该审核版本；未改行的“人改版/人认可版”提示、待确认别名及旧认可记录保持。只改注释不会生成修订，也不会批量认可其他行。这些历史提示不进入字幕提示词。

三个真实样例在隔离复制数据库中，分别只改 MyGO 专辑标题说明、写真拍摄说明和主播身高值，核验声明保持 6→6、10→10、7→7；旧节点 ID/类型、归属边、匹配项、共享政策及不相关载荷保持。另验证简介修改、旧证据保留、新哈希重新进入核验、原子冲突回滚，以及默认 dry-run、未打补丁拒绝、陈旧基线和未收录投影保护。

复现时显式提供未改的固定引擎源码和打补丁的隔离源码：

```powershell
$env:FINESUB_REFERENCE_SRC = '填写固定原版FineSub src绝对路径'
$env:FINESUB_COMPAT_SRC = '填写固定补丁版FineSub src绝对路径'
$env:REVIEW_KNOWLEDGE_TEST_ROOT = '填写只读真实知识源或其原始完整副本绝对路径'
$env:FINESUB6_REFERENCE_SRC = '填写固定原版0.6.0的src绝对路径'
$env:FINESUB6_COMPAT_SRC = '填写固定0.6.0补丁副本的src绝对路径'
$env:REVIEW_SCHEMA6_TEST_ROOT = '填写schema6完整知识库测试快照绝对路径'
Set-Location (Join-Path $reviewRoot 'backend')
& $python -m pytest tests -q -p no:cacheprovider --basetemp '填写全新测试临时目录绝对路径'
```

真实源仅用于读取正文和复制数据库；原生写入、证据追加及冲突测试均在临时副本。测试不连接原业务数据库，不安装引擎补丁，不推送、部署或重建 Docker。
