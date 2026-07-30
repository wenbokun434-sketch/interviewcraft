# InterviewCraft MVP 开发清单

> 规范基线：`docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md`（v2.1）与 `docs/DESIGN.md`（v1.0）
> 实施策略：Go 单二进制 + TUI + SQLite；Lite 模式默认不依赖 Docker、Node.js、外部数据库、队列或常驻服务。

## 0. 强制执行协议

1. 必须严格按 T-001 → T-022 的顺序开发；前一项未通过，不得开始后一项。
2. 每轮只能有一个任务从 `[ ]` 变为 `[x]`；不得预先勾选、批量勾选或跳项。
3. 开始每轮前，必须在回复和本文件“执行记录”中明确：
   - 修改目标
   - 允许修改的范围
   - 不允许破坏的逻辑
   - 验收的标准
4. 只允许修改当前任务列出的文件或目录。发现需要越界时，停止本轮，先调整计划并说明理由。
5. 每个功能模块都必须验证：
   - 主流程
   - 加载中
   - 空数据
   - 接口/依赖报错
6. 对纯基础设施任务，若某类 UI 状态不适用，也必须在测试记录中明确写出 `N/A` 及原因，不得直接省略。
7. 每项任务必须依次完成：实现 → 格式化/静态检查 → 单元测试 → 模块测试 → 主链路回归 → 更新本文件（只勾当前项并写测试记录）→ 立即 Git 提交。
8. 测试未通过时不得勾选、不得提交“完成”提交；修复必须留在当前任务范围内。
9. 每项任务使用独立提交，提交信息格式：`type(scope): T-xxx concise summary`。
10. `docs/` 是需求基线。除非用户明确要求修订规范，否则开发任务不得修改其中内容。

## 1. 全局不可破坏约束

- 练习而非代答：拒绝真实面试代答、隐蔽提示、伪造经历；严格模式不得给出当前题完整答案或可直接提交代码。
- 证据而非印象：所有评分、风险和建议必须关联 `evidence_id`，否则显示“不足以判断”。
- 上下文隔离：Coach 不得读取未提交回答/代码；Interviewer 不得读取 Coach 回复正文。
- 事件不可篡改：已提交问答按追加事件保存，修正只能追加，不能静默改写历史。
- 草稿安全：焦点切换、Coach overlay、Provider 重试、终端 resize 与重启不得丢失本地草稿。
- Lite 可用：无 Docker 时文字面试、Coach、报告主链路仍可运行；可选依赖失败只禁用对应能力。
- 本地优先：默认不监听公网端口；密钥不回显、不写日志、不导出。
- 删除完整：删除源数据时同步移除派生画像、索引、报告、学习缺口与推荐。
- TUI 一致性：功能代码只使用语义色彩 Token 和复用组件；无鼠标专属操作、无仅靠颜色表达的状态。
- 终端兼容：支持 160×48、120×36、80×24；小于 80×24 必须进入阻塞提示；支持 `--ascii` 与 `--reduce-motion`。

## 2. 严格顺序任务

### [x] T-001 仓库与 Go 单二进制骨架

- 修改目标：建立可构建、可测试、可运行的 Go 项目和 `interviewcraft` CLI 空壳。
- 允许修改的范围：`.gitignore`、`go.mod`、`go.sum`、`README.md`、`cmd/interviewcraft/`、仅为构建元信息所需的 `internal/` 子包、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得实现业务页面、数据库、Provider 或 Runner；不得修改 `docs/`；不得引入 Node.js 或常驻服务。
- 验收的标准：
  - `go test ./...` 通过。
  - `go build ./cmd/interviewcraft` 通过并生成单一可执行程序。
  - `interviewcraft --help` 能列出 `init/run/doctor/export/import` 命令占位，未知命令返回非零退出码和可行动提示。
  - 四态测试：主流程=帮助命令；加载中=N/A（无异步过程）；空数据=无配置时帮助仍可用；报错=未知命令。
  - 主链路回归：CLI 可启动且不要求 Docker、Node.js、外部数据库。
- 完成后提交：`chore(cli): T-001 bootstrap Go command`

### [x] T-002 领域契约、异步状态与类型化错误

- 修改目标：定义核心实体、Agent 输出契约、`Pending/Streaming/Succeeded/Failed` 状态和可恢复领域错误。
- 允许修改的范围：`internal/core/`、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：事实与推断不得混淆；`ProfileFact.source_span`、`ProfileInference.confidence/needs_confirmation`、各 Agent 必填字段不得省略；渲染层不得产生临时错误文案。
- 验收的标准：
  - 核心 JSON Schema/Go 校验覆盖 Profile、Scenario、InterviewerAction、CoachResponse、EvaluationFinding。
  - 非法输出自动重试上限可表达为 1 次，仍失败时生成类型化回退错误。
  - 四态测试：主流程=合法契约；加载中=Pending/Streaming 转换；空数据=必填字段缺失；报错=非法枚举/证据引用。
  - 主链路回归：T-001 CLI 测试继续通过。
- 完成后提交：`feat(core): T-002 add domain contracts and async states`

### [x] T-003 SQLite 初始化、迁移与本地目录

