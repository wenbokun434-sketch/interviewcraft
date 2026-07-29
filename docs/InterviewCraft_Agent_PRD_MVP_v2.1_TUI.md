# InterviewCraft Agent - MVP 产品需求文档

> 版本：v2.1｜状态：可进入设计与研发评审｜日期：2026-07-30  
> 产品形态：开源、自托管优先的终端 TUI 应用  
> MVP 范围：文字模拟面试 + Coach Sidebar + 隔离代码面试；语音能力后置

---

## 1. Executive Summary

### 1.1 Problem Statement

求职者需要以自己的简历和目标岗位为依据练习面试，但通用题库无法形成真实的经历追问。用户在模拟中卡住时通常要离开训练界面查资料，既打断训练，也无法在复盘时区分“知识不会”“表达不清”与“解题过程不成熟”。

### 1.2 Proposed Solution

InterviewCraft Agent 将简历、目标岗位和 JD 结构化为候选人画像与面试剧本，提供文字面试、代码题和不打断主流程的 Coach Sidebar。会话结束后，系统综合简历/JD、主面试表现、代码测试结果及侧边栏求教记录，生成带证据的能力报告与下一轮训练计划。

### 1.3 Success Criteria

| 指标 | MVP 目标 | 统计口径 |
|---|---:|---|
| 首轮激活率 | ≥ 70% | 新用户从上传/粘贴简历到创建首个场景 |
| 文字模拟完成率 | ≥ 55% | 开始过面试且完成报告的会话 / 已开始会话 |
| Coach Sidebar 后续完成率 | ≥ 60% | 使用过侧边栏后仍完成会话的用户占比 |
| 报告证据覆盖率 | 100% | 每项评分、风险、建议均有事件/代码/简历证据，或显示“不足以判断” |
| Lite 部署可用率 | 100% | 按 README 在无 Docker、无外部数据库的环境中可启动文字面试主链路 |
| 代码隔离通过率 | 100% | Docker Runner 启用后，禁网/超时/内存限制测试全部通过 |

---

## 2. User Experience & Functionality

### 2.1 产品原则

1. **练习而非代答**：所有实时帮助服务于模拟训练；拒绝真实面试代答、隐蔽提示、伪造经历等请求。
2. **证据而非印象**：系统评价必须关联到用户原话、代码运行结果、已确认的简历事实或侧边栏事件。
3. **主线不被打断**：Coach Sidebar 与主面试分工明确，默认不停止题目计时、不替用户写入答案。
4. **单二进制优先**：MVP 只需一个终端程序、SQLite 与一个模型 Provider；代码 Runner、Docker 和本地语音均为可选扩展。
5. **可删除、可迁移**：简历、会话、代码、侧边栏历史和报告均能按用户选择导出或删除。

### 2.2 User Personas

| Persona | 目标 | 主要痛点 | MVP 优先需求 |
|---|---|---|---|
| 初级软件工程师 | 完成校招/初级岗位技术面 | 不知道如何讲项目，基础题和算法题易卡住 | 简历追问、文字模拟、代码题、轻提示 |
| 有经验工程师/AI 工程师 | 练项目深挖、系统设计和技术决策 | 有经验但表达冗长，难预判追问深度 | 角色化场景、事实锚点、深度追问、复盘 |
| 转岗求职者 | 把原经验映射到目标岗位 | 不清楚能力缺口，过度依赖通用答案 | JD 对齐、学习缺口地图、渐进训练计划 |
| 开源贡献者/自托管者 | 自己部署和扩展题库/Provider | 不想维护复杂基础设施 | 单命令启动、SQLite、适配器接口、清晰配置 |

### 2.3 关键用户旅程

1. **准备**：用户粘贴/上传简历，选择目标岗位、级别、JD 与场景类型。
2. **确认**：系统生成候选人画像；用户修正项目、技能、成果、待确认项。
3. **练习**：面试官发问，用户在中部回答；遇到问题时在右侧 Coach Sidebar 求教；代码题切换到代码工作台。
4. **复盘**：系统展示逐题证据、能力评分、代码结果、侧边栏知识缺口及下一轮训练卡。
5. **再训练**：用户一键从推荐的薄弱主题或原场景创建下一轮。

### 2.4 User Stories 与验收目标

#### US-01：创建候选人画像

**用户故事**：作为求职者，我希望将简历和目标岗位转化为可编辑画像，以便得到与自己经历相关的面试问题。

**验收目标**：

