# InterviewCraft

InterviewCraft 是一款本地优先、面向终端的面试训练工具。它以候选人确认过的简历事实为基础，组织训练场景、文字面试、分级 Coach 提示、可选代码练习、证据化报告以及下一轮训练计划。

Lite 版本采用单个 Go 二进制文件和内嵌 SQLite：

- 默认不需要 Docker、Node.js、Java、Python 或外部数据库；
- `RUNNER_MODE` 默认保持为 `disabled`；只有 Full setup 完成签名、digest、镜像策略与隔离 smoke 检查后才会原子启用；
- 支持 OpenAI-compatible 和 Ollama 模型服务；
- 简历、草稿、训练记录、代码证据和报告默认保存在本机；
- 报告中的结论必须关联持久化证据，证据不足时明确标记为“证据不足”。

项目公开地址：[github.com/wenbokun434-sketch/interviewcraft](https://github.com/wenbokun434-sketch/interviewcraft)

## 目录

- [当前实现状态](#当前实现状态)
- [功能概览](#功能概览)
- [运行要求](#运行要求)
- [安装](#安装)
- [五分钟快速开始](#五分钟快速开始)
- [配置模型 Provider](#配置模型-provider)
- [三种部署方式](#三种部署方式)
- [命令行使用说明](#命令行使用说明)
- [训练流程与键盘操作](#训练流程与键盘操作)
- [本地数据目录](#本地数据目录)
- [导出迁移与恢复](#导出迁移与恢复)
- [启用 Docker Runner](#启用-docker-runner)
- [升级与回滚](#升级与回滚)
- [常见问题排查](#常见问题排查)
- [从源码验证和发布](#从源码验证和发布)
- [安全与隐私边界](#安全与隐私边界)
- [贡献与许可证](#贡献与许可证)

## 当前实现状态

MVP 的领域服务、SQLite 持久化、P-01～P-07 屏幕模型、三语言 Runner、隔离攻击测试、证据化报告和完整 E2E 已实现并具有自动化测试。

`interviewcraft run` 现在会启动常驻终端事件循环，并要求 stdin/stdout 都是交互终端。CI、脚本或重定向输出请使用 `interviewcraft run --once` 渲染单帧。

`setup`、`init`、`doctor`、完整常驻 `run`、`export` 和 `import` 均可直接使用。训练屏幕通过同一事件循环完成完整业务闭环；退出后仍保留已经持久化的训练状态与草稿。

## 功能概览

- 支持 PDF、DOCX、TXT 和粘贴文本的简历提取；
- 将简历内容拆分为有原文位置的事实和待确认推断；
- 根据已确认事实生成可编辑的训练场景；
- 文字面试回答先落库，再调用 Interviewer Provider；
- Coach 支持 L1～L4 分级提示、严格模式和上下文隔离；
- 内置 Python、JavaScript、Java 三语言代码题模板；
- Docker Runner 可执行公开测试和隐藏测试，但只暴露安全摘要；
- 报告包含八个固定维度、逐题复盘、学习缺口和下一轮计划；
- 支持 Markdown/JSON 报告导出和完整 Lite 数据迁移包；
- 支持 160×48、120×36、80×24 三档终端布局，以及 ASCII、无颜色和减弱动效模式。

## 运行要求

| 项目 | Lite | Private Local | Full Practice |
| --- | --- | --- | --- |
| InterviewCraft 二进制 | 必需 | 必需 | 必需 |
| SQLite | 已内嵌 | 已内嵌 | 已内嵌 |
| 模型 Provider | 新建 AI 训练时必需 | 本地 Ollama | OpenAI-compatible 或 Ollama |
| Docker | 不需要 | 不需要 | 仅代码执行需要 |
| Node.js / Java / Python | 不需要 | 不需要 | 位于 Runner 镜像内，宿主不需要 |
| 最小终端 | 80 列 × 24 行 | 80 列 × 24 行 | 80 列 × 24 行 |

从源码构建需要 Go 1.26 或更高版本。Windows 下执行仓库脚本时，需要 PowerShell 7，或使用 Windows PowerShell 的 `-ExecutionPolicy Bypass` 参数。

## 安装

### 方式一：一键验证安装（推荐）

Windows PowerShell 5.1 或 PowerShell 7：

```powershell
$installer = Join-Path $env:TEMP "interviewcraft-install.ps1"; Invoke-WebRequest "https://raw.githubusercontent.com/wenbokun434-sketch/interviewcraft/main/scripts/install.ps1" -OutFile $installer; & $installer; Remove-Item $installer
```

Linux 或 macOS：

```sh
curl -fsSL https://raw.githubusercontent.com/wenbokun434-sketch/interviewcraft/main/scripts/install.sh -o /tmp/interviewcraft-install.sh && sh /tmp/interviewcraft-install.sh && rm -f /tmp/interviewcraft-install.sh
```

安装器默认安装最新 Lite 版本，写入当前用户的 PATH，并执行 `setup → doctor`；不请求管理员或 root 权限。Windows 默认目录为 `%LOCALAPPDATA%\Programs\InterviewCraft\bin`，Linux/macOS 默认为 `$HOME/.local/bin`。可传入固定版本、档位和非敏感 Provider 参数，例如：

```powershell
$installer = Join-Path $env:TEMP "interviewcraft-install.ps1"; Invoke-WebRequest "https://raw.githubusercontent.com/wenbokun434-sketch/interviewcraft/main/scripts/install.ps1" -OutFile $installer; & $installer -Version 1.2.3 -Profile private-local -Provider ollama -Endpoint http://127.0.0.1:11434 -Model llama3.2 -NonInteractive; Remove-Item $installer
```

```sh
curl -fsSL https://raw.githubusercontent.com/wenbokun434-sketch/interviewcraft/main/scripts/install.sh -o /tmp/interviewcraft-install.sh && sh /tmp/interviewcraft-install.sh --version 1.2.3 --profile private-local --provider ollama --endpoint http://127.0.0.1:11434 --model llama3.2 --non-interactive && rm -f /tmp/interviewcraft-install.sh
```

API Key 只可通过 `-ApiKeyStdin`/`--api-key-stdin` 传入。安装器先用仓库固定 SHA-256 验证 Cosign v3.1.3，再验证发布清单的 Sigstore bundle、精确 GitHub Actions 发布者身份和 OIDC issuer，随后验证归档 hash/size、路径和内嵌版本，最后原子安装。同版本重复执行是幂等的；对已安装版本传入更新版本时，安装器转交内置可信更新器，不会直接覆盖现有二进制。

检查、升级和回滚：

```text
interviewcraft update --check
interviewcraft update
interviewcraft update --version 1.3.0
interviewcraft rollback
```

更新器严格执行 `check → download → verify → backup → switch → migrate → doctor → commit`。它先等待所有 SQLite 写进程退出并获取跨进程独占维护锁，再创建含二进制与完整数据目录的逐文件 SHA-256 备份。签名、checksum、磁盘、备份或锁检查在切换前失败时不改现状；切换后的迁移、doctor、Runner 复验、取消或断点失败会自动恢复旧二进制和匹配的数据目录。Windows 使用退出后的自替换 helper，Linux/macOS 使用同目录原子 rename。`update --check` 在没有新版本时成功退出，且不创建空备份。

卸载只依据无密钥安装收据删除自身二进制和 PATH 条目，默认保留配置、系统凭据和 `~/.interviewcraft`：

```powershell
$uninstaller = Join-Path $env:TEMP "interviewcraft-uninstall.ps1"; Invoke-WebRequest "https://raw.githubusercontent.com/wenbokun434-sketch/interviewcraft/main/scripts/uninstall.ps1" -OutFile $uninstaller; & $uninstaller; Remove-Item $uninstaller
```

```sh
curl -fsSL https://raw.githubusercontent.com/wenbokun434-sketch/interviewcraft/main/scripts/uninstall.sh -o /tmp/interviewcraft-uninstall.sh && sh /tmp/interviewcraft-uninstall.sh
```

也可以运行 `interviewcraft uninstall`。只有明确传入 `--purge-data`，并用 `--confirm-purge` 再次给出安装收据绑定的规范数据目录时，才会同时删除该目录、回滚备份和对应系统凭据；主目录、临时目录、工作目录、卷根目录、符号链接或与安装目录重叠的目标一律拒绝。例如先用 `interviewcraft doctor` 确认数据目录，再执行：

```text
interviewcraft uninstall --purge-data --confirm-purge "/exact/canonical/.interviewcraft"
```

### 方式二：手动下载发行包

如果 [Releases 页面](https://github.com/wenbokun434-sketch/interviewcraft/releases)已有构建产物，请同时下载平台压缩包、`release-manifest.txt` 和 `release-manifest.sigstore.json`。发行包命名格式为：

```text
interviewcraft_<版本>_<操作系统>_<架构>
```

Windows amd64：

```powershell
Get-FileHash .\interviewcraft_<版本>_windows_amd64.zip -Algorithm SHA256
Expand-Archive .\interviewcraft_<版本>_windows_amd64.zip -DestinationPath .\interviewcraft
.\interviewcraft\interviewcraft.exe --help
```

Linux amd64：

```sh
sha256sum interviewcraft_<版本>_linux_amd64.tar.gz
tar -xzf interviewcraft_<版本>_linux_amd64.tar.gz
./interviewcraft --help
```

macOS arm64：

```sh
shasum -a 256 interviewcraft_<版本>_darwin_arm64.tar.gz
tar -xzf interviewcraft_<版本>_darwin_arm64.tar.gz
./interviewcraft --help
```

正式发布清单使用 Tab 分隔，固定记录版本、Git commit、创建时间，以及六个平台归档、`checksums.txt` 和 SPDX SBOM 的 SHA-256/字节数。先使用 Cosign v3.1.3 验证发布者身份：

```sh
cosign verify-blob \
  --bundle release-manifest.sigstore.json \
  --certificate-identity "https://github.com/wenbokun434-sketch/interviewcraft/.github/workflows/release.yml@refs/tags/v<版本>" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  release-manifest.txt
```

再按清单校验归档 hash 与 size，并在解压后运行 `interviewcraft version --json` 核对版本、commit、构建时间和平台。Release 工作流会先创建 Draft，重新下载所有资产完成同样验证后才公开；`release-provenance.sigstore.json` 保存 GitHub 构建来源证明。若 Releases 暂无附件，请使用源码构建。

### 方式三：从源码构建

Windows PowerShell：

```powershell
git clone https://github.com/wenbokun434-sketch/interviewcraft.git
Set-Location interviewcraft
New-Item -ItemType Directory -Force .\bin | Out-Null
go mod verify
go build -trimpath -o .\bin\interviewcraft.exe .\cmd\interviewcraft
.\bin\interviewcraft.exe --help
```

Linux 或 macOS：

```sh
git clone https://github.com/wenbokun434-sketch/interviewcraft.git
cd interviewcraft
mkdir -p bin
go mod verify
go build -trimpath -o ./bin/interviewcraft ./cmd/interviewcraft
./bin/interviewcraft --help
```

仓库当前 `go.mod` 中的模块路径仍为 `github.com/interviewcraft/interviewcraft`，因此本文不建议使用个人仓库路径执行 `go install ...@latest`；直接克隆并构建最可靠。

## 五分钟快速开始

以下示例以 Windows PowerShell 为主。若二进制不在 `PATH`，请把 `interviewcraft` 替换为实际路径，例如 `.\bin\interviewcraft.exe`。

### 1. 选择数据目录（可选）

默认目录为当前用户主目录下的 `~/.interviewcraft`。也可以在第一次初始化前指定独立目录：

```powershell
$env:INTERVIEWCRAFT_DATA_DIR = "D:\InterviewCraftData"
```

### 2. 一键配置 Lite

```powershell
$env:INTERVIEWCRAFT_API_KEY = "<你的密钥>"
interviewcraft setup --profile lite `
  --provider openai-compatible `
  --endpoint https://provider.example/v1 `
  --model model-name `
  --api-key-env INTERVIEWCRAFT_API_KEY `
  --non-interactive
```

交互终端中省略 `--non-interactive` 会使用隐藏输入读取 API Key，并将其保存到系统凭据库。自动化中也可通过 `--api-key-stdin` 从标准输入读取；不支持 `--api-key <value>`，因此密钥不会进入进程参数。`setup` 可以重复执行，并会保留没有显式更改的现有 Provider 字段。

只需本地 Ollama 时：

```powershell
interviewcraft setup --profile private-local `
  --provider ollama `
  --endpoint http://127.0.0.1:11434 `
  --model llama3.2 `
  --non-interactive
```

已有脚本仍可继续使用幂等的 `interviewcraft init`；它不会覆盖已有配置。

### 3. 配置模型 Provider

使用 OpenAI-compatible 服务或 Ollama，具体示例见下一节。没有可用 Provider 时，历史数据和本地页面仍可读取，但不能创建依赖模型的新训练内容。

### 4. 运行健康检查

```powershell
interviewcraft doctor
```

阻塞问题会返回非零退出码。Runner 未启用或 Docker 不可用只产生警告，不会阻止 Lite 的文字训练主链路。

### 5. 渲染训练主页

```powershell
interviewcraft run
```

兼容能力有限的终端可以使用：

```powershell
interviewcraft run --once --ascii --reduce-motion --no-color
```

如“当前实现状态”所述，现阶段该命令渲染一次主页后退出。

<a id="configure-a-model-provider"></a>

## 配置模型 Provider

InterviewCraft 支持两种 Provider 类型：

- `openai-compatible`：兼容 OpenAI `/models` 和 `/chat/completions` 风格的服务；
- `ollama`：使用 Ollama `/api/tags` 和 `/api/chat` 接口。

API Key 的解析顺序为环境变量优先、系统凭据库其次。系统凭据库使用 Windows Credential Manager、macOS Keychain 或 Linux Secret Service；不可用时 setup 会明确要求环境变量，不会明文降级。`config.json` 只保存“密钥所在环境变量的名称”，不会保存密钥值。

### OpenAI-compatible：Windows PowerShell

```powershell
$env:INTERVIEWCRAFT_LLM_PROVIDER = "openai-compatible"
$env:INTERVIEWCRAFT_LLM_ENDPOINT = "https://provider.example/v1"
$env:INTERVIEWCRAFT_LLM_MODEL = "model-name"
$env:INTERVIEWCRAFT_LLM_API_KEY_ENV = "INTERVIEWCRAFT_API_KEY"
$env:INTERVIEWCRAFT_API_KEY = "<你的密钥>"

interviewcraft doctor
```

### OpenAI-compatible：Linux 或 macOS

```sh
export INTERVIEWCRAFT_LLM_PROVIDER=openai-compatible
export INTERVIEWCRAFT_LLM_ENDPOINT=https://provider.example/v1
export INTERVIEWCRAFT_LLM_MODEL=model-name
export INTERVIEWCRAFT_LLM_API_KEY_ENV=INTERVIEWCRAFT_API_KEY
export INTERVIEWCRAFT_API_KEY='<你的密钥>'

interviewcraft doctor
```

Provider 地址必须使用 `http://` 或 `https://`，不得把用户名、密码、查询参数或片段写入 URL。例如下面的写法会被拒绝：

```text
https://user:password@provider.example/v1?api_key=secret
```

### 本地 Ollama

先确保 Ollama 已运行并且目标模型已安装，再设置：

```powershell
$env:INTERVIEWCRAFT_LLM_PROVIDER = "ollama"
$env:INTERVIEWCRAFT_LLM_ENDPOINT = "http://127.0.0.1:11434"
$env:INTERVIEWCRAFT_LLM_MODEL = "<已安装模型名>"

interviewcraft doctor
```

### 环境变量表

| 环境变量 | 作用 | 默认值 |
| --- | --- | --- |
| `INTERVIEWCRAFT_DATA_DIR` | 配置、SQLite、上传、导出和日志目录 | `~/.interviewcraft` |
| `INTERVIEWCRAFT_LLM_PROVIDER` | `openai-compatible` 或 `ollama` | 未设置 |
| `INTERVIEWCRAFT_LLM_ENDPOINT` | 模型服务基础 URL | 未设置 |
| `INTERVIEWCRAFT_LLM_MODEL` | 模型名称 | 未设置 |
| `INTERVIEWCRAFT_LLM_API_KEY_ENV` | 保存 API Key 的另一个环境变量名称 | `OPENAI_API_KEY` |
| `RUNNER_MODE` | `disabled` 或 `docker` | `disabled` |
| `AUDIO_PROVIDER` | MVP 保留的音频 Provider 选择器 | `browser` |
| `COLUMNS` | 非交互/测试场景下覆盖终端列数 | 未设置时使用 120 |
| `LINES` | 非交互/测试场景下覆盖终端行数 | 未设置时使用 36 |

PowerShell 中通过 `$env:` 设置的变量只对当前进程及其子进程生效。生产部署时应使用操作系统服务管理器、容器编排环境或受控启动脚本注入变量，不要把密钥提交到仓库、写入 `config.json` 或放进命令行 URL。

## 三种部署方式

### 1. Lite：单机轻量部署

适合希望使用远程 OpenAI-compatible Provider，又不需要执行候选代码的用户。

组件：

```text
InterviewCraft 二进制
├─ 内嵌 SQLite
├─ 本地数据目录
└─ 一个远程或本地模型 Provider
```

部署步骤：

```powershell
$env:RUNNER_MODE = "disabled"
$env:INTERVIEWCRAFT_LLM_PROVIDER = "openai-compatible"
$env:INTERVIEWCRAFT_LLM_ENDPOINT = "https://provider.example/v1"
$env:INTERVIEWCRAFT_LLM_MODEL = "model-name"
$env:INTERVIEWCRAFT_LLM_API_KEY_ENV = "INTERVIEWCRAFT_API_KEY"
$env:INTERVIEWCRAFT_API_KEY = "<你的密钥>"

interviewcraft init
interviewcraft doctor
interviewcraft run
```

此模式不需要 Docker、PostgreSQL、Redis、消息队列、对象存储或向量数据库。

### 2. Private Local：本地私有模型部署

适合希望训练数据和模型请求都留在本机的用户。

组件：

```text
InterviewCraft 二进制 + 内嵌 SQLite + 本机 Ollama
```

建议只使用回环地址：

```powershell
$env:RUNNER_MODE = "disabled"
$env:INTERVIEWCRAFT_LLM_PROVIDER = "ollama"
$env:INTERVIEWCRAFT_LLM_ENDPOINT = "http://127.0.0.1:11434"
$env:INTERVIEWCRAFT_LLM_MODEL = "<已安装模型名>"

interviewcraft init
interviewcraft doctor
interviewcraft run --reduce-motion
```

只有在 Ollama 本身没有远程转发、遥测或外部调用的前提下，才能认为模型流量完全留在本机。InterviewCraft 不负责安装、启动或更新 Ollama。

### 3. Full Practice：启用隔离代码执行

适合需要 Python、JavaScript 和 Java 代码练习的用户。它是在 Lite 或 Private Local 之上增加可选 Docker Runner，不会改变主程序的 SQLite 架构。

部署顺序：

1. 安装并启动可信任的本地 Docker daemon；
2. 运行 Full setup；
3. setup 从对应应用版本的签名 Runner 清单解析当前架构 digest，拉取并验证 GHCR 镜像；
4. 签名、标签、版本、协议、默认用户和隔离 smoke 全部通过后，配置才切换为 `RUNNER_MODE=docker`；
5. `doctor` 在每次诊断时重新验证 daemon、签名和完整镜像策略。

```powershell
interviewcraft setup --profile full --restart
interviewcraft run
```

若需要退回 Lite：

```powershell
$env:RUNNER_MODE = "disabled"
interviewcraft doctor
```

已保存的代码证据仍保留在报告中，但不会再启动新的代码容器。

## 命令行使用说明

```text
interviewcraft <command>
```

| 命令 | 作用 | 重要行为 |
| --- | --- | --- |
| `interviewcraft init` | 初始化配置、目录和 SQLite | 幂等；保留已有配置 |
| `interviewcraft setup` | 选择部署档位并验证 Provider、SQLite 与可选 Runner | 可恢复；凭据不写入配置或参数 |
| `interviewcraft doctor` | 检查数据目录、SQLite、终端、Provider、可选 Runner | 阻塞错误返回 1；Runner disabled 只警告 |
| `interviewcraft version [--json]` | 输出 schema、版本、commit、构建时间与平台 | 源码构建显示 `dev/unknown`；发行构建由 ldflags 注入 |
| `interviewcraft run` | 启动完整常驻 TUI | 需要交互终端；单帧使用 `run --once` |
| `interviewcraft export` | 导出迁移包或单份报告 | 默认不包含 Coach 原文，不覆盖已有文件 |
| `interviewcraft import` | 导入完整迁移包 | 目标必须已初始化且没有训练数据 |

查看帮助：

```powershell
interviewcraft --help
interviewcraft setup --help
interviewcraft run --help
interviewcraft export --help
interviewcraft import --help
```

### `run` 选项

```text
--theme auto|dark|light
--ascii
--reduce-motion
--ansi-16
--no-color
```

示例：

```powershell
interviewcraft run --theme dark --ansi-16
interviewcraft run --ascii --reduce-motion --no-color
```

### `export` 选项

```text
--format package|json|markdown
--output <新文件>
--session <会话 ID>      # json/markdown 必需
--include-coach          # 显式包含 Coach 原文
```

如果省略 `--output`：

- `package` 默认写入数据目录的 `exports/interviewcraft-transfer.json`；
- `json` 默认写入 `exports/report-<session-id>.json`；
- `markdown` 默认写入 `exports/report-<session-id>.md`。

导出不会覆盖已存在的同名文件。

### `import` 选项

```text
--input <迁移包路径>
```

导入前会严格验证包版本、未知字段、ID、外键图和报告证据，整个写入在一个事务中完成；任一步失败都不会留下半导入状态。

## 训练流程与键盘操作

业务模型中的完整训练顺序为：

```text
简历输入
  → 确认事实与目标岗位
  → 生成/编辑/确认场景
  → 文字面试
  → 按需请求 Coach
  → 可选代码练习
  → 证据化报告
  → 创建下一轮训练
```

交互设计遵循以下统一规则：

- 全流程可使用键盘，不依赖鼠标；
- `?` 打开当前屏幕快捷键帮助；
- `Tab` 在可聚焦区域间移动；
- `Esc` 关闭帮助或 overlay，并恢复进入前的焦点和草稿；
- 面试主回答使用 `Ctrl+Enter` 提交，单独 `Enter` 只换行；
- Coach 在窄屏中以全屏 overlay 打开，返回后保留主回答光标；
- 代码工作台使用 `Ctrl+1/2/3` 切换 Python、JavaScript、Java；
- 代码工作台使用 `Ctrl+S/F/Z/R/E/H` 完成保存、格式化、重置、运行、解释错误和返回面试。

上述交互均由 `interviewcraft run` 的常驻事件循环接入，并由脚本化全链路测试覆盖。

## 本地数据目录

初始化后目录结构如下：

```text
~/.interviewcraft/
├─ config.json             # 非敏感运行配置
├─ setup-state.json         # 仅在未完成 setup 时存在，不含密钥
├─ interviewcraft.db       # SQLite 训练数据
├─ uploads/                # 本地导入材料
├─ exports/                # 默认导出位置
└─ logs/                   # 本地日志目录
```

`config.json` 不保存 API Key 值。SQLite 中包含简历文本、画像、草稿、训练事件、Coach 事件、代码快照和报告，应按私密个人数据保护。

如需切换目录，必须在 `init`、`doctor`、`run`、`export` 和 `import` 前使用一致的 `INTERVIEWCRAFT_DATA_DIR`。不要让两个正在写入的进程同时操作同一数据目录。

## 导出迁移与恢复

### 导出完整迁移包

```powershell
interviewcraft export --format package --output .\interviewcraft-transfer.json
```

默认迁移包：

- 包含画像、来源、场景、会话、主事件、草稿、代码证据和报告；
- 不包含 Provider 配置或 API Key；
- 不包含 Coach 回复原文，但保留安全的学习统计。

只有明确需要时才包含 Coach 原文：

```powershell
interviewcraft export --format package --include-coach --output .\with-coach.json
```

### 导出单份报告

```powershell
interviewcraft export --format markdown --session <会话 ID> --output .\report.md
interviewcraft export --format json --session <会话 ID> --output .\report.json
```

### 恢复到新实例

目标实例必须已经 `init`，但不能包含任何训练数据：

```powershell
$env:INTERVIEWCRAFT_DATA_DIR = "D:\InterviewCraftRestored"
interviewcraft init
interviewcraft import --input .\interviewcraft-transfer.json
interviewcraft run
```

导入不会覆盖目标实例已有的本地 Provider 配置。迁移后需要在新机器上重新注入 API Key 环境变量。

## 启用 Docker Runner

### 正式镜像与开发镜像

正式用户不需要从源码构建 Runner。发布流水线分别构建 linux/amd64 与 linux/arm64 镜像，以 keyless Sigstore 身份签名每个不可变 digest，并发布一个同样签名的严格 Runner 清单。启用命令是：

```text
interviewcraft setup --profile full --restart
```

setup 不会安装或启动 Docker，也不会请求管理员/root 权限。Docker、Registry、签名、digest、架构、标签、版本、协议、`65532:65532` 默认用户或隔离 smoke 任一检查失败时，Runner 保持 disabled，新增镜像引用会被清理，Lite 文字训练不受影响。

仓库开发者仍必须从仓库根目录运行，并将 `docker/runner` 作为唯一上下文构建本地测试镜像：

```powershell
docker build -t interviewcraft-runner:local docker/runner
```

该本地镜像只用于隔离攻击门，不能写入正式 Runner 配置或绕过发布签名。不要改为以下命令：

```text
docker build -f docker/runner/Dockerfile .
```

后者会把整个仓库发送给 Docker builder，扩大简历、Git 历史或其他本地文件进入构建上下文的风险。

如果需要代理，可通过脚本环境变量传入，不要在 Dockerfile 中持久化代理：

```powershell
$env:INTERVIEWCRAFT_RUNNER_BUILD_PROXY = "http://host.docker.internal:端口"
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-runner-isolation.ps1
```

如果默认 Alpine CDN 不可用，可以配置经过确认的 HTTPS 镜像站：

```powershell
$env:INTERVIEWCRAFT_RUNNER_ALPINE_MIRROR = "https://<可信镜像站>/alpine"
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-runner-isolation.ps1
```

### 隔离策略

每次代码执行使用全新容器，并强制：

- `--network none`、`--ipc none`；
- 只读根文件系统，不挂载宿主目录；
- 用户 `65532:65532`，禁止 root；
- `--cap-drop ALL`；
- `no-new-privileges=true`；
- CPU 0.50、内存/交换空间 256 MiB、PID 64；
- `nproc`、`nofile=64:64` 和有界 noexec tmpfs；
- 不转发宿主环境变量；
- wall-clock 超时；
- 成功、失败、超时、OOM、取消和协议错误后都强制删除容器与匿名卷。

对外结果只能包含公开测试名称/状态、隐藏测试通过/失败数量、枚举错误、耗时和峰值内存。隐藏输入、预期结果、测试源码、原始 stderr、宿主/容器路径和密钥没有公开协议字段。

完整安全边界见 [docs/SECURITY.md](docs/SECURITY.md)。

## 升级与回滚

### 原地升级

1. 停止所有正在使用当前数据目录的 InterviewCraft 进程；
2. 备份完整数据目录，而不只是 `interviewcraft.db`；
3. 替换二进制文件；
4. 保持 `INTERVIEWCRAFT_DATA_DIR` 不变；
5. 运行 `interviewcraft doctor`，让内嵌迁移按顺序执行；
6. 运行 `interviewcraft run` 并检查最近训练记录。

不要手工修改 `_schema_migrations`，也不要手工执行项目迁移 SQL。

### 回滚

如果升级失败：

1. 保留失败后的数据目录用于诊断；
2. 停止所有写入进程；
3. 恢复升级前的完整目录备份；
4. 恢复旧版二进制；
5. 重新运行 `doctor`。

不要在 SQLite 正在写入时直接复制单个数据库文件作为一致性备份。跨机器迁移优先使用 `export --format package`。

## 常见问题排查

### `尚未初始化 Lite 配置`

运行：

```powershell
interviewcraft init
```

并确认每次命令使用相同的 `INTERVIEWCRAFT_DATA_DIR`。

### `doctor` 报终端尺寸不足

将终端调整到至少 80×24。CI 或重定向输出场景可以显式设置：

```powershell
$env:COLUMNS = "120"
$env:LINES = "36"
interviewcraft doctor
```

### Provider 不可用

依次检查：

1. `INTERVIEWCRAFT_LLM_PROVIDER` 是否为 `openai-compatible` 或 `ollama`；
2. endpoint 是否为可访问的 HTTP(S) 基础地址；
3. endpoint 是否误带用户名、密码、查询参数或 `#fragment`；
4. 模型名称是否真实存在；
5. `INTERVIEWCRAFT_LLM_API_KEY_ENV` 指向的环境变量是否已设置；
6. 本地 Ollama 是否正在监听配置的回环端口。

### SQLite 或数据目录不可写

确认运行用户对整个数据目录拥有创建、写入、重命名和删除文件的权限。不要把数据目录放在只读安装目录。Windows 上还应检查杀毒软件、受控文件夹访问和同步软件是否占用数据库。

### Docker 不可用但只想使用文字训练

保持：

```powershell
$env:RUNNER_MODE = "disabled"
```

Runner 警告不会阻塞 Lite。不要为消除警告而安装不需要的 Docker。

### Runner 镜像不存在、签名无效或不健康

```powershell
interviewcraft setup --profile full --restart
interviewcraft doctor
```

不要只设置 `RUNNER_MODE=docker`。缺少与应用版本匹配的官方仓库、不可变 digest、协议和架构元数据时，配置验证会拒绝启用；也不要删除安全参数来换取语言测试通过。

### PowerShell 拒绝执行脚本

使用一次性进程级绕过，而不是修改全局策略：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1 -SkipRunnerIsolation
```

## 从源码验证和发布

### Docker-free Lite 门禁

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1 -SkipRunnerIsolation
```

它会检查：

- `gofmt` 和 `git diff --check`；
- 根模块与 Runner agent 模块的 `go mod verify`；
- `go vet ./...`；
- 根模块全量覆盖测试；
- SQLite 迁移测试；
- 嵌套 `docker/runner/agent` 测试；
- 当前平台单二进制构建和 fresh-install smoke；
- Windows、Linux、macOS × amd64、arm64 六目标交叉编译。

### 完整发布门禁

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-release-quality.ps1
```

完整门禁还会构建 Runner 镜像并执行 Python、JavaScript、Java 主流程，以及死循环、禁网、内存炸弹和进程炸弹隔离测试；最后要求专用集成容器残留为 0。

### 一键部署全链路验收

以下复制粘贴命令也是 CI 实际执行的命令。它在临时、隔离的用户目录中验证 Lite 与 Private Local 的安装、幂等重装、setup、doctor、完整训练、重启、可信升级、篡改拒绝、回滚和保留数据卸载，不会修改真实用户 PATH。

Windows PowerShell：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-deployment-e2e.ps1 -GoBinary go -EvidencePath .\deployment-evidence.json
```

Linux/macOS：

```sh
pwsh -NoProfile -File ./scripts/test-deployment-e2e.ps1 -GoBinary go -EvidencePath ./deployment-evidence.json
```

受支持的 Docker 主机还可追加 `-FullPractice`，验证签名 Runner 配置、Python/JavaScript/Java 和隔离攻击矩阵。证据 JSON 记录平台、架构、应用版本、Go 版本、提交 SHA、工作区是否有未提交修改、开始/结束时间、耗时和每项结果。仓库的 `deployment-e2e` 工作流在 Windows、Ubuntu 与 macOS 分别执行上述命令，并在 Ubuntu 另跑 Full Practice；本地没有相应系统或 Docker 时，应将该边界记为未运行，不能用交叉编译冒充真机通过。

仓库根目录的 `go test ./...` 不会自动进入嵌套模块 `docker/runner/agent`，不能把根模块单独通过当作完整发布通过。

详细证据矩阵见 [docs/QUALITY_GATES.md](docs/QUALITY_GATES.md)。发布归档配置见 [.goreleaser.yaml](.goreleaser.yaml)，CI 配置见 [.github/workflows/](.github/workflows/)。

## 安全与隐私边界

- 简历和训练数据默认保存在本机 SQLite；
- 使用远程模型时，经过角色策略选择的上下文会发送给该 Provider；
- 使用本地 Ollama 时，是否完全离线还取决于 Ollama 自身配置；
- API Key 值不写入配置、界面、报告或迁移包；
- Interviewer 看不到 Coach 回复正文；
- Coach 看不到未提交回答、未运行代码草稿或之前的 Coach 正文；
- Evaluator 不允许无证据的人格、录用或能力断言；
- Docker daemon 属于可信基础设施，Runner 不是面向不可信多租户的云沙箱；
- 本地数据库目前不提供应用层静态加密，需要时应使用操作系统磁盘或目录加密。

不要在公开 Issue 中提交真实简历、API Key、隐藏测试或容器逃逸细节。安全问题请使用 GitHub 仓库的私密 Security Advisory 渠道。

## 相关文档

- [产品需求文档](docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md)
- [终端设计系统](docs/DESIGN.md)
- [Lite Runtime ADR](docs/ADR-0001-lite-runtime.md)
- [部署与运维](docs/DEPLOYMENT.md)
- [安全模型](docs/SECURITY.md)
- [质量门与验收矩阵](docs/QUALITY_GATES.md)
- [贡献指南](CONTRIBUTING.md)
- [有序任务与验收记录](TODO.md)

## 贡献与许可证

贡献前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)，尤其是领域契约、迁移、Agent 上下文隔离和 Runner 安全规则。普通功能必须在 `RUNNER_MODE=disabled`、没有 Docker 的环境中通过测试。

当前仓库尚未包含 `LICENSE` 文件。代码虽然公开可见，但在选择并加入 MIT、Apache-2.0 等许可证之前，严格意义上尚未授予他人复制、修改和分发代码的开源许可。

## MVP 明确不做

- 不提供真实面试中的隐蔽辅助、屏幕规避或自动代答；
- 不做视频、人脸、情绪、人格、录用或招聘决策评分；
- 不强制引入向量数据库、消息队列、微服务或云端观测平台；
- 不在 MVP 中提供多人协作、导师批注、付费、自动投递或 ATS 集成；
- 语音 ASR/TTS 不是 MVP 发布阻塞条件，完整文字训练路径优先。