- 修改目标：实现 SQLite 数据层、迁移和本地数据目录创建，支撑画像、场景、会话、草稿、Coach、代码与报告。
- 允许修改的范围：`internal/db/`、`internal/adapters/storage/`、迁移文件、对应测试、SQLite 纯 Go 驱动所需的 `go.mod/go.sum`、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得静默吞掉写入错误；密钥只存引用或受保护值；已提交事件必须追加写；删除需要事务边界。
- 验收的标准：
  - 首次启动可创建数据库及 `uploads/exports/logs` 目录，迁移可重复执行。
  - CRUD、事务回滚、草稿恢复、事件顺序和级联删除测试通过。
  - 四态测试：主流程=迁移与读写；加载中=迁移状态事件；空数据=新库查询；报错=只读目录/损坏迁移给出路径和恢复动作。
  - 主链路回归：CLI 在无 Docker、无网络环境可启动。
- 完成后提交：`feat(storage): T-003 add SQLite migrations and local data`

### [x] T-004 `init`、`doctor` 与运行时配置

- 修改目标：实现 Lite 初始化、配置加载和健康诊断。
- 允许修改的范围：`cmd/interviewcraft/`、命令分发所需的 `internal/cli/`、`internal/config/`、`internal/doctor/`、相关测试与文档、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：默认必须为 SQLite、本地数据目录、`RUNNER_MODE=disabled`；可选依赖失败不得阻止 Lite 启动；密钥不得输出。
- 验收的标准：
  - `interviewcraft init` 可安全重复执行。
  - `interviewcraft doctor` 检查终端尺寸、SQLite、数据目录、LLM 配置与可选 Runner；阻塞错误返回非零退出码。
  - 四态测试：主流程=健康配置；加载中=逐项诊断状态；空数据=首次无配置引导；报错=不可写目录/无效 Provider 配置。
  - 主链路回归：`init → doctor` 在无 Docker 下完成。
- 完成后提交：`feat(runtime): T-004 implement init and doctor`

### [x] T-005 TUI 主题、基础组件与响应式 AppShell

- 修改目标：实现语义主题、稳定上下栏、焦点模型和 DESIGN 规定的核心渲染组件。
- 允许修改的范围：`internal/tui/theme/`、`internal/tui/components/`、`internal/tui/layout/`、快照/组件测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得使用功能局部原始颜色；不得嵌套超过两层 Pane；不得依赖颜色或鼠标；不得假设 Unicode 固定宽度。
- 验收的标准：
  - 实现 `AppShell/Pane/SectionLabel/KeyHint/SelectableList/TextComposer/StatusBadge/ConfirmPrompt/InlineNotice/ProgressLine/ActivityLine`。
  - `auto/dark/light`、ANSI-16、`--ascii`、`--reduce-motion` 可切换。
  - 四态测试：主流程=聚焦与键盘导航；加载中=Progress/Activity；空数据=SelectableList/TextComposer；报错=InlineNotice/阻塞状态。
  - 160×48、120×36、80×24 与小于 80×24 快照通过，包含 CJK、长路径和长模型名。
  - 主链路回归：CLI 与存储测试继续通过。
- 完成后提交：`feat(tui): T-005 build design-system primitives`

### [x] T-006 P-01 训练主页与导航骨架

- 修改目标：实现训练主页、全局导航、最近训练和 Practice Queue 的数据接口。
- 允许修改的范围：`internal/tui/screens/training/`、必要的查询层、`internal/cli/` 的 `run` 最小启动接线、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：每屏只能有一个主行动；分数必须带维度；继续训练必须恢复最后持久化事件。
- 验收的标准：
  - 新用户可从空主页进入创建画像；已有会话可继续或查看报告。
  - 四态测试：主流程=继续/新建/查看报告；加载中=列表加载；空数据=`还没有训练记录` + `[n]`；报错=SQLite 查询失败带原因与恢复动作。
  - 键盘帮助、焦点、三种支持尺寸、ASCII/减弱动效测试通过。
  - 主链路回归：`init → run → 训练主页`。
- 完成后提交：`feat(training): T-006 add training home and navigation`

### [x] T-007 LLM Provider 适配器与 P-07 设置页

- 修改目标：实现 OpenAI-compatible/Ollama 适配器、结构化输出校验、连接诊断和设置页。
- 允许修改的范围：`internal/adapters/llm/`、`internal/tui/screens/settings/`、配置与测试替身、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得把密钥写入日志/导出/界面；Provider 失败不得丢草稿或历史；无可用 Provider 时不得开始新场景。
- 验收的标准：
  - Provider 请求、超时、取消、一次 Schema 重试、Ollama/OpenAI-compatible 连接测试通过。
  - 四态测试：主流程=连接成功；加载中=连接测试中；空数据=无 Provider 引导；报错=endpoint/认证/模型错误分别可诊断。
  - 设置页显示 DB/LLM/Runner/数据目录的文字状态和修复动作。
  - 主链路回归：无 Provider 时历史可浏览、设置可打开、Lite 不崩溃。
- 完成后提交：`feat(provider): T-007 add LLM adapters and settings`