- 支持 PDF、DOCX、TXT（每个文件 ≤ 10MB）上传与纯文本粘贴；解析失败时仍可用粘贴文本继续。
- 在 60 秒内（5 页以内文本简历、标准模型响应条件）输出经历、项目、技能、教育、量化成果和待确认项。
- 每个“事实”关联原文 `source_span`；每个“推断”有 `confidence` 标识，且未确认推断不得作为既定履历出现在主面试问题中。
- 用户可编辑、删除、锁定字段；保存后重启 TUI，已确认画像保持一致。
- 用户执行删除后，源文件、解析文本、画像、全文索引和派生推荐在同一删除作业中被移除；UI 在 10 秒内显示结果或失败状态。

#### US-02：创建个性化面试场景

**用户故事**：作为求职者，我希望基于简历、JD、难度和时间生成一场可编辑的模拟面试，以便针对目标岗位练习。

**验收目标**：

- 内置行为面、项目深挖、基础技术、算法编码、系统设计、综合面 6 个模板。
- 每个 Scenario Plan 至少包含：题目、题目意图、预计时长、评分量表、简历/JD 证据锚点、最大追问次数与结束条件。
- 用户可删除/替换题目，修改难度、时长与模式；保存后生成的会话仅使用确认后的版本。
- 有 JD 时，系统展示至少 3 条“JD 要求 ↔ 简历证据/待补足项”映射；无 JD 时可以跳过，不阻塞创建。
- 对不含任何简历证据的深挖问题，系统必须标记为通用问题或改为澄清式问法。

#### US-03：进行文字模拟面试

**用户故事**：作为求职者，我希望在有限时间内连续回答面试官问题，以便练习真实面试的表达与节奏。

**验收目标**：

- 主面试一次仅呈现一个当前问题，并显示题号、剩余时间、模式、结束本题控制项。
- 用户提交回答后，面试官在 3 秒 P95 内进入下一状态：追问、收束或下一题（模型 Provider 不可用时给出重试/结束本题回退）。
- 面试官每题的追问次数不超过 Scenario Plan 中的 `max_follow_ups`（默认 2）。
- 面试官追问只可引用已确认履历、用户已提交回答、已运行代码或题目约束；日志可保存对应 `evidence_id`。
- 用户重启 TUI 或网络恢复后，能恢复到上一次已持久化的题目和已提交消息；未提交草稿以本地草稿事件保存，不上传给模型。
- 用户点击“结束本题/结束面试”后，系统需要二次确认；确认后不再继续提问，进入评估队列。

#### US-04：使用 Coach Sidebar 即时求教

**用户故事**：作为正在模拟的求职者，我希望不离开面试界面就能获得恰当提示，以便持续练习并留下真实的学习缺口。

**验收目标**：

- 终端宽度 ≥ 110 列时，Coach Sidebar 固定在右侧，可收起；终端宽度 < 110 列时，Coach 切换为全屏可返回的 overlay；不得丢失主回答草稿。
- 每个侧边栏事件至少存储：`session_id`、`question_id`、时间戳、意图、帮助层级、主题标签、用户理解状态和是否暂停主计时。
- 打开、提问、查看侧边栏默认不暂停主计时；只有用户点击“暂停并求教”后，事件写入 `pause_reason=coach_help`，计时才停止。
- 提供 6 个快捷入口：解释概念、给我提示、梳理回答结构、检查我的思路、解释报错/测试失败、加入复习。
- Coach 不得读取未提交的主回答草稿或未提交代码；Interviewer Agent 不得读取 Coach 回复正文。
- 严格模式默认只允许每题 1 次 L1/L2 帮助；不得输出当前题完整标准答案或完整可提交代码。常规模式默认每题 2 次；教练模式允许 L3/L4，但 L4 只能在本题结束或用户主动结束会话后提供。
- 用户可标记“已理解 / 仍困惑 / 加入复习”；标记为“仍困惑”与“加入复习”的主题必须出现在最终报告中。
- 用户可删除单条、单题或整场 Coach 历史；删除后的事件不得进入报告或长期推荐。

#### US-05：完成代码面试题

**用户故事**：作为技术岗位候选人，我希望在面试中解释和编写代码，并看到可信的测试结果，以便训练过程表达与编码能力。

**验收目标**：

- 代码题展示问题描述、输入输出、约束、至少 2 个示例、目标复杂度与评分维度。
- MVP 支持 Python、JavaScript、Java；用户可编辑、格式化、重置模板，草稿自动本地保存。
- Lite 模式默认关闭 Runner；Runner 未启用时，UI 显示“代码执行未启用”以及启用指引，文字面试与 Coach 不受影响。
- 启用 Docker Runner 后，用户代码运行在独立容器，默认禁网、非 root、只读基础镜像；每次运行销毁容器。
- 每次执行必须限制 CPU、内存、进程数与 wall-clock timeout；死循环、网络请求、内存滥用测试均不能影响主应用。
- 公开测试结果、错误类型、耗时、内存和代码快照作为会话事件持久化；隐藏测试仅返回通过/失败与受控诊断，不泄露测试数据。
- Coach 可解释已运行的报错或测试失败；严格模式不得生成可直接提交的当前题完整实现。

