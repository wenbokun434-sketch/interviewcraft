# InterviewCraft MVP 与一键部署开发清单

> 规范基线：`docs/InterviewCraft_Agent_PRD_MVP_v2.1_TUI.md`（v2.1）与 `docs/DESIGN.md`（v1.0）
> 实施策略：Go 单二进制 + TUI + SQLite；Lite 模式默认不依赖 Docker、Node.js、外部数据库、队列或常驻服务；T-023～T-029 在此基础上完成可信的一键安装、配置、运行、升级与回滚。

## 0. 强制执行协议

1. 必须严格按 T-001 → T-029 的顺序开发；前一项未通过，不得开始后一项。
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
7. 每项任务必须依次完成：实现 → 格式化/静态检查 → 单元测试 → 模块测试 → 主链路回归 → 更新本文件（只勾当前项并写测试记录）→ 立即 Git 提交；测试已经跑通时不得继续夹带下一模块修改。
8. 测试未通过时不得勾选、不得提交“完成”提交；修复必须留在当前任务范围内。
9. 每项任务使用独立提交，提交信息格式：`type(scope): T-xxx concise summary`。
10. 提交前必须依次运行 `git status --short`、仅显式暂存当前任务允许范围内的文件、`git diff --cached --check`、`git diff --cached --name-only`；确认暂存区不含其他任务、用户已有修改、本地数据、密钥、数据库、日志或构建产物后，立即执行当前任务规定的 `git commit`，提交完成后再次检查 `git status --short`。
11. 禁止使用 `git add -A`、`git add .` 或其他会无差别暂存工作区内容的命令；禁止为了得到干净状态而删除、覆盖或重置用户已有文件。默认只要求本地提交，不自动推送远端。
12. `docs/` 是需求基线。除非用户明确要求修订规范，否则开发任务不得修改其中内容。

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

### [x] T-015 P-04 Coach Sidebar 与窄屏 overlay

- 修改目标：实现 CoachPane、HintMeter、快捷提问、自由输入、理解标记和响应式 overlay。
- 允许修改的范围：`internal/tui/screens/interview/coach*`、`CoachPane/HintMeter` 组件、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：Coach 视觉上必须次于主面试；不得在主 transcript 中渲染 Coach 正文；overlay 返回必须恢复精确光标和草稿。
- 验收的标准：
  - ≥160 列三栏；110–159 列 Main+Coach；80–109 列 Coach overlay；Trace 按规范折叠。
  - 四态测试：主流程=快捷/自由提问与返回；加载中=Coach ActivityLine；空数据=3 个高价值快捷入口；报错=额度/Provider 错误和恢复动作。
  - 默认计时继续，显式“暂停并求教”才暂停并记录原因。
  - 主链路回归：打开/关闭 Coach、resize、重试均不丢主回答。
- 完成后提交：`feat(coach-ui): T-015 add responsive Coach pane`

### [x] T-016 证据化评估器与报告服务

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

### [x] T-017 P-06 报告、证据跳转与 Practice Queue

- 修改目标：实现报告页、EvidenceLink、LearningGapRow、下一轮训练入口和报告删除。
- 允许修改的范围：`internal/tui/screens/report/`、报告专用组件、训练队列查询、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得把单一总分作为主视觉；每项评分必须可回到证据或显示 unavailable；改进项每组最多 3 条；删除需二次确认。
- 验收的标准：
  - 会话事实、Keep/Improve/Practice next、学习地图和下一轮计划均可键盘浏览。
  - 四态测试：主流程=浏览/跳证据/创建下轮；加载中=阶段生成；空数据=`还没有可用报告`；报错=报告生成/读取失败。
  - 删除后报告、学习缺口、派生队列消失。
  - 主链路回归：`完成会话 → 报告 → 一键创建下一场景`。
- 完成后提交：`feat(report-ui): T-017 add report and practice loop`

### [x] T-018 数据导出、导入与删除命令

- 修改目标：实现 Markdown/JSON 报告导出、实例迁移包和数据删除命令。
- 允许修改的范围：`cmd/interviewcraft/` 的 `export/import`、`internal/core/transfer/`、设置页 Data 区、对应测试与文档、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得导出密钥；Coach 原文必须可选；导入不得破坏现有数据；删除必须完整且可报告失败。
- 验收的标准：
  - 在另一空 Lite 实例恢复画像、场景、会话和报告，ID/证据关系有效。
  - 四态测试：主流程=导出导入；加载中=确定性进度；空数据=无可导出内容；报错=损坏包/版本不兼容/不可写路径。
  - 删除单场与全部数据均二次确认，事务失败不留下部分删除。
  - 主链路回归：迁移后可查看旧报告并创建下一轮。
- 完成后提交：`feat(transfer): T-018 add export import and deletion`

### [x] T-019 代码题领域、三语言草稿与禁用降级

- 修改目标：定义代码题、Python/JavaScript/Java 模板、代码草稿、运行结果事件，并实现 Runner disabled 路径。
- 允许修改的范围：`content/coding/`、`internal/core/coding/`、代码存储、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：无 Docker 时不得影响文字/Coach；未运行/未提交代码不得提供给 Coach；隐藏测试内容不得进入领域输出。
- 验收的标准：
  - 题目含输入输出、约束、至少 2 个示例、复杂度与 rubric。
  - 三语言编辑/格式化接口、模板重置、草稿恢复和快照事件测试通过。
  - 四态测试：主流程=编辑保存；加载中=草稿保存状态；空数据=未运行；报错=Runner disabled 给启用说明。
  - 主链路回归：Lite 无 Docker 可完成纯文字会话和报告。
- 完成后提交：`feat(coding): T-019 add coding domain and Lite fallback`

### [x] T-020 Docker Runner 隔离执行