### [x] T-008 简历解析适配器与画像服务

- 修改目标：解析 PDF/DOCX/TXT/粘贴文本，形成可追溯、可编辑的 CandidateProfile。
- 允许修改的范围：`internal/adapters/resume/`、`internal/core/profile/`、字段锁定/原文恢复所必需的 `internal/db/` Profile 元数据迁移与查询、测试样例、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得虚构履历；未确认推断不得进入既定事实；取消/失败不得留下半成品画像；文件不得超过 10MB。
- 验收的标准：
  - 支持四种输入路径，事实带 `source_span`，推断带置信度与待确认标志。
  - 保存、编辑、锁定、删除、重启恢复及事务级完整删除通过。
  - 四态测试：主流程=解析保存；加载中=可取消进度；空数据=无简历禁止继续；报错=路径无效/格式失败时保留文件名与粘贴回退。
  - 主链路回归：解析失败仍可通过粘贴文本继续。
- 完成后提交：`feat(profile): T-008 implement resume parsing and profile service`

### [x] T-009 P-02 简历与目标岗位工作台

- 修改目标：实现 Profile 输入、目标岗位/JD、事实/推断确认和行内编辑界面。
- 允许修改的范围：`internal/tui/screens/profile/`、必要的 Profile 查询/命令层、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：事实必须显示 `✓ confirmed`，推断必须显示 `? verify`；切换焦点/resize 不得丢输入；无简历文本不能继续。
- 验收的标准：
  - 文件路径、粘贴、角色、级别、JD、语言与画像编辑均有键盘路径。
  - 四态测试：主流程=解析确认保存；加载中=解析进度与取消；空数据=`还没有加载简历`；报错=路径/解析/保存错误有具体恢复动作。
  - 160×48、120×36、80×24、ASCII、CJK/长路径快照通过。
  - 主链路回归：`训练主页 → Profile → 保存 → 重启恢复`。
- 完成后提交：`feat(profile-ui): T-009 add profile workbench`

### [x] T-010 场景模板、JD 映射与 Scenario Planner

- 修改目标：实现 6 类模板、JD 映射、可编辑计划、Coach 规则锁定和版本化。
- 允许修改的范围：`content/scenarios/`、`internal/core/scenario/`、Provider 提示/Schema、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：只能使用已确认履历；无证据深挖必须标记通用或改成澄清题；用户手改计划不得被刷新覆盖。
- 验收的标准：
  - 每题含题目、意图、时长、rubric、证据锚点、追问上限、结束条件。
  - 有 JD 时至少产生 3 条映射；无 JD 可跳过。
  - 四态测试：主流程=生成/编辑/锁定；加载中=生成中且 Start 禁用、Back 可用；空数据=无计划可生成；报错=Provider/Schema 失败可重试。
  - 主链路回归：确认画像可生成并持久化 Scenario 版本。
- 完成后提交：`feat(scenario): T-010 implement scenario planner`

### [x] T-011 P-03 场景工厂

- 修改目标：实现模板、模式、时长、Run Plan 编辑和开始会话界面。
- 允许修改的范围：`internal/tui/screens/scenario/`、必要的场景命令层、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：场景摘要滚动时保持可见；每题不得是无意图/无证据标识的黑箱输出；开始后策略与版本不可变。
- 验收的标准：
  - 可删除/替换题目、修改难度/时长/模式，并保留手动编辑。
  - 四态测试：主流程=生成编辑开始；加载中=ActivityLine；空数据=`还没有场景计划`；报错=模型不可用/输出非法。
  - 键盘帮助、焦点和三种尺寸快照通过。
  - 主链路回归：`Profile → Scenario → 创建 Session`。
- 完成后提交：`feat(scenario-ui): T-011 add scenario factory`

### [x] T-012 文字面试状态机与持久化

- 修改目标：实现提问、提交、追问、收束、下一题、暂停、结束与恢复的事件状态机。
- 允许修改的范围：`internal/core/interview/`、会话存储查询、Provider 契约、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：追问不得超过 `max_follow_ups`；证据来源仅限已确认履历/已提交回答/已运行代码/题目约束；结束必须二次确认；重试不得重复写回答。
- 验收的标准：
  - 状态迁移、幂等提交、事件时间序、草稿本地保存、重启恢复测试通过。
  - 四态测试：主流程=完整多题会话；加载中=面试官思考/取消；空数据=无可用题目；报错=Provider 不可用/非法输出可重试或结束本题。
  - P95 延迟测量接口具备，提交原文在调用 Provider 前可靠持久化。
  - 主链路回归：Scenario 创建的 Session 可完成并进入待评估状态。
- 完成后提交：`feat(interview): T-012 implement interview state machine`

### [x] T-013 P-04 文字面试室与 Answer Trace