#### US-06：获得三源综合复盘报告

**用户故事**：作为完成模拟的求职者，我希望知道自己会什么、不会什么、为什么，以及下一轮该练什么。

**验收目标**：

- 报告至少包含：会话总览、逐题复盘、能力评分卡、代码结果（若有）、Coach 学习地图、三源交叉洞察、下一轮训练计划。
- 每个低分项、风险与建议必须关联用户答案、代码结果、已确认简历事实或 SidebarEvent；若缺少证据则显示“不足以判断”。
- 评分维度固定为：表达结构、经历可信度、技术深度、问题澄清、解题过程、代码质量、时间管理、独立完成度。无代码题时，代码质量标记为“不适用”而非零分。
- Coach 学习地图按主题聚合问题，并展示提问次数、最高帮助层级、理解状态、关联题目、关联简历技能/JD 要求。
- 提示后 5 分钟内的后续主回答或代码修正可作为“理解迁移”证据；没有足够事件时不得推断迁移成功/失败。
- 下一轮训练计划至少生成 3 项可执行内容：练习主题、场景/模式、时长、完成标准；用户可一键创建新场景。
- 用户可导出 Markdown 和 JSON；导出前可选择是否包含 Coach 原文。

#### US-07：以轻量方式部署并配置

**用户故事**：作为开源使用者，我希望不维护复杂基础设施就运行核心训练能力，并能按需启用本地模型与代码 Runner。

**验收目标**：

- 下载对应平台的单一发行二进制（或执行 `go install`）后，运行 `interviewcraft init` 与 `interviewcraft run` 可启动 Lite 模式；不要求 Node.js、Docker、PostgreSQL、Redis、消息队列、对象存储或向量数据库。
- `interviewcraft doctor` 必须检查终端尺寸、SQLite 可写性、数据目录、LLM Provider 连通性与可选 Docker Runner，并以非零退出码表示阻塞错误。
- Lite 默认配置为 `SQLite + data/ 本地目录 + RUNNER_MODE=disabled + AUDIO_PROVIDER=browser`。
- 系统启动时自动创建数据库、迁移和 `~/.interviewcraft/{uploads,exports,logs}` 目录；失败时输出明确的路径或权限错误。
- LLM Provider 至少支持 OpenAI-compatible API 和 Ollama；缺少 Key 或 Ollama 不可达时，配置页能检测并给出修复步骤。
- 通过单一环境变量启用可选能力：如 `RUNNER_MODE=docker`、`ASR_PROVIDER=whisper`；缺失依赖仅禁用对应功能，不得导致主应用启动失败。
- 提供 `interviewcraft export` 和 `interviewcraft import`；导出后可在另一 Lite 实例导入并恢复画像、会话、报告（密钥不导出）。

### 2.5 Non-Goals

- 语音 ASR/TTS 不是 MVP 发布阻塞条件；优先保证文字训练完整。
- 不提供真实面试实时帮助、隐身模式、屏幕规避或自动回答。
- 不做视频面试分析、人脸/情绪/人格评分或招聘录用预测。
- 不默认使用向量数据库、队列、微服务、复杂权限系统或云端观测平台。
- 不在 MVP 中做多人协作、导师批注、账号付费、自动投递或 ATS 集成。

---

## 3. AI System Requirements

### 3.1 Agent 职责与工具边界

| 组件 | 负责什么 | 不负责什么 |
|---|---|---|
| Profile Agent | 简历/JD 结构化、事实/推断分层、证据定位 | 虚构候选人经历 |
| Scenario Planner | 创建题纲、评分量表、追问限制和时间预算 | 自由改变已确认的场景规则 |
| Interviewer Agent | 发问、根据证据追问、收束问题 | 读取 Coach 回复、替用户组织答案 |
| Coach Agent | 概念解释、分层提示、代码错误解释、知识标记 | 真实面试代答、严格模式完整解法 |
| Evaluator Agent | 基于证据评分、生成学习缺口与训练计划 | 无证据的性格判断或能力断言 |
| Policy Gate | 提示层级控制、违规请求拒绝、隐私检查 | 代替用户确认事实 |

### 3.2 Prompt 与输出契约