- 修改目标：实现可选 Docker Runner、资源限制、公开/隐藏测试协议和安全诊断。
- 允许修改的范围：`internal/adapters/runner/`、`docker/runner/`、`scripts/` 的隔离测试、相关文档与测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：Runner 默认关闭；容器必须禁网、非 root、只读基础镜像、受 CPU/内存/进程/时间限制且每次销毁；不得泄露宿主/容器路径、密钥或隐藏输入。
- 验收的标准：
  - Python/JavaScript/Java 公开测试执行和受控诊断通过。
  - 四态测试：主流程=通过/失败测试；加载中=耗时状态且编辑器可写；空数据=未运行；报错=超时/OOM/网络请求/Runner 不健康。
  - 死循环、网络、内存、进程炸弹和容器销毁隔离测试 100% 通过。
  - 主链路回归：Runner 停止/损坏时 Lite 文字链路仍正常。
- 完成后提交：`feat(runner): T-020 add isolated Docker execution`

### [x] T-021 P-05 代码面试工作台

- 修改目标：实现题目规格、CodeEditor、RunSummary、Coach 错误解释入口和响应式代码界面。
- 允许修改的范围：`internal/tui/screens/coding/`、`CodeEditor/RunSummary` 组件、对应测试、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：规格与编辑器必须分区；运行后 RunSummary 始终可见；错误不得泄露敏感路径/隐藏数据；严格模式不得生成完整实现。
- 验收的标准：
  - 三语言切换、草稿恢复、重置、运行、错误解释与返回面试均可键盘完成。
  - 四态测试：主流程=编辑运行提交；加载中=运行耗时且禁重复 Run；空数据=公开测试未运行；报错=disabled/failed/timeout/OOM。
  - 三种终端尺寸、ASCII、长错误、CJK 题目快照通过。
  - 主链路回归：包含代码题的完整会话可生成带代码证据的报告。
- 完成后提交：`feat(coding-ui): T-021 add code interview workbench`

### [x] T-022 全链路质量门、文档与发布验收

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

### [x] T-023 常驻 TUI 事件循环与完整发布入口

- 修改目标：将现有 P-01～P-07 屏幕模型接入一个常驻、可恢复终端状态的应用事件循环，使发布二进制执行 `interviewcraft run` 后能够完成整场训练，而不是只渲染一次主页后退出。
- 允许修改的范围：新增 `internal/tui/app/` 与必要的终端输入适配层、`internal/cli/` 中 `run` 的启动接线、各屏幕为统一导航协议所必需的最小接口、`cmd/interviewcraft/`、确有必要的纯 Go 终端依赖及 `go.mod/go.sum`、对应测试、`scripts/` 中与发布入口烟测直接相关的脚本、`README.md` 当前实现状态、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得改写既有领域服务、SQLite 事件顺序、证据链、Coach/Interviewer 上下文隔离或 Runner 安全边界；不得改变现有快捷键、响应式断点和主题语义；异常、取消和 `Ctrl+C` 必须恢复终端，不能丢草稿或留下数据库写事务；继续保持 CGO-free、单二进制和 Lite 无 Docker 可用；不得提前实现安装、更新或镜像分发。
- 验收的标准：
  - 主流程：从真实 `interviewcraft run` 进入训练主页，完成 `画像 → 场景 → 文字面试 → Coach → 可选代码 → 报告 → 下一轮`，退出并重启后可恢复最近持久化状态。
  - 加载中：Provider、Coach、评估和 Runner 异步操作均在事件循环中持续刷新，允许既定取消/返回操作，不冻结键盘、resize 或无关编辑区，也不重复提交事件。
  - 空数据：全新 SQLite 打开训练主页时只提供明确的首要动作，空列表、无 Provider、无题目和无报告均可导航且不会 panic 或自动制造业务数据。
  - 接口/依赖报错：Provider、SQLite、终端输入和 Runner 故障均显示类型化恢复动作；取消或报错后草稿与已提交证据保留，终端模式和光标状态恢复。
  - 160×48、120×36、80×24、72×22 阻塞、ASCII、no-color、reduce-motion、CJK、长错误和全键盘 E2E 通过；原 P-01～P-07 快照、领域测试、`test-fresh-install.ps1`、静态检查、两模块测试及单二进制构建全部通过。
- 完成后提交：`feat(tui-app): T-023 run the complete interactive journey`

### [x] T-024 幂等 `setup` 向导、部署档位与凭据安全

- 修改目标：新增交互式和非交互式 `interviewcraft setup`，用一个可恢复的编排入口完成 Lite、Private Local 和 Full Practice 的配置选择、数据初始化、凭据注入、健康检查与启动准备。
- 允许修改的范围：新增 `internal/setup/`、新增凭据存储抽象及 Windows/macOS/Linux 平台适配文件、`internal/cli/`、`internal/config/`、`internal/doctor/`、设置页与 setup 共用表单所必需的最小接口、对应测试和测试替身、`README.md` 的 setup 用法、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：`init`、`doctor` 和已有环境变量配置必须继续兼容；不得把 API Key 写入 `config.json`、命令行参数、日志、错误、导出包或测试快照；凭据库不可用时只能明确退回环境变量引用，不能静默明文落盘；重复 setup 不得覆盖用户数据或未授权更改 Provider；Runner 必须默认 disabled，不能静默安装 Docker、请求管理员权限或启用代码执行；不得提前实现发布下载、自动更新或远端镜像拉取。
- 验收的标准：
  - 主流程：全新目录分别完成 Lite/OpenAI-compatible、Private Local/Ollama 配置；Full Practice 通过可注入 Runner provisioner 完成编排；`setup → init → doctor → run` 可重复执行且配置与数据保持一致。
  - 加载中：setup 按 `preflight → profile → provider → credential → initialize → diagnose → complete` 发出确定性进度；取消后保留已确认输入和安全检查点，重试从最近安全步骤继续。
  - 空数据：没有配置、没有 Provider、凭据库为空或数据目录尚不存在时进入明确向导，不创建半配置、空密钥或不可用 Runner 状态；非交互模式缺必填项时返回 usage 错误和字段列表。
  - 接口/依赖报错：凭据库、Provider、Ollama、目录权限、SQLite 和诊断失败分别给出脱敏恢复动作；失败不覆盖旧配置、不删除旧凭据、不留下半迁移数据库。
  - setup 状态机、平台凭据契约、密钥泄漏扫描、配置幂等、取消恢复、原 `init/doctor/settings/export/import` 回归、静态检查、测试和单二进制构建全部通过。