- 修改目标：实现单题主对话、计时器、草稿编辑、Trace 与结束确认界面。
- 允许修改的范围：`internal/tui/screens/interview/`、`AnswerTrace/QuestionCard/Timer` 组件、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：一次只显示一个当前问题；历史不可编辑；提交用 `Ctrl+Enter`；`Esc/q` 不得直接丢失会话；主问题、输入区和当前操作必须同时可见。
- 验收的标准：
  - 提交、追问、下一题、暂停/恢复、结束本题/会话、重启恢复均可键盘完成。
  - 四态测试：主流程=完整文字题；加载中=`interviewer: ▌`；空数据=无题目引导；报错=模型失败保留已提交答案并给 `[t]/[x]`。
  - 计时器正常/警告/暂停/结束状态带文字，Trace 顺序与事件一致。
  - 主链路回归：`主页 → Profile → Scenario → 完成文字会话`。
- 完成后提交：`feat(interview-ui): T-013 add interview room and trace`

### [x] T-014 Coach Policy、上下文隔离与学习事件

- 修改目标：实现 6 类 Coach 意图、L1-L4 边界、额度、暂停规则、理解状态和删除策略。
- 允许修改的范围：`internal/core/coach/`、Coach Provider 契约、SidebarEvent 存储、政策/对抗测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：Coach 不读未提交草稿/代码；Interviewer 不读 Coach 正文；严格模式不得输出完整解答；普通打开 Coach 不暂停计时。
- 验收的标准：
  - Strict/Standard/Coach 的次数和最高帮助级别严格执行，L4 仅在允许时出现。
  - 删除单条/单题/整场事件后不进入报告或推荐。
  - 四态测试：主流程=提问/标记理解；加载中=Coach thinking 且主输入可用；空数据=Coach ready；报错=额度耗尽/Provider 失败有独立作答路径。
  - 50 个严格模式诱导请求测试中 100% 不给完整可提交答案。
  - 主链路回归：Coach 使用不打断文字面试，显式暂停才冻结计时。
- 完成后提交：`feat(coach): T-014 enforce Coach policy and isolation`

### [ ] T-015 P-04 Coach Sidebar 与窄屏 overlay

- 修改目标：实现 CoachPane、HintMeter、快捷提问、自由输入、理解标记和响应式 overlay。
- 允许修改的范围：`internal/tui/screens/interview/coach*`、`CoachPane/HintMeter` 组件、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：Coach 视觉上必须次于主面试；不得在主 transcript 中渲染 Coach 正文；overlay 返回必须恢复精确光标和草稿。
- 验收的标准：
  - ≥160 列三栏；110–159 列 Main+Coach；80–109 列 Coach overlay；Trace 按规范折叠。
  - 四态测试：主流程=快捷/自由提问与返回；加载中=Coach ActivityLine；空数据=3 个高价值快捷入口；报错=额度/Provider 错误和恢复动作。
  - 默认计时继续，显式“暂停并求教”才暂停并记录原因。
  - 主链路回归：打开/关闭 Coach、resize、重试均不丢主回答。
- 完成后提交：`feat(coach-ui): T-015 add responsive Coach pane`

### [ ] T-016 证据化评估器与报告服务

- 修改目标：生成逐题复盘、固定维度评分、学习地图、迁移证据和下一轮计划。
- 允许修改的范围：`internal/core/evaluation/`、`internal/core/report/`、Provider Schema、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：无 evidence 的结论必须为“不足以判断”；无代码题时代码质量为“不适用”；不得做人格/录用判断；Coach 删除事件不得参与。
- 验收的标准：
  - 每项 finding 的 evidence 链接完整且可解析，学习地图与 SidebarEvent 数量一致。
  - 迁移判断仅使用提示后 5 分钟内的后续事件，没有足够证据不推断。
  - 四态测试：主流程=生成完整报告；加载中=分阶段状态；空数据=无完成会话；报错=非法模型输出/缺证据降级。
  - 下一轮计划至少 3 项，每项含主题、模式、时长和完成标准。
  - 主链路回归：完成会话可进入评估并得到可持久化报告。
- 完成后提交：`feat(report): T-016 add evidence-based evaluation`

### [ ] T-017 P-06 报告、证据跳转与 Practice Queue

- 修改目标：实现报告页、EvidenceLink、LearningGapRow、下一轮训练入口和报告删除。
- 允许修改的范围：`internal/tui/screens/report/`、报告专用组件、训练队列查询、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得把单一总分作为主视觉；每项评分必须可回到证据或显示 unavailable；改进项每组最多 3 条；删除需二次确认。
- 验收的标准：
  - 会话事实、Keep/Improve/Practice next、学习地图和下一轮计划均可键盘浏览。
  - 四态测试：主流程=浏览/跳证据/创建下轮；加载中=阶段生成；空数据=`还没有可用报告`；报错=报告生成/读取失败。
  - 删除后报告、学习缺口、派生队列消失。
  - 主链路回归：`完成会话 → 报告 → 一键创建下一场景`。
- 完成后提交：`feat(report-ui): T-017 add report and practice loop`

### [ ] T-018 数据导出、导入与删除命令