- 所有 Agent 输出使用 Zod/JSON Schema 校验；校验失败最多重试 1 次，仍失败则降级为“无法生成，请重试”。
- `ProfileFact` 必须包含 `source_span`；`ProfileInference` 必须包含 `confidence` 和 `needs_confirmation=true`。
- `InterviewerAction` 必须包含 `action`、`question_id`、`message`、`evidence_ids`、`session_state`。
- `CoachResponse` 必须包含 `intent`、`help_level(L1-L4)`、`knowledge_tags`、`recommended_action` 与可选 `policy_note`。
- `EvaluationFinding` 必须包含 `dimension`、`score|not_applicable`、`evidence_ids`、`confidence`、`next_action`。

### 3.3 AI 质量评估策略

| 测试集 | 规模 | 通过门槛 | 方法 |
|---|---:|---:|---|
| 简历事实归因 | 50 份合成简历 + 标注题目 | ≥ 95% 题目引用正确事实；0 条虚构事实 | 人工双人审阅 + 自动 span 校验 |
| 场景相关性 | 30 份简历/JD 组合 | ≥ 85% 审阅者认为题目与岗位/简历相关 | 5 分量表，≥4 分视为相关 |
| 追问连贯性 | 100 段多轮会话 | ≥ 90% 不重复、不过度追问 | 状态机断言 + 人工抽样 |
| Coach 边界 | 50 个严格模式诱导请求 | 100% 不给当前题完整可提交答案 | 政策单测 + 对抗测试 |
| 报告证据性 | 50 份完成会话 | 100% 结论有 evidence 或“不足以判断” | JSON schema 与链接完整性检查 |
| 学习缺口归类 | 100 条人工标注 SidebarEvent | Macro-F1 ≥ 0.80 | 知识/表达/迁移/策略四分类 |

---

## 4. Technical Specifications

### 4.1 Architecture Overview（Lite 默认）

```text
Terminal Emulator
  └─ InterviewCraft TUI (single process / local state)
       ├─ Screens: training / profile / scenario / interview / report / settings
       ├─ Profile / Scenario / Interview / Coach / Evaluation modules
       ├─ Policy gate + JSON Schema validation
       ├─ LLM adapters: OpenAI-compatible | Ollama
       └─ SQLite + ~/.interviewcraft/ directory
                         │ optional profile only
                 Docker Code Runner (disabled by default)
```

**ADR-01（建议，待实现 POC 确认）**：使用 Go 构建单一跨平台二进制，TUI 采用 Bubble Tea/Lip Gloss 或等价库，SQLite 作为嵌入式数据层。选择 Go 的理由是发行包可直接执行、内存占用可控、跨平台静态构建成熟；LLM 和 Docker Runner 均通过标准 HTTP/CLI 适配。若 POC 证明 TypeScript TUI 能以单文件可执行包达到相同的安装体验，可替换实现，但不得改变终端优先、SQLite、无常驻服务的产品约束。

### 4.2 Repository Structure

```text
interviewcraft/
├─ cmd/interviewcraft/  # init / run / doctor / export / import 子命令
├─ internal/tui/        # Screen、Pane、快捷键、终端尺寸适配
├─ internal/core/       # 状态机、Agent 契约、评分、Policy Gate
├─ internal/adapters/   # LLM、speech、storage、runner Provider
├─ internal/db/         # SQLite migrations、queries、数据模型
├─ content/             # 场景模板、rubric、合成样例
├─ docker/runner/       # 可选 Runner 镜像和限制配置
├─ scripts/             # release、healthcheck、E2E 测试
└─ docs/                # 部署、安全、贡献、ADR
```

### 4.3 Data Model

| 实体 | 关键字段 | 说明 |
|---|---|---|
| CandidateProfile | facts、inferences、projects、skills、target_role、confirmed_at | 事实与推断分层 |
| Scenario | template、questions、rubric、mode、time_budget、prompt_version | 用户确认后不可被 Agent 随意改写 |
| SessionEvent | speaker、question_id、content、timestamp、evidence_refs | 主面试、代码、状态切换的统一事件流 |
| SidebarEvent | intent、help_level、tags、outcome、paused_timer | 侧边栏求教与学习状态 |
| CodeSubmission | language、source、test_result、runtime_stats、snapshot_id | Runner 启用时产生 |
| Report | scorecard、findings、learning_gaps、practice_plan | 每个 finding 关联证据 |
| ProviderConfig | provider、model、secret_ref、enabled | 密钥只保留引用或本地加密值 |

### 4.4 Integration Points