- 完成后提交：`feat(setup): T-024 add idempotent secure setup`

### [x] T-025 可验证发布清单、版本元数据与供应链门禁

- 修改目标：让每个正式版本同时产出机器可读发布清单、版本信息、跨平台归档、校验和、签名、SBOM 和可验证构建来源，为安装器与更新器提供唯一可信输入。
- 允许修改的范围：新增 `internal/version/`、CLI 的 `version` 命令、`.goreleaser.yaml`、`.github/workflows/release.yml` 及必要的发布工作流、`scripts/` 下发布清单生成/校验脚本、发布相关测试和 fixture、`README.md` 的版本校验说明、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得跳过或弱化现有完整发布门禁；不得在质量门未通过时发布资产；不得把长期签名私钥提交仓库；不得允许 manifest、checksum、签名和实际归档版本不一致；不得改变 Lite/Runner 的运行时安全边界或把 Docker 加入普通构建依赖；不得提前实现本机安装和升级写操作。
- 验收的标准：
  - 主流程：`v*` 标签通过完整质量门后生成 Windows/Linux/macOS × amd64/arm64 归档、`checksums.txt`、签名、SBOM、来源证明和含版本/平台/URL/hash/size 的严格发布清单；`interviewcraft version` 与产物元数据一致。
  - 加载中：发布流水线按质量门、构建、清单、签名、验证、发布分阶段输出；发布前验证阶段能读取全部资产并显示确定性进度，未完成时不把 Release 标为可用。
  - 空数据：无发布标签、空 manifest、缺少当前平台资产或零字节资产均被显式拒绝，不生成“成功”发布或选择其他平台兜底。
  - 接口/依赖报错：构建、GitHub Release、签名、上传、下载回读、checksum 或 schema 校验任一失败都返回非零并阻止完成发布；日志不得包含令牌或凭据。
  - 本地 fixture 校验、篡改归档/清单/签名拒绝测试、YAML 语法、GoReleaser dry-run、现有发布质量门和六目标交叉编译全部通过。
- 完成后提交：`chore(distribution): T-025 publish verifiable release metadata`

### [x] T-026 Windows、Linux 与 macOS 一键安装/卸载

- 修改目标：提供不依赖 Go 的 `install.ps1`、`install.sh` 及对应卸载入口，自动识别平台、下载并验证发布产物、安装到当前用户目录、配置 PATH，并调用 `interviewcraft setup` 完成首次部署。
- 允许修改的范围：新增 `scripts/install.ps1`、`scripts/install.sh`、`scripts/uninstall.ps1`、`scripts/uninstall.sh` 及共用 fixture/测试、`.goreleaser.yaml` 的安装脚本归档、安装器所需的最小发布工作流调整、`.gitignore`、`README.md` 安装/卸载说明、`docs/DEPLOYMENT.md`、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：默认只能安装到当前用户可写目录，不得静默请求管理员/root 权限、执行未验证内容或修改系统范围配置；不得使用未签名/校验失败资产；重复安装同版本必须幂等，升级旧版本必须留给 T-028；卸载默认保留 `~/.interviewcraft`，不得删除用户数据、密钥或其他程序 PATH；不得把本地数据、安装缓存或密钥提交仓库。
- 验收的标准：
  - 主流程：Windows PowerShell 与 Linux/macOS shell 在无 Go 的干净环境中一条命令完成平台识别、下载、签名/checksum 验证、用户级安装、PATH 配置、`setup → doctor`，新终端可直接运行 `interviewcraft version` 和 `run`。
  - 加载中：下载、验证、解压、安装、PATH、setup 和 doctor 均显示明确阶段及可取消行为；取消后旧版本和 PATH 保持可用，临时文件可安全清理。
  - 空数据：首次安装、PATH 中不存在旧版本、没有配置/数据目录时完成新装；Release 无当前平台资产时给出支持矩阵，不创建空二进制或残缺安装目录。
  - 接口/依赖报错：网络/代理、Release API、签名、checksum、权限、磁盘空间、解压、PATH 和 setup 失败均非零退出并给恢复动作；失败不得覆盖可用旧二进制或删除用户数据。
  - 安装两次幂等、卸载后 PATH 无残留且数据保留、恶意 manifest/路径穿越/截断下载被拒绝；PowerShell AST、ShellCheck、干净 VM/容器 smoke、发布归档 fresh-install 和原质量门全部通过。
- 完成后提交：`feat(installer): T-026 add verified one-command installation`

### [x] T-027 预构建 Runner 镜像、完整诊断与 Full Practice 部署

- 修改目标：在发布流水线构建并发布与应用版本匹配的 amd64/arm64 Runner 镜像，使用不可变 digest 完成拉取、验证和启用，使 Full Practice 不再要求用户从源码构建镜像。
- 允许修改的范围：`docker/runner/`、`internal/adapters/runner/`、`internal/config/`、`internal/doctor/`、`internal/setup/` 的 Runner provisioner、`scripts/test-runner-isolation.ps1` 及新增镜像发布/验证脚本、`.github/workflows/`、`.goreleaser.yaml` 或发布清单中的 Runner 元数据、相关测试、`README.md`、`docs/DEPLOYMENT.md`、`docs/SECURITY.md`、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：Runner 仍默认 disabled；不得自动安装/启动 Docker 或请求特权；容器必须继续禁网、非 root、只读根、无宿主挂载、drop all capabilities、no-new-privileges、限 CPU/内存/PID/ulimit、每次强制清理且不泄露隐藏测试；只接受签名有效、版本兼容、标签/用户正确并固定 digest 的镜像；Runner/Registry 失败不得阻塞 Lite 文字主链路。
- 验收的标准：
  - 主流程：发布流水线产出 amd64/arm64 签名镜像和 digest，`setup --profile full` 在已有 Docker 环境中拉取并验证镜像，`doctor` 同时验证 daemon、镜像签名/标签/默认用户/协议版本，三语言练习通过。
  - 加载中：镜像解析、拉取、签名验证、inspect、隔离 smoke 和启用按阶段报告；拉取期间可取消，未全部通过前 `RUNNER_MODE` 保持 disabled。
  - 空数据：无 Docker、无本地镜像、manifest 无 Runner digest 或用户选择 Lite 时不创建容器、不改变模式，并提供明确安装/稍后启用路径。
  - 接口/依赖报错：Registry、签名、digest、架构、标签、用户、协议、Docker daemon 和隔离 smoke 任一失败都拒绝启用 Runner，清理临时镜像/容器并保持 Lite 可用。
  - 原 Runner 单元/集成/攻击测试全部通过；签名篡改、错误 digest、错误架构、无效标签和中断拉取测试通过；最终残留容器为 0，普通 Lite 质量门仍不依赖 Docker。