- 修改目标：实现 Markdown/JSON 报告导出、实例迁移包和数据删除命令。
- 允许修改的范围：`cmd/interviewcraft/` 的 `export/import`、`internal/core/transfer/`、设置页 Data 区、对应测试与文档、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得导出密钥；Coach 原文必须可选；导入不得破坏现有数据；删除必须完整且可报告失败。
- 验收的标准：
  - 在另一空 Lite 实例恢复画像、场景、会话和报告，ID/证据关系有效。
  - 四态测试：主流程=导出导入；加载中=确定性进度；空数据=无可导出内容；报错=损坏包/版本不兼容/不可写路径。
  - 删除单场与全部数据均二次确认，事务失败不留下部分删除。
  - 主链路回归：迁移后可查看旧报告并创建下一轮。
- 完成后提交：`feat(transfer): T-018 add export import and deletion`

### [ ] T-019 代码题领域、三语言草稿与禁用降级

- 修改目标：定义代码题、Python/JavaScript/Java 模板、代码草稿、运行结果事件，并实现 Runner disabled 路径。
- 允许修改的范围：`content/coding/`、`internal/core/coding/`、代码存储、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：无 Docker 时不得影响文字/Coach；未运行/未提交代码不得提供给 Coach；隐藏测试内容不得进入领域输出。
- 验收的标准：
  - 题目含输入输出、约束、至少 2 个示例、复杂度与 rubric。
  - 三语言编辑/格式化接口、模板重置、草稿恢复和快照事件测试通过。
  - 四态测试：主流程=编辑保存；加载中=草稿保存状态；空数据=未运行；报错=Runner disabled 给启用说明。
  - 主链路回归：Lite 无 Docker 可完成纯文字会话和报告。
- 完成后提交：`feat(coding): T-019 add coding domain and Lite fallback`

### [ ] T-020 Docker Runner 隔离执行

- 修改目标：实现可选 Docker Runner、资源限制、公开/隐藏测试协议和安全诊断。
- 允许修改的范围：`internal/adapters/runner/`、`docker/runner/`、`scripts/` 的隔离测试、相关文档与测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：Runner 默认关闭；容器必须禁网、非 root、只读基础镜像、受 CPU/内存/进程/时间限制且每次销毁；不得泄露宿主/容器路径、密钥或隐藏输入。
- 验收的标准：
  - Python/JavaScript/Java 公开测试执行和受控诊断通过。
  - 四态测试：主流程=通过/失败测试；加载中=耗时状态且编辑器可写；空数据=未运行；报错=超时/OOM/网络请求/Runner 不健康。
  - 死循环、网络、内存、进程炸弹和容器销毁隔离测试 100% 通过。
  - 主链路回归：Runner 停止/损坏时 Lite 文字链路仍正常。
- 完成后提交：`feat(runner): T-020 add isolated Docker execution`

### [ ] T-021 P-05 代码面试工作台

- 修改目标：实现题目规格、CodeEditor、RunSummary、Coach 错误解释入口和响应式代码界面。
- 允许修改的范围：`internal/tui/screens/coding/`、`CodeEditor/RunSummary` 组件、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：规格与编辑器必须分区；运行后 RunSummary 始终可见；错误不得泄露敏感路径/隐藏数据；严格模式不得生成完整实现。
- 验收的标准：
  - 三语言切换、草稿恢复、重置、运行、错误解释与返回面试均可键盘完成。
  - 四态测试：主流程=编辑运行提交；加载中=运行耗时且禁重复 Run；空数据=公开测试未运行；报错=disabled/failed/timeout/OOM。
  - 三种终端尺寸、ASCII、长错误、CJK 题目快照通过。
  - 主链路回归：包含代码题的完整会话可生成带代码证据的报告。
- 完成后提交：`feat(coding-ui): T-021 add code interview workbench`

### [ ] T-022 全链路质量门、文档与发布验收

- 修改目标：完成 E2E、安全、可访问性、AI 质量、性能、构建发布与使用文档。
- 允许修改的范围：全仓测试/修复所直接涉及的现有模块、`scripts/`、`README.md`、新增 ADR/部署/安全/贡献文档、发布配置、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得降低前述验收门槛来换取测试通过；不得引入 MVP Non-Goals；不得让 Docker 成为 Lite 必需项。
- 验收的标准：
  - E2E 跑通：`init → doctor → 简历 → 画像 → 场景 → 文字面试 → Coach → 可选代码 → 报告 → 下一轮 → export/import`。
  - 每个 P-01～P-07 的主流程、加载中、空数据、接口/依赖报错都有自动化覆盖。
  - 160×48、120×36、80×24、小终端阻塞、CJK、长路径、ASCII、减弱动效、全键盘路径全部通过。
  - Lite 无 Docker/无外部数据库 E2E 通过；Runner 隔离门禁通过；所有结论证据覆盖率 100%。
  - `go test ./...`、静态检查、构建、迁移、新安装烟测和 README 命令全部通过。
  - README 覆盖二进制安装、`init/run/doctor`、部署档位、环境变量、迁移、Runner 安全和贡献指南。
- 完成后提交：`chore(release): T-022 complete MVP quality gates`

## 3. 执行记录

> 每轮只新增一条记录。只有完成当前任务的全部验收后，才允许将对应任务勾选为 `[x]`。