| 集成 | MVP 方案 | 失败降级 |
|---|---|---|
| LLM | OpenAI-compatible HTTP 或 Ollama | 禁止开始新场景，保留历史与草稿；配置页给出诊断 |
| 数据库 | SQLite / Drizzle | 显示本地路径/权限错误，不吞掉异常 |
| 代码执行 | Docker Runner，可选 | 代码运行按钮禁用；文字和 Coach 可正常工作 |
| 文件解析 | 本地 PDF/DOCX/TXT extractor | 提示粘贴纯文本继续 |
| 语音（后续） | 本机麦克风/ASR Adapter | 自动回退为文字输入 |

### 4.5 Security & Privacy

- 默认只在本地终端运行，不开放监听端口，不要求注册、OAuth、邮件服务或公网数据库。
- 简历、回答、代码、Coach 历史默认不用于模型训练；云 Provider 的数据处理提示须在设置页可见。
- 未提交的主回答草稿只存本地 SQLite 草稿表；Coach 和主面试严格隔离上下文。
- Lite 模式支持会话级和账户级数据删除；导出不包含 API Key。
- 启用 Runner 时必须为禁网、非 root、只读镜像、资源配额、临时容器；Runner 健康检查失败即禁用执行。

---

## 5. TUI 界面框架与交互规格

### 5.1 视觉方向：Terminal Field Notes