- 完成后提交：`feat(runner-deploy): T-027 publish and provision signed runner images`

### [ ] T-028 自动更新、完整备份、失败回滚与安全卸载

- 修改目标：新增 `update --check`、`update`、`rollback` 和安全卸载能力，在保持用户数据和可用旧版本的前提下完成可信升级、SQLite 迁移验证以及失败后的二进制与数据整体恢复。
- 允许修改的范围：新增 `internal/update/`、CLI 的 update/rollback/uninstall 接线、`internal/db/` 中备份/独占锁所需的最小接口、`internal/config/` 中安装状态元数据、Windows 自替换 helper、`scripts/install*`/`uninstall*` 的升级接线、发布清单客户端、对应测试和 fixture、`README.md`、`docs/DEPLOYMENT.md`、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得在写进程活动时直接复制数据库；不得覆盖唯一可用二进制或原地修改备份；不得在缺少有效签名/checksum、备份或磁盘空间时升级；数据库降级必须同时恢复升级前完整数据目录，不能只替换二进制；回滚/卸载默认不得删除用户数据和凭据；`--purge-data` 必须使用精确目标、显式二次确认且不能作用于宽泛目录。
- 验收的标准：
  - 主流程：检查新版本、下载验证、获取独占锁、创建可验证备份、原子切换二进制、运行迁移和 doctor、提交升级；随后可回滚到前一版本并恢复对应数据，最近会话和报告一致。
  - 加载中：check/download/verify/backup/switch/migrate/doctor/commit 各阶段可观测；安全阶段前可取消且不改现状，切换后取消或失败自动进入回滚，不能同时启动第二个更新。
  - 空数据：无更新、首次无备份、全新空数据库和无 Runner 环境均返回明确结果；无更新不是错误，不生成空备份或伪造可回滚版本。
  - 接口/依赖报错：Release API、网络、签名、checksum、锁、磁盘、备份、替换、迁移、doctor、Runner 更新任一失败都恢复旧二进制和匹配数据；错误脱敏且保留诊断材料路径。
  - Windows 正在使用二进制替换、Linux/macOS 原子 rename、迁移失败、断电点故障注入、并发更新、备份损坏、回滚与卸载保留数据测试通过；现有迁移、transfer、fresh-install 和完整质量门回归通过。
- 完成后提交：`feat(update): T-028 add verified upgrade and rollback`

### [ ] T-029 一键完整部署 E2E、故障矩阵与交付文档

- 修改目标：建立干净环境的一键完整部署验收矩阵，统一 Lite、Private Local、Full Practice 的安装、setup、运行、更新、回滚和卸载证据，并把最终用户路径固化到 CI 和交付文档。
- 允许修改的范围：全仓仅为修复部署验收缺陷所必需的代码、`internal/e2e/`、`scripts/` 下部署测试与 fixture、`.github/workflows/` 部署矩阵、`.goreleaser.yaml`、`README.md`、`docs/DEPLOYMENT.md`、`docs/SECURITY.md`、`docs/QUALITY_GATES.md`、`CONTRIBUTING.md`、`TODO.md` 当前任务状态与记录。
- 不允许破坏的逻辑：不得降低 T-001～T-028 的测试、隐私、证据、隔离、签名或回滚门禁来换取绿灯；不得把 Docker、Go、Node.js、Python、Java、外部数据库或管理员权限变成 Lite 前置；不得在文档中宣称未自动化验证的平台/模式已经通过；不得提交安装缓存、Release 凭据、本地数据、数据库、日志或构建产物。
- 验收的标准：
  - 主流程：Windows、Linux、macOS 干净环境完成“一条安装命令 → setup → doctor → 完整训练 → 重启恢复 → update → rollback → uninstall”；Full Practice 另在受支持 Docker runner 上完成签名镜像拉取与三语言练习。
  - 加载中：安装、setup、Provider、训练、Runner、更新和回滚的全部异步阶段有自动化断言，键盘/取消/重试可用，不出现冻结、重复事件、半配置或半升级。
  - 空数据：全新用户、无 Provider、无历史、无 Runner、无更新、无备份分别显示唯一明确下一步；Lite 在无 Docker/无外部数据库环境完整可用。
  - 接口/依赖报错：覆盖网络/Release API、签名/checksum、Provider、凭据库、目录/SQLite、终端、Docker/Registry/Runner、迁移和回滚失败；每项均验证脱敏提示、恢复动作、数据保留和再次执行成功。
  - 干净环境矩阵、四态故障矩阵、安装幂等、完整训练、升级回滚、卸载保留数据、Runner 零残留、供应链篡改拒绝、六目标构建、全部单元/模块/E2E/安全门禁通过；README 的复制粘贴命令由 CI 实际执行，执行记录写入平台、版本、耗时和提交 SHA。