| 轮次 | 任务 | 修改目标 | 允许范围 | 不允许破坏 | 验收与测试结果 | Git 提交 |
|---|---|---|---|---|---|---|
| R-000 | 规划 | 从两份规范生成严格有序的开发清单 | 仅 `TODO.md` | 不改 `docs/`，不实现业务 | 已覆盖顺序、边界、四态、主链路回归和逐项提交规则 | 待仓库初始化 |
| R-001 | T-001 | 初始化 Git 与 Go CLI 单二进制骨架 | `.gitignore`、`go.mod`、`README.md`、`cmd/interviewcraft/`、`internal/cli/`、`TODO.md`、Git 元数据 | 不改 `docs/`；不实现数据库、TUI、Provider、Runner；不引入 Node/Docker/常驻服务 | `gofmt -l` 无输出；`go vet ./...`、`go test ./...`、单二进制构建通过；主流程 help=0；加载中=N/A（无异步）；空目录/无配置 help=0；未知命令=2；占位命令=1；无 Docker 主链路通过 | `chore(cli): T-001 bootstrap Go command` |
| R-002 | T-002 | 建立领域契约、异步状态与类型化错误 | `internal/core/`、对应测试、`TODO.md` | 不混淆事实/推断；不放宽必填来源、置信度和证据字段；不实现 Provider/DB/TUI/Runner；不改 `docs/` | 五类 JSON Schema 与严格 Go 校验通过；主流程=合法契约；加载中=Pending→Streaming→Succeeded/Failed；空数据=缺失 facts 等必填字段被拒绝；报错=未知字段、非法枚举、空 evidence、无效状态被拒绝；Schema 失败仅重试 1 次并返回类型化 fallback；`gofmt -l`、`go vet ./...`、`go test -count=1 -cover ./...`、构建及 T-001 CLI 回归通过 | `feat(core): T-002 add domain contracts and async states` |
| R-003 | T-003 | 建立 SQLite、本地目录、迁移与训练数据持久化 | `internal/db/`、`go.mod/go.sum`、`TODO.md` | 事件只追加；删除事务化；不吞写入错误；不引入 CGO/Docker/外部数据库；不改 `docs/` | 固定并校验 `modernc.org/sqlite v1.55.0`；主流程=首次/重复迁移、完整训练图 CRUD、重启恢复、事件时间序和级联删除通过；加载中=Pending→Streaming→Succeeded/Failed；空数据=新库各查询返回显式空值；报错=无效路径、损坏/改名迁移、重复事件、级联失败回滚和无效 evidence 均返回类型化错误；`gofmt -l`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 ./...`、单二进制构建及 CLI 回归通过；DB 覆盖率 71.5% | `feat(storage): T-003 add SQLite migrations and local data` |
| R-004 | T-004 | 实现 Lite 初始化、运行时配置和健康诊断 | `internal/cli/`、`internal/config/`、`internal/doctor/`、相关测试、`README.md`、`TODO.md` | 保持 SQLite、本地目录、Runner 默认禁用；Runner 失败不阻塞 Lite；不输出密钥；不实现 TUI/Provider/Runner 业务 | 主流程=`init → doctor` 在无 Docker 下通过且 init 幂等；加载中=Pending→5×Streaming→Succeeded/Failed；为空=无配置时返回非零并提示 init，缺失数据目录不被 doctor 隐式创建；报错=模型不可用/终端过小/SQLite 不可用阻塞，Docker 不可用仅告警；`gofmt -l`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建和 help 回归通过；config/doctor/cli 覆盖率 68.9%/76.3%/72.7% | `feat(runtime): T-004 implement init and doctor` |
| R-005 | T-005 | 建立语义主题、基础组件、焦点模型与响应式 AppShell | `internal/tui/theme/`、`internal/tui/components/`、`internal/tui/layout/`、组件/快照测试、`TODO.md` | 不使用功能局部原始颜色；Pane 深度不超过两层；不依赖颜色或鼠标；不假设 Unicode 固定宽度；不提前实现业务 Screen | 11 个 DESIGN 核心组件齐备；`auto/dark/light`、true-color/ANSI-16/no-color、ASCII、reduce-motion 可切换；主流程=键盘焦点循环、overlay 精确恢复草稿焦点；加载中=Progress 与 Pending/Streaming/Succeeded/Failed Activity；为空=SelectableList/TextComposer 提供行动；报错=类型化安全信息与尺寸阻塞态；160×48、120×36、80×24、72×22 快照及 CJK、长路径、长模型名、ASCII overlay 通过；`gofmt -l`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建和 CLI/存储回归通过；components/layout/theme 覆盖率 80.8%/68.2%/70.1% | `feat(tui): T-005 build design-system primitives` |
| R-006 | T-006 | 实现 P-01 训练主页、全局导航、最近训练与 Practice Queue | `internal/tui/screens/training/`、必要查询层、`internal/cli/` 的 `run` 最小启动接线、对应测试、`TODO.md` | 单屏一个主行动；分数必须带维度；继续训练恢复最后持久化事件；不改 `docs/`；不提前实现 Profile/Report/Provider 业务 | 主流程=继续精确恢复最后持久化事件与独立草稿、新建进入画像、最近报告与 Practice Queue 跳转；加载中=Pending/Streaming 且全局导航可用；为空=`还没有训练记录` + `[n]`；报错=SQLite 原因、恢复动作与 `[t]`；160×48、120×36、80×24、ASCII、reduce-motion、CJK/长文本、快捷键/焦点/resize 快照通过；`gofmt -l` 无输出、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建及真实 `init → run` 烟测通过；training/db 覆盖率 75.2%/71.8% | `feat(training): T-006 add training home and navigation` |
| R-007 | T-007 | 实现 OpenAI-compatible/Ollama 适配器、结构化输出与设置页 | `internal/adapters/llm/`、`internal/tui/screens/settings/`、必要配置与测试替身、对应测试、`TODO.md` | 密钥不写日志/导出/界面；Provider 失败不丢历史或草稿；无 Provider 禁止新场景但历史与设置仍可用；Lite 不依赖 Docker | 主流程=OpenAI-compatible `/models`+`/chat/completions` 与 Ollama `/api/tags`+`/api/chat` 请求/响应、JSON Schema 校验和仅一次修复重试通过；加载中=连接 Pending/Activity 且设置可浏览；为空=无 Provider 显示配置入口、历史可用且新场景禁用；报错=endpoint/HTTP、本地或远端认证、模型缺失分别诊断，超时与取消类型化；DB/LLM/Runner/数据目录均显示文字状态与恢复动作；密钥值/引用不渲染，endpoint 禁止凭据/query/fragment；160×48、120×36、80×24、ASCII、reduce-motion、焦点/resize 快照通过；`gofmt -l` 无输出、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建及无 Provider `init → run` 回归通过；llm/settings/config 覆盖率 77.0%/77.0%/70.4% | `feat(provider): T-007 add LLM adapters and settings` |
| R-008 | T-008 | 实现四类简历输入、可追溯画像服务与完整持久化 | `internal/adapters/resume/`、`internal/core/profile/`、必要的 `internal/db/` Profile 元数据迁移/查询、测试样例、对应测试、`TODO.md` | 不虚构履历；未确认推断不进入事实；失败/取消不保存半成品；文件不超过 10MB；不改 `docs/`；不提前实现 P-02 UI | PDF/DOCX/TXT/粘贴提取与 10MB 限制通过；主流程=粘贴→严格 Schema→byte span 校验→SQLite 保存→重启恢复；加载中=Pending/Streaming 且进度中取消后无半成品；空数据=空简历禁止继续；报错=无效路径/格式保留来源并提供 `[p]` 粘贴回退；编辑、锁定、删除、保存失败回滚、元数据事务回滚及级联完整删除通过；`gofmt -l` 无输出、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；resume/profile/db 覆盖率 67.6%/76.8%/72.4% | `feat(profile): T-008 implement resume parsing and profile service` |
| R-009 | T-009 | 实现 P-02 简历与目标岗位工作台 | `internal/tui/screens/profile/`、必要的 Profile 查询/命令层、对应测试、`TODO.md` | 事实/推断标识不可混淆；焦点/resize 不丢表单；无简历不能继续；不改 `docs/`；不提前实现 T-010 | 文件/粘贴、角色、级别、JD、语言和画像列表均有键盘路径；事实显示 `✓ confirmed`（ASCII 为 `ok confirmed`），推断显示 `? verify`；编辑会使画像回到待确认，锁定/删除与确认保存通过；主流程=`训练主页 → Profile → 解析确认保存 → SQLite 重启恢复`；加载中=分阶段进度且 `[c]` 可取消；空数据=`还没有加载简历` 且禁止继续；报错=路径/格式保留输入并支持 `[p]` 粘贴回退，保存失败保留表单与画像；焦点/resize/行内草稿保持，160×48、120×36、80×24、ASCII、CJK/长路径快照通过；`gofmt -l` 无输出、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；profile screen/core profile 覆盖率 76.0%/77.7% | `feat(profile-ui): T-009 add profile workbench` |
| R-010 | T-010 | 实现场景模板、JD 映射与 Scenario Planner | `content/scenarios/`、`internal/core/scenario/`、Provider 提示/Schema、对应测试、`TODO.md` | 只使用已确认履历；无证据问题必须通用化；刷新不覆盖手改；确认后规则不可变；不改 `docs/`；不提前实现 P-03 UI | 6 类内嵌模板及完整题目字段通过；生成输入只含已确认事实/项目/技能，不含推断，未知证据、通用题伪证据和非法 JD 映射均被拒绝；有 JD 至少 3 条映射、无 JD 显式空映射；主流程=生成→编辑→刷新保留手改→确认锁定→SQLite 重启恢复版本；加载中=Start 禁用且 Back 可用；空数据=无计划时可从已确认画像生成；报错=Provider/Schema 失败仅重试一次并保留旧计划；`gofmt -l` 无输出、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；scenario/llm 覆盖率 73.6%/77.5% | `feat(scenario): T-010 implement scenario planner` |
| R-011 | T-011 | 实现模板、难度、模式、时长、Run Plan 编辑和开始会话界面 | `internal/tui/screens/scenario/`、必要的场景命令层、对应测试、`TODO.md` | 摘要滚动时保持可见；题目必须展示意图与证据/通用标识；手动编辑不得被刷新覆盖；开始后策略与版本不可变；不改 `docs/`；不提前实现 T-012 | 六类模板、难度/模式/时长、题目替换/删除、滚动常驻摘要、意图与 evidence/generic 标识及确认锁定通过；主流程=生成→手工编辑→刷新保留→确认版本→SQLite 创建 active Session；加载中=ActivityLine 且 Back 可用、Start 禁用；空数据=`还没有场景计划` + `[g]` 且禁止删除/开始；报错=Provider 不可用/非法 Schema 保留现有计划并显示 `[g]` 恢复动作；键盘帮助/焦点精确恢复、160×48/120×36/80×24 与 ASCII/CJK 快照通过；`Profile → Scenario → 创建 Session → 重启恢复` 回归通过；`gofmt -l` 无输出、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；scenario screen 覆盖率 77.2% | `feat(scenario-ui): T-011 add scenario factory` |
| R-012 | T-012 | 实现文字面试状态机、事件持久化、幂等提交与恢复 | `internal/core/interview/`、必要的会话存储查询、Interviewer Provider 契约、对应测试、`TODO.md` | 追问不超过上限；证据仅来自已确认履历/已提交回答/已运行代码/题目约束；答案先持久化再调用 Provider；重试不重复写回答；结束二次确认；事件只追加；不提前实现 P-04 UI | 状态迁移、暂停/恢复、结束二次确认、事件时间序、草稿保存与 SQLite 重启恢复通过；主流程=三题会话含追问/收束/下一题并进入 `evaluation_pending`；加载中=`Pending → Streaming → Succeeded/Failed`，取消等待返回类型化错误且答案不丢；空数据=无题目返回可行动错误；报错=Provider 不可用、非法 Schema/越权证据/超限追问可重试或安全结束本题；答案先写事件再调用 Provider，同一提交 ID 重试不重复写回答/动作，Interviewer 上下文不含草稿、推断或 Coach/Sidebar 正文；P95 接口与测试通过；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；interview/llm/db 覆盖率 78.8%/77.8%/72.3% | `feat(interview): T-012 implement interview state machine` |
| R-013 | T-013 | 实现 P-04 文字面试室、Answer Trace、计时与草稿交互 | `internal/tui/screens/interview/`、`AnswerTrace/QuestionCard/Timer` 组件、对应测试、`TODO.md` | 一次只显示当前题；历史只读；`Ctrl+Enter` 提交；`Esc/q` 不直接丢会话；主问题、输入区、当前操作同时可见；不实现 T-014 Coach 业务 | QuestionCard、Timer、不可变 Answer Trace 与响应式 Interview Room 通过；主流程=`Scenario 创建的 SQLite Session → 两题键盘作答 → 暂停/恢复 → evaluation_pending`；加载中=`interviewer: ▌`、输入禁用且 `[Esc]` 取消等待，已落库回答与草稿保留并可跨模型恢复后用同一 ID 重试；空数据=无题目显示原因、恢复动作和 `[h]` 返回路径；报错=Provider 不可用或非法动作保留原文并给 `[t]` 重试/`[x]` 安全结束；`Enter` 不提交、`Ctrl+Enter` 提交，`Esc/q` 不直接离开，结束本题/会话均持久化二次确认；Trace 按事件时间序只读浏览，Timer 正常/警告/暂停/结束均有文字；草稿、帮助焦点、resize 与 SQLite 重启恢复通过；160×48、120×36、80×24、ASCII、CJK 快照和几何校验通过；定向 `-race` 通过；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；interview screen/components 覆盖率 80.5%/81.6% | `feat(interview-ui): T-013 add interview room and trace` |
| R-014 | T-014 | 实现 Coach Policy、上下文隔离、学习事件与删除策略 | `internal/core/coach/`、Coach Provider 契约、SidebarEvent 存储、政策/对抗测试、`TODO.md` | Coach 不读未提交草稿/代码；Interviewer 不读 Coach 正文；Strict 不输出完整解答；普通求教不暂停；删除事件不进入报告/推荐；不提前实现 T-015 UI | Strict/Standard/Coach 的 1/2/无限额度与 L1–L4 上限、6 类意图、理解/困惑/复习标记、单条/单题/整场物理删除及不可绕过额度账本通过；主流程=隔离后的已确认事实/已提交回答/已运行代码生成并持久化 CoachResponse；加载中=`Pending → coach: thinking → Succeeded/Failed` 且普通请求不写暂停事件；空数据=非 nil 空历史（Coach ready）；报错=额度耗尽、Provider 不可用/取消、非法 Schema/越权输出均返回独立作答恢复路径；显式暂停仅写 `pause_reason=coach_help`，Interviewer 上下文继续排除 Sidebar 正文；50 条 Strict 诱导请求 100% 拦截完整答案/可提交代码，策略说明与标签旁路亦被拒绝；SQLite v3 迁移、重启恢复、删除后报告/推荐查询排除及 Profile 级联通过；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、定向 `-race`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；coach/llm/db 覆盖率 77.8%/77.5%/72.0% | `feat(coach): T-014 enforce Coach policy and isolation` |