设计主题：面向认真练习技术面试的人，界面像一份可执行的“面试日志”，而不是带霓虹特效的聊天产品。OpenCode 的终端优先、可主题化与会话化体验是参考方向；本产品直接采用终端 TUI，把问题、回答、代码与求教组织为清晰的窗格，而非在浏览器中模拟终端。OpenCode 官方文档将其定位为终端/桌面/IDE 可用的开源 AI agent，并支持主题化界面。[官方介绍](https://opencode.ai/docs) [主题说明](https://opencode.ai/docs/themes/)

**设计 Token**

| 角色 | Token | 用途 |
|---|---|---|
| Canvas | `#10110E` Ink terminal | 全局背景，不使用渐变 |
| Surface | `#181A15` Panel | 面板、编辑器、对话区 |
| Rule | `#3C4035` Grid line | 1px 分隔线、焦点边框 |
| Ink | `#E8E7DF` Paper | 主要文本 |
| Signal | `#D7FF54` Signal lime | 进行中、主要 CTA、通过状态；只作状态信号 |
| Warning | `#FFC857` Amber | 时间/提示额度/需注意状态 |
| Error | `#FF6B5B` Red | Runner 错误、阻塞错误 |

**字体与微像素规则**

- 字体由终端模拟器决定；推荐 `JetBrains Mono` / `Maple Mono`，中文回退 `Noto Sans Mono CJK SC`。TUI 仅使用 ANSI 16 色基础调色板，尊重用户终端主题。
- 普通正文保持可读；状态标签、题号、计时器、快捷键使用短、固定宽度的终端符号。
- 使用 box-drawing 字符、单线分隔和反白焦点；不使用图片、阴影、圆角或鼠标依赖交互。
- **标志性交互**：回答、追问、求教、代码运行均写入左侧的 `Answer Trace` 事件轨；事件在发生时像终端日志一样逐行落入，不做装饰性动画。这让用户看到“自己如何思考”，也直接服务复盘证据。

### 5.2 全局框架

```text
┌ InterviewCraft ───── [t]rain [p]rofile [r]eport [s]ettings ── ● local ┐
│ ANSWER TRACE     │ current screen                                         │
│ 14:02 scene      │                                                        │
│ 14:03 ask Q1     │                                                        │
│ 14:04 coach      │                                                        │
│ 14:06 submit     │                                                        │
│                  │                                                        │
├──────────────────┴────────────────────────────────────────────────────┤
│ ↑↓ select · Tab next pane · Enter confirm · ? help · q quit           │
└───────────────────────────────────────────────────────────────────────┘
```

- 左侧 `Answer Trace`：宽度为当前终端 20 列；仅展示当前会话的重要事件。非会话 Screen 展示最近训练。
- 顶部状态栏：显示产品、快捷键、Provider 健康点；按 `s` 进入设置与诊断。
- 主内容占满终端可用行列，不使用浮动卡片；所有核心操作可由键盘完成。
- 终端 ≥ 140 列显示 Trace + Main + Coach 三栏；110–139 列隐藏 Trace；<110 列时 Coach 以可返回的 overlay 打开。最低支持 80 列 × 24 行，并显示尺寸不足提示。

### 5.3 Screen P-01：训练主页

**页面目标**：让用户继续未完成训练，或在 1 次点击内创建新训练。

```text
┌─ TRAINING / 07.30 ─────────────────────────────────────── [ + 新建 ] ┐
│ next:  Redis 缓存一致性 · 15 min · Coach: 1 hint                     │
│ [继续训练]  [查看上轮报告]                                             │
├─────────────────────────┬────────────────────────────────────────────┤
│ RECENT SESSIONS         │ PRACTICE QUEUE                             │
│ > 后端项目深挖  72/100  │ [01] 项目指标表达             10 min       │
│ > 双指针编码    64/100  │ [02] 缓存一致性               15 min       │
│ > 行为面        78/100  │ [03] 复杂度分析               15 min       │
└─────────────────────────┴────────────────────────────────────────────┘
```

| 元素 | 行为 | 验收目标 |
|---|---|---|
| 继续训练 | 恢复未完成 Session | 恢复到最后持久化事件，草稿从本地恢复 |
| 新建训练 | 打开 P-02 | 无画像时先进入简历输入；已有画像则预填 |
| 最近训练 | 打开对应报告 | 显示场景、日期、状态、总分/不适用状态 |
| Practice Queue | 一键生成带预填主题的新场景 | 主题来自 Report 的 `practice_plan`，可编辑后开始 |

### 5.4 Screen P-02：简历与目标岗位工作台

**页面目标**：完成简历输入、画像确认与目标岗位定义；避免模型黑箱。

```text
┌─ PROFILE / input ─────────────────────────────────────── [Enter: 保存] ┐
│ file [~/resume.pdf________________]  or  [ 粘贴简历文本........... ] │
├─────────────────────────┬────────────────────────────────────────────┤
│ TARGET                  │ PROFILE / editable                         │
│ role  [Backend Engineer]│ PROJECTS                                    │
│ level [Junior       v]  │ > Payment platform · owner · +32%          │
│ JD    [paste optional]  │ SKILLS                                      │
│ language [中文     v]   │ Redis  Node.js  PostgreSQL                  │
│                         │ ? needs confirmation: “led 5 engineers”    │
└─────────────────────────┴────────────────────────────────────────────┘
```

| 元素 | 行为 | 验收目标 |
|---|---|---|
| 文件路径/粘贴 | 输入本地 PDF/DOCX/TXT 路径或粘贴文本，开始解析 | 路径不可读时提示具体路径；失败不丢粘贴内容；60 秒内出现结果或失败 |
| Target 表单 | 设置角色、级别、JD、语言 | 可跳过 JD；字段校验失败时定位具体字段 |
| Profile 列表 | 行内编辑事实/推断，锁定或删除 | 推断标签可见；未确认推断不进入面试事实上下文 |
| 保存并继续 | 保存画像，进入 P-03 | 没有简历文本时禁止继续；保存成功可刷新恢复 |

### 5.5 Screen P-03：场景工厂

**页面目标**：在开始前让用户理解并控制本轮面试规则。

```text
┌─ NEW SCENARIO ───────────────────────────────────────────── [开始 →] ┐
│ template: [ 项目深挖 ]  mode: [ 常规 ]  time: [ 20 min ]              │
├──────────────────────────────────────────────────────────────────────┤
│ RUN PLAN                                                              │
│ [01] payment platform: 架构与个人贡献        6m  [Redis, ownership]  │
│ [02] 缓存一致性与失败恢复                    7m  [Redis, trade-off] │
│ [03] coding: LRU cache                       7m  [hashmap, list]    │
├──────────────────────────────────────────────────────────────────────┤
│ coach policy: Strict [1×L1/L2]  Standard [2×]  Coach [unlimited]     │
└──────────────────────────────────────────────────────────────────────┘
```

| 元素 | 行为 | 验收目标 |
|---|---|---|
| 模板/模式/时长 | 更新计划预览 | 切换后 3 秒内刷新题纲；保留用户手动编辑 |
| Run Plan | 删除/替换题目，查看事实锚点 | 每题显示 intent、时长与至少一个锚点/通用标识 |
| Coach policy | 展示提示上限和帮助规则 | 进入会话后规则被锁定并记录到 Session |
| 开始 | 创建 Session 并进入 P-04 | 保存 Scenario 的版本与 `prompt_version` |

### 5.6 Screen P-04：模拟面试室（文字 + Coach Sidebar）

**页面目标**：提供一条专注的主面试线和一条不打断的学习线。

```text
┌─ Q 02/03 · PROJECT DEEP DIVE ─────── 12:14 left ─ [结束面试] ┐
│ TRACE        │ INTERVIEW ROOM                            │ COACH        │
│ 14:03 Q1     │ > INTERVIEWER                              │ topic: Redis │
│ 14:05 answer │  请说明缓存失效时你如何保证一致性？          │ [解释概念]   │
│ 14:06 coach  │                                             │ [给我提示]   │
│ 14:07 Q2     │ > YOU                                      │ [回答结构]   │
│              │  [在这里输入回答........................]   │              │
│              │  [提交回答 ↵] [结束本题]                   │ > Coach      │
│              │                                             │ 先确认读写…  │
│              │                                             │ [已理解][复习]│
└──────────────┴────────────────────────────────────────────┴──────────────┘
```

| 元素 | 行为 | 验收目标 |
|---|---|---|
| 顶部状态条 | 显示题号、场景、计时、结束操作 | 每秒刷新计时；结束须二次确认 |
| Trace | 写入问答、Coach、运行、状态事件 | 事件顺序与 `SessionEvent.timestamp` 一致；可键盘聚焦阅读 |
| 主对话 | 提交文字、展示面试官回答/追问 | 提交后输入框清空，原文持久化；失败给重试而不重复写入 |
| Coach Sidebar | 快捷提问和自由输入 | 默认不暂停；帮助层级、额度与当前题绑定 |
| 暂停并求教 | 显式暂停并记录原因 | 仅该操作冻结计时；恢复操作产生独立事件 |
| 窄终端 | Coach 变为全屏 overlay | 打开 overlay 不丢失未提交文本；Esc 返回后焦点回到主输入框 |

### 5.7 Screen P-05：代码面试工作台

**页面目标**：让用户像在技术面中一样“说清楚、写出来、跑起来”，但在 Lite 运行中保持可选与安全。

```text
┌─ Q 03/03 · CODING / LRU CACHE ─────────────── 06:42 left ─────────────┐
│ SPEC / TRACE             │ EDITOR                                      │
│ Implement get / put      │ 01 class LRUCache:                          │
│ input · output · limits  │ 02   def get(self, key):                    │
│ expected: O(1)           │ 03     ...                                  │
│ [examples] [tests]       │                                             │
│                           │ [Run public tests] [Explain approach]      │
├──────────────────────────┴────────────────────────────────────────────┤
│ RUN: 3/4 public tests · 124ms · 32MB · TypeError at line 18            │
└────────────────────────────────────────────────────────────────────────┘
```

| 元素 | 行为 | 验收目标 |
|---|---|---|
| 题目规格 | 展示约束、示例、复杂度和测试入口 | 每题至少两个示例，长文本可折叠不遮挡编辑器 |
| 编辑器 | 语言切换、模板、草稿 | 支持 Python/JS/Java；刷新后恢复本地草稿 |
| Run | 调用可选 Docker Runner | Runner 禁用时按钮替换为配置说明；启用时状态流转可见 |
| 运行面板 | 显示公开测试、错误、耗时、内存 | 隐藏测试不泄露输入；错误不会泄露容器路径/宿主信息 |
| Coach 入口 | 解释已运行错误或检查思路 | 严格模式不产生完整解法；只读取提交/运行快照 |

### 5.8 Screen P-06：复盘报告

**页面目标**：把“本次表现”和“应该如何练”连接起来，而不是只显示一个分数。

```text
┌─ REPORT / PROJECT DEEP DIVE · 20m ────────────────── [导出 ▾] ┐
│ score 72  /  independent  B  /  3 questions / 2 coach prompts │
├─────────────────────┬─────────────────────────────────────────┤
│ SCORECARD           │ LEARNING MAP                            │
│ structure     4/5   │ [high] Redis consistency · 2 asks        │
│ technical      3/5  │ [medium] explain trade-offs · 1 ask      │
│ code           2/5  │ [transfer] hint used → answer improved   │
├─────────────────────┴─────────────────────────────────────────┤
│ NEXT RUN / 15 min                                                │
│ 01 cache consistency · strict · 1 hint  [创建训练]              │
│ 02 LRU variant       · standard · 2 hints [创建训练]            │
└────────────────────────────────────────────────────────────────┘
```

| 元素 | 行为 | 验收目标 |
|---|---|---|
| 会话总览 | 显示场景、时长、模式、提示、题目数 | 提示次数与 SidebarEvent 完全一致 |
| Scorecard | 展开分数对应的证据 | 无代码时显示“不适用”；每项可回跳到证据 |
| Learning Map | 聚合侧边栏主题与理解状态 | 显示次数、层级、关联题/JD/简历技能；支持查看原始事件 |
| 逐题复盘 | 对比问题、用户回答、追问、代码、建议 | 建议明确、可行动，最多 3 条关键改进项 |
| Next Run | 由 Practice Plan 生成 | 每项包含主题、时长、模式、完成标准；一键带预填创建 |
| 导出/删除 | Markdown、JSON；删除整场 | 导出可选择 Coach 原文；删除后报告和派生学习缺口均消失 |

### 5.9 Screen P-07：设置与本地运行状态

**页面目标**：让非 DevOps 用户看懂应用是否可用，并按需启用本地能力。

```text
┌─ SETTINGS / runtime ──────────────────────────────────────────┐
│ Lite mode  [● healthy]  DB: ~/.interviewcraft/interviewcraft.db│
├───────────────────────────────────────────────────────────────┤
│ LLM      [OpenAI compatible v]  model [....................]  │
│          [test connection]  status: ready                       │
│ Runner   disabled  [enable Docker runner]                       │
│ Speech   browser default  [configure later]                     │
├───────────────────────────────────────────────────────────────┤
│ Data     [export] [import] [delete all local data]              │
└────────────────────────────────────────────────────────────────┘
```

| 元素 | 行为 | 验收目标 |
|---|---|---|
| 健康状态 | 显示 DB、LLM、Runner、数据目录状态 | 每项有 ready/warn/error 与具体修复说明，不能只显示红点 |
| Provider | 选择 OpenAI-compatible/Ollama，测试连接 | 密钥不回显、不写入日志；测试失败说明 endpoint/认证/模型问题 |
| Runner | 显式启用 Docker profile | 启用前显示安全要求；健康检查失败不开放 Run 按钮 |
| Data | 导入、导出、删除 | 删除必须二次确认；导出不包含密钥 |

---

## 6. Risks & Roadmap

### 6.1 Phased Rollout

| 阶段 | 周期 | 交付内容 | 发布门槛 |
|---|---|---|---|
| MVP-0 Foundation | 第 1–2 周 | Go TUI 单二进制、SQLite、画像 Schema、LLM Adapter、设置 Screen | 无 Docker 时可跑通简历解析/场景创建 |
| MVP-1 Core Interview | 第 3–5 周 | 文字面试、状态机、Trace、基础报告 | 完整文字主链路 E2E 通过 |
| MVP-2 Learning Loop | 第 6–7 周 | Coach Sidebar、提示策略、学习地图、Practice Queue | 严格模式边界和报告证据测试通过 |
| MVP-3 Optional Code | 第 8–10 周 | Docker Runner、三语言题、代码复盘 | Runner 隔离测试全通过；Lite 不受影响 |
| v1.1 Voice | 后续 | 终端可选麦克风/ASR/TTS Adapter、转写编辑 | 语音失败可 100% 回退文字 |
| v2.0 Collaboration | 后续 | 导师审阅、团队模板、匿名汇总 | 需重新评估身份、权限和数据隔离 |

### 6.2 Technical & Product Risks

| 风险 | 影响 | 缓解 | 监测信号 |
|---|---|---|---|
| 模型虚构简历事实 | 误导用户，降低信任 | facts/inferences 分层、source span、schema/对抗评测 | 虚构事实率 > 0 |
| Coach 造成提示依赖 | 训练失真 | 模式额度、L1-L4、独立完成度单列 | 每题 L2+ 使用次数 |
| 代码 Runner 被攻击 | 主机/数据风险 | 默认关闭、Docker 隔离、禁网、资源限制 | 容器逃逸/异常资源事件 |
| Lite 配置门槛仍过高 | 开源使用率低 | 单体、SQLite、清晰健康状态、最小 `.env` | 首次启动失败率 |
| LLM/网络不可用 | 会话中断 | 草稿本地保存、重试、Provider 健康检查、可切 Ollama | 请求失败率、P95 延迟 |
| 报告过度断言 | 用户误判能力 | evidence-first、置信度、不足以判断 | 无证据 finding 比例 |

---

## 7. MVP Definition of Done

- Lite 模式能仅凭单一发行二进制、SQLite 与单一 LLM Provider 启动；无需 Node.js、Docker、PostgreSQL、Redis、队列、对象存储或向量数据库。
- 用户可完成“简历 → 确认画像 → 编辑场景 → 文字模拟 → Coach 求教 → 证据化报告 → 创建下轮训练”的端到端流程。
- Coach Sidebar 满足上下文隔离、提示层级、严格模式边界、删除与报告沉淀要求。
- Docker Runner 未启用时，代码模块安全降级；启用后通过禁网、非 root、超时、内存和临时容器销毁测试。
- 所有核心功能均有自动化 E2E、schema、权限/安全与 AI 质量测试；所有 AI 结论带证据或“不足以判断”。
- P-01 至 P-07 在 160×48、120×36、80×24 三种终端尺寸完成可用性检查：键盘操作、焦点状态、文本截断、overlay/侧栏切换均无阻塞问题。
- README 提供发行二进制、`init/run/doctor` 命令、Lite/Private Local/Full Practice 三档部署说明、环境变量表、数据迁移、Runner 安全说明和贡献指南。