- 完成后提交：`chore(deployment): T-029 certify one-click full deployment`

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
| R-015 | T-015 | 实现 P-04 Coach Sidebar、HintMeter 与窄屏 overlay | `internal/tui/screens/interview/coach*`、Interview Room 最小接线、`CoachPane/HintMeter` 组件、对应测试、`TODO.md` | Coach 视觉次于主面试；正文不进入主 transcript/Trace 摘要；overlay 返回恢复精确主焦点、rune 光标与草稿；不改变 T-014 Policy；不提前实现 T-016 | CoachPane、额度刻度/L1–L4、6 类快捷与自由输入、理解/困惑/复习标记和结束会话 L4 复盘通过；主流程=SQLite Session → 快捷提问 → 标记理解 → 自由输入 → 显式暂停并求教 → 返回主回答；加载中=独立 `coach: thinking` ActivityLine 且主回答可继续编辑；空数据=`COACH READY`、默认计时继续及 3 个高价值入口；报错=Provider 失败可用同一请求 ID 重试，额度耗尽显示独立作答恢复路径；普通提问不新增主事件，显式暂停仅写 `pause_reason=coach_help`；Answer Trace 只合并 Coach 时间/层级/标签，不渲染正文；≥160 为 Trace+Main+Coach，110–159 为 Main+Coach，80–109 为 Coach 全屏 overlay，关闭/resize/重试均保留主草稿、焦点和 rune 光标；160×48、120×36、80×24、ASCII/CJK 几何与内容快照通过；定向 `-race`、`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；components/interview screen 覆盖率 82.5%/83.0% | `feat(coach-ui): T-015 add responsive Coach pane` |
| R-016 | T-016 | 实现证据化评估器、逐题复盘、固定维度评分、学习地图、迁移证据与下一轮计划 | `internal/core/evaluation/`、`internal/core/report/`、评估所需 Provider Schema/测试替身、对应测试、`TODO.md` | 无证据不得断言；无代码题的代码质量必须为“不适用”；不得做人格式/录用判断；已删除 Coach 事件不得进入报告；不改 `docs/`；不提前实现 T-017 UI | 8 个固定维度、逐题复盘、可解析 Evidence 目录、Coach 学习地图、5 分钟迁移窗口、三源洞察与不少于 3 项下一轮计划通过；主流程=`evaluation_pending` SQLite 会话 → 严格 Evaluator Schema（非法输出仅重试 1 次）→ 证据校验 → 报告持久化 → `completed`，重复生成幂等恢复；加载中=`Pending → scoring_evidence → grouping_learning_gaps → planning_next_run → saving_report → Succeeded`；空数据=无完成会话/无已提交回答或代码时明确拒绝；报错=Provider/存储失败保留 pending 会话且不写半成品，非法模型输出、未知/缺失 evidence、人格/录用断言均局部降级为“不足以判断”；无代码时 `code_quality=not_applicable`；学习地图计数与未删除 SidebarEvent 完全一致，删除事件不进入 Provider/报告；迁移仅接受同题提示后 5 分钟内事件（含恰好 5 分钟边界），不推断成功/失败；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；evaluation/report/llm 覆盖率 77.5%/63.0%/75.4%；`-race`=N/A（Windows 环境缺少 CGO C 编译器，T-016 服务不持有共享可变状态） | `feat(report): T-016 add evidence-based evaluation` |
| R-017 | T-017 | 实现 P-06 报告页、证据跳转、学习缺口、下一轮训练入口与报告删除 | `internal/tui/screens/report/`、`EvidenceLink`/`LearningGapRow` 报告组件、Practice Queue 删除查询、对应测试、`TODO.md` | 不以单一总分作为主视觉；每项评分可跳到 evidence 或显示 `evidence unavailable`；Keep/Improve/Practice next 每组最多 3 条；删除必须 `[d] → [y]` 二次确认并消费一次性授权；不改 `docs/`；不提前实现 T-018 | 会话事实、8 维评分、逐题复盘、Keep/Improve/Practice next、学习地图、跨源洞察与训练计划的键盘浏览/证据跳转通过；主流程=`completed SQLite Session → 持久化报告 → [n] 携带主题/模式/时长/完成标准创建下一场景`；加载中=`Pending → scoring/planning Streaming → Succeeded` 且 reduce-motion 静态；空数据=`还没有可用报告` + `[t]`；报错=读取/生成/删除失败均类型化且不泄露底层错误，删除失败保留报告；缺证据显式 `evidence unavailable`；删除未确认不可调用、默认 Enter/Esc 取消，确认后报告/学习地图/派生 Practice Queue 同时消失；160×48、120×36、80×24、ASCII/CJK 几何通过；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；report screen/components/db 覆盖率 72.3%/82.4%/72.1%；`-race`=N/A（Windows 环境缺少 CGO C 编译器） | `feat(report-ui): T-017 add report and practice loop` |
| R-018 | T-018 | 实现 Markdown/JSON 报告导出、迁移包、导入恢复、数据删除命令与设置页 Data 区 | `cmd/interviewcraft/` 的 `export/import`、`internal/core/transfer/`、`internal/tui/screens/settings/` Data 区、对应测试与文档、`TODO.md` | 不导出密钥；Coach 原文仅显式选择时包含；导入不得覆盖现有数据；单场/全部删除须二次确认且事务失败完整回滚；不提前实现 T-019 | 固定版本迁移包、Markdown/严格 JSON 单报告导出、空 Lite 原事务恢复及已有数据拒绝通过；Provider 配置与密钥不进入迁移包且目标本地配置保留，Coach 正文默认清空、显式选择才包含；主流程=`画像/来源/场景/会话/事件/草稿/Coach/代码/报告 → 导出 → 空 Lite 导入 → 查看旧报告 → [n] 创建下一练`；加载中=导出 4 阶段、导入 6 阶段与 Data 区 Pending/Streaming 确定性状态；空数据=无可导出内容且 Data 区仅开放导入；报错=损坏包、未知字段、版本不兼容、ID/证据/外键关系、已有目标数据、覆盖与不可写路径均类型化拒绝；单场/全部删除须 `[d/x] → [y]` 一次性授权与精确确认短语，注入提交失败时导入/删除均完整回滚，全部删除后 Provider 配置保留；160×48、120×36、80×24、ASCII/CJK 几何通过；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；transfer/settings/cli 覆盖率 77.9%/75.3%/66.5%；`-race`=N/A（Windows 环境缺少 CGO C 编译器） | `feat(transfer): T-018 add export import and deletion` |
| R-019 | T-019 | 定义代码题、三语言模板、代码草稿、运行结果事件与 Runner disabled 降级 | `content/coding/`、`internal/core/coding/`、现有代码存储、对应测试、`TODO.md` | 无 Docker 不影响文字/Coach；未运行/未提交代码不进入 Coach；隐藏测试内容不进入领域输出；不提前实现 T-020/T-021 | 内嵌代码题严格校验问题描述、输入输出、约束、2 个公开示例、目标复杂度、4 项 rubric 与 Python/JavaScript/Java 模板；三语言缓冲区以版本化 JSON 共存于现有 `DraftCode` 槽，逐语言编辑、无外部依赖格式化、模板重置、语言切换与重启恢复通过；主流程=打开模板 → 编辑保存 → 格式化/重置 → 可选 Runner 执行 → `code_submissions` 不可变快照 → 幂等恢复；加载中=`Pending → saving_draft → Succeeded/Failed`，格式化与运行追加确定性 Streaming 阶段；空数据=`LatestRun=nil` 且未运行草稿只留本地；报错=Runner disabled 返回“代码执行未启用”与 `RUNNER_MODE=docker` 指引且不写代码证据，存储失败、损坏草稿/运行结果、无效 Runner Schema 与取消均类型化且不泄露底层细节；领域运行结果仅含公开测试名称/状态、隐藏测试通过/失败计数、枚举错误与安全资源统计，不存在隐藏输入、预期输出、原始 stderr 或容器路径字段；无 Runner 回归=`未运行代码草稿 → 文字面试 → Coach（CodeRuns 为空且草稿不可见）→ 文字回答 → 证据化报告`，报告 `CodeRunCount=0` 且代码质量为“不适用”；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；coding 覆盖率 80.7%；`-race`=N/A（Windows 环境缺少 CGO C 编译器） | `feat(coding): T-019 add coding domain and Lite fallback` |
| R-020 | T-020 | 实现可选 Docker Runner、资源限制、公开/隐藏测试协议和安全诊断 | `internal/adapters/runner/`、`docker/runner/`、`scripts/` 隔离测试、相关文档/测试、`TODO.md` | Runner 默认关闭；容器禁网、非 root、只读、限 CPU/内存/进程/时间且每次销毁；不泄露宿主/容器路径、密钥或隐藏输入；不提前实现 T-021 | `RUNNER_MODE` 默认 disabled，普通测试与 Lite 文字/Coach/报告链路不依赖 Docker；镜像以 `docker/runner` 为唯一上下文构建，标签=true、默认用户=65532:65532、大小=203999278 bytes，Python 3.12.13/Node 22.23.2/Java 21.0.11 在禁网、只读根、非 root、cap-drop、no-new-privileges、CPU/256MB/PID/ulimit/noexec tmpfs/IPC/总时限约束下可用；Python/JavaScript/Java 正确与错误实现均实测，2 个公开测试只返回名称/状态，隐藏测试只返回通过/失败计数，耗时/峰值内存合法且源码、隐藏名称/输入/预期、stderr、路径、环境变量和密钥未泄露；主流程通过/失败、加载中 elapsed streaming 且调用方编辑状态独立、空数据 `LatestRun=nil`、timeout/OOM/network denied/Runner 或镜像不健康/cancellation/invalid protocol 四态均通过；死循环、网络请求、内存炸弹、进程炸弹实测返回受控结果，每次后零残留，隔离门禁最终 30.957s、容器清理率 100%；Agent 测试 29.2%、Runner 普通测试 80.7%，`gofmt`/PowerShell AST/`git diff --check`/`go mod verify`/两模块 `go vet`/`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`/单二进制构建/Lite disabled 定向回归/隔离脚本均通过；`-race`=N/A（Windows 环境缺少 gcc） | `feat(runner): T-020 add isolated Docker execution` |
| R-021 | T-021 | 实现题目规格、CodeEditor、RunSummary、Coach 错误解释入口和响应式代码界面 | `internal/tui/screens/coding/`、`internal/tui/components/coding*`、对应测试、`TODO.md` | 规格与编辑器分区；运行后 RunSummary 常驻；错误不泄露路径、隐藏数据或底层 cause；Strict 不生成完整实现；不提前实现 T-022 | `CodeEditor` 支持 Python/JavaScript/Java、rune/CJK 光标、行号、草稿恢复、格式化与模板重置，`RunSummary` 覆盖 not-run/running/passed/failed/timeout/OOM/disabled/error 且只显示公开名称/状态、隐藏计数与安全资源数据；全键盘=`Ctrl+1/2/3` 切语言、`Ctrl+S/F/Z/R/E/H` 保存/格式化/重置/运行/解释/返回，Tab/帮助 overlay/resize 精确恢复焦点与光标；主流程=SQLite 草稿恢复 → 编辑保存 → 运行不可变快照 → Coach 解释入口 → 返回面试，加载中持续显示 elapsed、禁重复 Run/保存/切换/格式化/重置/解释但编辑器仍可写且运行快照不被并发编辑覆盖；空数据显示公开测试未运行，报错覆盖 disabled/failed/timeout/OOM 与长底层错误不泄露宿主/容器路径、stderr、隐藏输入或预期值；160×48、120×36、80×24、72×22 阻塞、ASCII、CJK、长错误及帮助焦点快照/几何通过；真实 SQLite+coding Service+Strict Coach Policy+evaluation/report 回归中，恶意完整实现被拒绝且不持久化/渲染，已运行 `code_submission` 进入报告并得到 `CodeRunCount=1` 与代码质量 evidence；`gofmt -l` 无输出、`git diff --check`、`go mod verify`、`go vet ./...`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、单二进制构建通过；components/coding screen 覆盖率 83.9%/76.7%；`-race`=N/A（Windows 环境缺少 gcc） | `feat(coding-ui): T-021 add code interview workbench` |
| R-022 | T-022 | 完成 MVP 全链路质量门、发布配置和使用/部署/安全/贡献文档 | 全仓测试与直接修复、`scripts/`、`README.md`、新增 ADR/部署/安全/质量/贡献文档、CI/发布配置、`TODO.md` | 不降低 T-001～T-021 门禁；不引入 MVP Non-Goals；Lite 默认 `RUNNER_MODE=disabled` 且不依赖 Docker/外部数据库；不泄露密钥、路径、隐藏测试或无证据结论 | 新增 Lite E2E 实测 `init → doctor → run → 粘贴简历 → 确认/锁定画像 → 场景 → 文字面试 → Coach → 可选代码 → 报告 → 下一轮 → export/import → 恢复运行`，异步阶段、未运行空态、80×24 ASCII/reduce-motion、配置/密钥不进入迁移包均通过，结论证据覆盖率 `15/15=100%`；P-01～P-07 主流程/加载/空/依赖错误及 160×48、120×36、80×24、72×22 阻塞、CJK、长路径/错误、ASCII、no-color、减弱动效、全键盘证据矩阵已自动化并文档化；Docker-free 发布门禁最终 154.9s 退出 0，包含 `gofmt`、`git diff --check`、两模块 `go mod verify`/`go vet`、`CGO_ENABLED=0 GOPROXY=off go test -count=1 -cover ./...`、迁移、Runner agent 29.2%、单二进制/fresh-install 与 Windows/Linux/macOS × amd64/arm64 六目标交叉编译；真实 Runner 隔离门 90.6s 退出 0（Docker integration 33.241s），三语言/超时/OOM/禁网/死循环/进程炸弹等攻击通过，事后 `integration_containers=0`、`protocol_images=0`、`running_containers=0`、镜像 label=true、user=65532:65532；GoReleaser/CI YAML 语法、Markdown 本地链接和仓库无二进制/DB/log 污染检查通过；README 覆盖发行安装、`init/run/doctor`、Lite/Private Local/Full Practice、环境变量、迁移、Runner 安全与贡献；`-race`=N/A（Windows 环境缺少 gcc，未误报通过） | `chore(release): T-022 complete MVP quality gates` |
| R-023 | T-023 | 用 Bubble Tea v2.0.8 建立常驻根事件循环并接通 P-01～P-07 完整路由 | `internal/tui/app/`、CLI/main 启动接线、Profile/Interview 最小输入接口、Go 模块依赖、入口烟测、README、`TODO.md` | 未改领域服务、SQLite 事件顺序、证据链、Coach/Interviewer 隔离或 Runner 安全；Lite 继续无 Docker 可用；`.interviewcraft/` 未跟踪且未暂存 | 根控制器统一 Training/Profile/Scenario/Interview/Coding/Report/Settings 路由和持久化 ID，操作 token 丢弃切页/取消后的过期结果，非并发安全屏幕延迟 resize、Interview/Coding 保持异步编辑；字符/粘贴/UTF-8 Backspace/Ctrl 键与定时 tick 接入，`Ctrl+K` 打开代码、完成会话 `[r]` 打开报告；普通 `run` 非 TTY 返回行动提示，`run --once` 完成 80×24 CI/fresh-install 单帧；主流程、加载中、空数据、Provider/SQLite/终端/Runner 错误与取消、resize/粘贴/过期消息自动化通过；最终 `test-release-quality.ps1 -SkipRunnerIsolation` 356.6s 退出 0，含 gofmt、`git diff --check`、两模块 verify/vet/test、迁移、Runner agent 29.2%、fresh-install、单二进制及六目标交叉构建；根模块全量测试通过，Runner 隔离按本轮计划未重复运行 | `feat(tui-app): T-023 run the complete interactive journey` |
| R-024 | T-024 | 新增可恢复的三档 setup，并统一系统凭据库、环境变量解析与原子配置保存 | `internal/setup/`、`internal/credentials/`、CLI/config/doctor/Settings 最小接线、Go 模块依赖、README、`TODO.md` | `init`、`doctor`、环境变量、Settings/export/import 保持兼容；API Key 不进入配置/参数/日志/状态；凭据库故障不明文降级；Runner 默认 disabled，仅探测本地 Docker 与 `interviewcraft-runner:local`，不拉取镜像；`.interviewcraft/` 未跟踪且未暂存 | Lite/OpenAI-compatible、Private Local/Ollama、Full 本地 Runner 三主流程通过；七阶段 Pending/Streaming/Succeeded/Failed 顺序确定，取消保留无密钥检查点并从安全阶段恢复，`--restart` 可显式丢弃不匹配状态；空凭据/新目录返回可行动 usage 且无半配置；环境变量优先于 go-keyring v0.2.8，隐藏输入/stdin、凭据库不可用、Provider/Ollama、SQLite、检查点损坏、原子配置故障和旧凭据回滚均自动化覆盖；重复 setup 保留未显式更改 Provider 字段，设置页与 doctor 使用同一凭据解析和原子保存；根模块与 Runner agent verify/vet/无缓存测试 97.1s 退出 0；最终 `test-release-quality.ps1 -SkipRunnerIsolation` 211.2s 退出 0，包含覆盖率测试、迁移、Runner agent 29.2%、fresh-install、单二进制及六目标交叉构建；末尾 Go 统计缓存权限警告不影响退出码 | `feat(setup): T-024 add idempotent secure setup` |
| R-025 | T-025 | 建立版本契约、严格发布清单、SPDX SBOM、Sigstore 签名与 Draft 发布后回读门禁 | `internal/version/`、CLI version、`.goreleaser.yaml`、release workflow、发布元数据脚本/fixture、README、`TODO.md` | 完整质量门保持前置；不含长期私钥；manifest/signature/checksum/version 必须一致；不改运行时与 Runner 安全边界；不执行本机安装/升级；`.interviewcraft/` 未跟踪且未暂存 | `version`/`--json` 源码 `dev/unknown` 与 ldflags 正式注入通过；Tab v1 manifest 严格拒绝未知行、重复/缺失平台、非法文件名、路径分隔、零大小、重复文件和非小写 SHA-256，并对下载目录逐文件复算 hash/size；GoReleaser v2.15.4 `check` 通过，snapshot 101.5s 退出 0，生成 Windows/Linux/macOS × amd64/arm64 六归档与 checksum，归档内二进制版本实测一致；本地 fixture 覆盖空/主流程/生成进度等价阶段、篡改资产/清单与临时 RSA 密钥签验拒绝，YAML 语法通过；工作流固定 Cosign v3.1.3 和 `cosign-installer@6f9f177...`，身份精确限定 tag workflow 与 GitHub OIDC issuer，先本地构建/SPDX/manifest/签名/来源证明，再创建 Draft、上传、重新下载验证 signature/hash/size/version/provenance 后解除 Draft；GitHub OIDC、真实 Draft Release 与 macOS runner 属本地边界未运行，未伪报通过；根模块和 Runner agent 全回归 73.7s 退出 0，最终 `test-release-quality.ps1 -SkipRunnerIsolation` 159.6s 退出 0，version 覆盖率 82.1%、Runner agent 29.2%、fresh-install 和六目标交叉构建通过；末尾 Go 统计缓存权限警告不影响退出码 | `chore(distribution): T-025 publish verifiable release metadata` |
| R-026 | T-026 | 提供 Windows、Linux、macOS 的可信一键安装/卸载、用户 PATH 托管与无密钥安装收据 | `scripts/install*`/`uninstall*`、Cosign v3.1.3 SHA-256 矩阵、跨平台 fixture、`.goreleaser.yaml`、release workflow、`.gitignore`、README、部署文档、`TODO.md` | 仅写当前用户目录/PATH，不请求管理员或 root；Cosign、Sigstore manifest、hash/size、归档路径和内嵌版本未全部通过前不安装；异版本拒绝覆盖并留给 T-028；失败回滚新二进制/PATH，卸载保留配置、凭据、SQLite 和 `~/.interviewcraft`；`.interviewcraft/` 已忽略且未删除/暂存 | PowerShell 5.1 本地 HTTP fixture 完成 `安装 → setup(private-local Ollama fixture) → doctor → 同版本重装 → 卸载`，Linux 容器以 UID 65532 完成安装/幂等/卸载，原生 POSIX fixture 在 Go Linux 容器 59.9s 退出 0；七阶段输出覆盖加载态，首次无二进制/无收据与缺平台资产覆盖空态；错误签名、manifest 缺平台、错误 hash、截断包、Zip Slip/额外可执行文件、磁盘不足、安装/PATH 权限失败、setup Provider 失败、异版本覆盖均非零且无残缺二进制/PATH，数据保留；固定 Cosign hash 矩阵漂移测试、PowerShell AST、POSIX `sh -n`、ShellCheck 零告警通过；GoReleaser v2.15.4 `check` 与六平台归档 snapshot（本轮仅跳过 T-025 已验证的 SBOM 工具阶段）56.1s 退出 0，归档含四个脚本和 hash 矩阵；最终 Docker-free `test-release-quality.ps1 -SkipRunnerIsolation` 170.0s 退出 0，含根模块/Runner agent verify、vet、覆盖率测试、fresh-install、release metadata、Windows installer 与六目标交叉构建；独立 Runner 隔离门 35.0s 退出 0且零残留；本机无 PowerShell 7 和 macOS runner，故其真实运行及 GitHub Release 网络/OIDC 均注明本地边界未运行，POSIX 共用路径已由 Linux fixture 覆盖且 CI 非 Windows 分支已接线 | `feat(installer): T-026 add verified one-command installation` |
| R-027 | T-027 | 发布双架构签名 Runner，并以不可变 digest 完成 Full setup、复验与运行时降级 | `docker/runner/`、`internal/adapters/runner/`、`internal/config/`、`internal/doctor/`、`internal/setup/`、CLI/TUI 最小接线、Runner 发布/隔离脚本、release workflow、README/部署/安全文档、`TODO.md` | Runner 默认 disabled；Lite/Private Local 不访问 Docker/Registry；不安装/启动 Docker或提权；只接受官方仓库、签名有效、版本/协议/架构/标签/用户匹配的 digest；原禁网、非 root、只读、无挂载、cap-drop、no-new-privileges、CPU/内存/PID/ulimit/noexec/清理和隐藏测试边界不变；失败不阻塞文字主链路；`.interviewcraft/` 保持忽略且未暂存 | release workflow 用固定 commit 的 QEMU/Buildx/login/build-push actions 构建 linux/amd64 与 linux/arm64，输出 digest、SBOM/provenance，Cosign v3.1.3 keyless 签名每个 digest并生成/签名/回读严格 `runner-manifest.txt`，Draft 解除前再次验证精确 workflow identity 与 GitHub OIDC issuer；Full setup 八阶段内展开 resolve/pull/signature/inspect/smoke/enable，Ctrl+C 可取消，只有全部成功才原子保存 mode 与官方 digest 元数据，并把固定 SHA-256 验证的 Cosign 原子保存到数据目录供 doctor/TUI 复验；配置拒绝单独设置 docker，doctor 与运行时复验 daemon、签名、RepoDigest、linux 架构、应用版本、协议、runner 标签与 65532:65532 用户，失败只降级代码执行；严格 Go/PowerShell fixture 覆盖主流程、加载、空清单/缺平台、Registry/pull、manifest/image 签名、大小写 digest、重复架构、外部仓库、标签、用户、协议、daemon、smoke、取消、恢复不重复拉取、持久化验证器与新增镜像失败清理；根模块与 Runner agent 无缓存回归通过；Docker-free 完整质量门 413.0s 退出 0（Runner adapter 覆盖率 72.1%，含 fresh-install、发布/安装器 fixture、六目标构建）；真实 amd64 Docker 隔离门 121.8s 退出 0，三语言与攻击测试 30.950s、零残留容器，镜像版本/协议/用户标签实检通过；GitHub OIDC、真实 GHCR push/sign、多架构远端构建和 arm64/macOS 真机属于本地边界未运行，未伪报通过，工作流 YAML 与等价失败逻辑已本地验证 | `feat(runner-deploy): T-027 publish and provision signed runner images` |
