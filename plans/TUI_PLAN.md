# mihomori-tui TUI 实现计划

> 交互式终端客户端 `mihomori-tui tui`,通过 HTTP/SSE 连接运行中的 `mihomori`,在终端完成运行时控制:内核状态、实时日志、启停内核、管理 profiles。
>
> 本文档既是设计说明,也是可勾选的执行清单。每一步为最小化拆解,完成后均可独立编译或测试验证。

---

## 一、设计决策

### 1.1 架构:独立二进制 + HTTP 客户端

```
[mihomori 二进制]                  [mihomori-tui 二进制]
  /api/control/kernel/*   <── HTTP ──┐
  /api/control/profiles/*             ├── client.go (Bearer 认证)
  /api/control/kernel/logs  <─ SSE ───┘
  /api/control/config/*   <── HTTP ───── (Phase 3)
        ▲                                  ▲
        └──── 复用 internal 契约类型 ───────┘
            (supervisor.KernelState / profile.Meta …)
```

- **独立 cmd 包** `cmd/mihomori-tui/`,纯 TUI 客户端,顶层 flag 直连 server。
- **HTTP 客户端模式**:复用 server 现有 `/api/control/**`,零逻辑重复。
- **复用契约类型**:与 server 同仓库,直接 `import ".../internal/supervisor"`,client/server 数据结构零漂移。
- **不改 server 任何代码**,零回归;静态资源抽离与本计划正交。

### 1.2 认证(基于 `internal/auth` 调研)

| server 模式 | TUI 行为 |
|------------|---------|
| `CONTROL_TOKEN` 为空(open) | 不加认证头 |
| `CONTROL_TOKEN` 非空 | 每个请求加 `Authorization: Bearer <token>`,SSE 同样 |

- token 来源:`--token` flag → `CONTROL_TOKEN` env → 无
- endpoint 来源:`--endpoint URL` flag → 默认 `http://127.0.0.1:<CONTROL_PORT>`

### 1.3 技术选型

**Bubble Tea**(`charmbracelet/bubbletea`)+ Lipgloss(样式)+ Bubbles(viewport/table/spinner)
- 日志流(viewport)、profiles 表格(table)均有现成组件;Elm 架构契合「后台 SSE 推送 + 按键」。
- 备选 `tview`:若 Phase 3 表单复杂可换,**只影响 `views/` 层**,`client.go`/`sse.go`/契约不变。

---

## 二、目录结构

```
cmd/mihomori-tui/main.go        # 极薄入口:flag 解析 + client 构造 + 启动 TUI
internal/ctl/
  ├── client.go         # API client:kernel/profile/config,封装 Bearer 认证
  ├── client_test.go    # httptest 假 server 测各端点
  ├── sse.go            # SSE 订阅 /api/control/kernel/logs + 事件解析
  ├── sse_test.go       # text/event-stream 解析单测
  └── tui/
      ├── tui.go        # 顶层 Model + Program 启动 + 窗口自适应
      ├── theme.go      # 颜色/样式常量(状态色)
      ├── status.go     # Phase 1:内核状态栏
      ├── logs.go       # Phase 1:实时日志 viewport + 过滤
      └── profiles.go   # Phase 2:profiles 表格
```

> 业务逻辑放 `internal/ctl` 而非 cmd 内,便于单测;`main.go` 仅做参数解析→调用。

---

## 三、界面布局

```
┌─ mihomori-tui ───────────────────────────── 127.0.0.1:9097  12:34:56 ─┐
│ ● running   pid 12345   mihomo v1.19.29   ec: 127.0.0.1:9090            │
│ mixed-port: 7890   active: my-profile (订阅, 2h前更新)                  │
├──────────────────────────────────────────────────────────────────────────┤
│ [1] Logs   [2] Profiles   [3] Config                                    │
├──────────────────────────────────────────────────────────────────────────┤
│ 2025-08-12 15:30:01 INFO  inbound mixed port 7890 listening             │
│ 2025-08-12 15:30:01 INFO  GEOIP provider ready                          │
│ ...                                                                     │ ← SSE 日志流
├──────────────────────────────────────────────────────────────────────────┤
│ s:start  S:stop  r:restart  R:rollback  c:recover  /:filter  q:quit    │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## 四、数据流(绝不阻塞 render 循环)

```
TUI Model
   │  tea.Cmd 启动后台 goroutine
   ▼
[SSE goroutine] ──GET /kernel/logs (Bearer)──> server
   │  逐行解析 data:{...} → KernelLogLine / stateEvent
   │  每行/每状态 → tea.Msg
   ▼
Model.Update(msg) → 追加日志 / 更新状态 → 重绘

[按键] → Update(KeyMsg) → 返回 tea.Cmd → client.Start()/Restart() ──HTTP──> server
```

退出时 cancel SSE 的 `http.Request.Context()`,防 goroutine 泄漏。

---

## 五、键位设计(Phase 1)

> Kernel 操作**不用快捷键**(防误触),移入独立 Kernel Tab:`↑↓` 选择 + `Enter` 执行,Recover 二次确认。

| 键 | 动作 |
|----|------|
| `1/2/3` | 切 Tab(Logs/Profiles/Config) |
| `↑↓` / `k/j` | Kernel Tab:选择操作 |
| `Enter` | Kernel Tab:执行选中操作 |
| `y` / 其他 | Recover 确认 / 取消 |
| `/` | 日志过滤 |
| `f` | 跟随末尾开关 |
| `PgUp/PgDn` / 滚轮 | 翻页 |
| `q` / `Ctrl+C` | 退出 |
| `?` | 帮助 |

---

## 六、执行步骤(最小化拆解)

> 约定:每个步骤完成后,先本地验证再勾选。验证命令默认在仓库根目录执行。

### Phase 0 — 脚手架与依赖

- [x] **0.1** 创建 `cmd/mihomori-tui/main.go`:仅 `package main` + `func main(){ fmt.Println("mihomori-tui") }`
  - 验证:`go build ./cmd/mihomori-tui` 编译通过
- [x] **0.2** `main.go` 解析顶层 flag `--endpoint/--token/--insecure`(默认 `http://127.0.0.1:8080`、`CONTROL_TOKEN` env);TUI 未实现前打印配置 stub 并 exit 1
  - 验证:`./mihomori-tui --help` 显示 flags;`./mihomori-tui` 打印配置 stub、exit code 1
- [x] **0.3** ~~`go get` 三依赖~~ → **依赖随首次 import 引入**(1.16 tui.go 首次 import bubbletea 时 go get bubbletea+lipgloss;1.21 viewport 时 go get bubbles;避免未使用依赖的 `// indirect` 垃圾)
  - 验证:`go build ./...` 全仓库通过
  - 验证:`go build ./...` 全仓库通过

### Phase 1a — Client 基础

- [x] **1.1** `internal/ctl/client.go`:`type Client struct { endpoint, token string; hc *http.Client }` + `NewClient(endpoint, token string, insecure bool) *Client`(insecure 时 `TLSClientConfig.InsecureSkipVerify=true`,默认 30s 超时)
  - 验证:`go vet ./internal/ctl` 通过
- [x] **1.2** `client.go` 私有 `do(method, path string, body io.Reader) (*http.Response, error)`:拼接 endpoint + path,token 非空时设 `Authorization: Bearer <token>`,设 `Accept: application/json`
  - 验证:编译通过
- [x] **1.3** `client_test.go`:`httptest.NewServer` 验证 —— token 非空时收到 `Authorization: Bearer`;token 为空时无该头
  - 验证:`go test ./internal/ctl -run TestClientAuth -v` 通过
- [x] **1.4** `client_test.go`:验证 path 拼接正确(请求 URL == endpoint+path)
  - 验证:`go test ./internal/ctl -run TestClientPath -v` 通过

### Phase 1b — Kernel API

- [x] **1.5** `client.KernelStatus() (supervisor.KernelState, error)`:`GET /api/control/kernel/status`,`json.Decode` 复用 `supervisor.KernelState`
  - 验证:编译通过(确认能 import `internal/supervisor`)
- [x] **1.6** `client_test.go`:假 handler 返回一段 `KernelState` JSON,断言 `Status/PID/Version` 解码正确
  - 验证:`go test ./internal/ctl -run TestKernelStatus -v` 通过
- [x] **1.7** `client.KernelStart/Stop/Restart() (supervisor.KernelState, error)`:三个 `POST` 方法,各自路径
  - 验证:编译通过
- [x] **1.8** `client.KernelRollback/Recover() (supervisor.KernelState, error)`:两个 `POST`
  - 验证:编译通过
- [x] **1.9** `client_test.go`:覆盖上述 5 个 POST —— 成功(200)返回 state;失败(500)返回 error 且携带 `lastError`
  - 验证:`go test ./internal/ctl -run TestKernelOps -v` 通过

### Phase 1c — SSE 订阅

- [x] **1.10** `internal/ctl/sse.go`:`type Event struct{ Type string; Data string }` + `parseStream(r *bufio.Reader, emit func(Event))`:按行解析 `data:` 前缀,多行 data 以 `\n` 拼接,空行触发 emit,忽略 `:` 注释行
  - 验证:编译通过
- [x] **1.11** `sse_test.go`:单测 —— 单行 data、多行 data 拼接、空行分隔、注释行跳过、无尾空行的边界
  - 验证:`go test ./internal/ctl -run TestSSEParse -v` 通过
- [x] **1.12** `client.SubscribeLogs(ctx context.Context) (<-chan Event, error)`:`GET /api/control/kernel/logs`(Bearer),起 goroutine 用 `parseStream` 喂 channel,ctx.Done 时关闭 resp.Body 与 channel
  - 验证:编译通过
- [x] **1.13** `client_test.go`:假 SSE server 持续写 `data:{"type":"log",...}\n\n`,断言收到若干 Event 后 ctx cancel 能干净退出(无 goroutine 泄漏:用 `goleak` 或超时断言)
  - 验证:`go test ./internal/ctl -run TestSubscribeLogs -v` 通过

### Phase 1d — TUI 框架

- [x] **1.14** ~~独立 theme.go~~ → **延后到 1.16 与 bubbletea 首次引入合并**(theme 不单独触发 lipgloss;1.16 时 theme.go + tui.go 骨架一起,依赖一次到位)
  - 验证:编译通过
- [x] **1.15** `internal/ctl/tui/tui.go`:`type Model struct { client *ctl.Client; state *supervisor.KernelState; width,height int; quitting bool }` + `New(client) Model`
  - 验证:编译通过
- [x] **1.16**(与 1.15 合并提交,含延后的 theme.go;首次 import bubbletea+lipgloss)`tui.go`:`Init() tea.Cmd`(返回初始状态拉取 + SSE 订阅 cmd)、`Update(msg)` 骨架(处理 `tea.Quit`/`tea.KeyMsg` q)、`View()` 返回占位字符串
  - 验证:编译通过
- [x] **1.17** `main.go`:用 0.2 解析的 flag 构造 client,`tea.NewProgram(tui.New(client)).Run()`
  - 验证:`./mihomori-tui --endpoint http://127.0.0.1:PORT` 能启动(连不上也先不崩),按 `q` 退出不卡死

### Phase 1e — 状态栏

- [x] **1.18** `status.go`:`renderStatus(state *supervisor.KernelState, endpoint string) string`,用 theme 渲染状态点 + status/pid/version/externalController
  - 验证:`go test ./internal/ctl/tui -run TestRenderStatus -v`(纯函数快照测试)通过
- [x] **1.19** `tui.go`:`statusTick` —— `Init` 返回 `tea.Tick(time.Second)` 产生 `tickMsg`;`Update` 收到 tickMsg 后返回 `client.KernelStatus()` 的 `tea.Cmd`(包成 `statusLoadedMsg`)
  - 验证:编译通过
- [x] **1.20** `tui.go`:`Update` 处理 `statusLoadedMsg` 更新 `m.state`,处理 error 写入 `m.err`;`View` 顶部渲染 `renderStatus`
  - 验证:启动后状态栏显示 status/pid(连真实或假 server)

### Phase 1f — 实时日志

- [x] **1.21** `logs.go`:`type LogsModel struct { viewport viewport.Model; lines []string; filter string; follow bool }` + `NewLogsModel()`,`Update`/`View` 委托 viewport
  - 验证:编译通过(引入 `bubbles/viewport`)
※ 顺序调整(用户决定):先 1.24 布局骨架,再 1.22/1.23 数据流
- [x] **1.22** `tui.go`:SSE 订阅 cmd 返回 `logMsg(Event)`/`stateMsg(Event)`;`Update` 把 log 行 append 到 LogsModel
  - 验证:编译通过
- [x] **1.23** `logs.go`:`append(line)` 追加 + 按当前 filter 过滤 + 重设 viewport 内容;`follow==true` 时 `viewport.GotoBottom()`
  - 验证:编译通过
 - [x] **1.24*(提前执行,布局先行)* `tui.go`:`View` 中间区渲染 `m.logs.View()`;整体布局用 lipgloss 垂直拼接(状态栏 / Tab / 日志 / 底部提示)
  - 验证:启动后实时日志出现并自动滚动到底

### Phase 1g — 按键控制

- [x] **1.25**(重定义:用户决定 Kernel 操作不用快捷键防误触)**Kernel 操作并入 Config Tab**(不单独占 Tab):Tab 结构 `[Logs/Profiles/Config]`(Config 放最后);`↑↓`/`k/j` 选择操作(Start/Stop/Restart;Rollback/Recover 已注释,后续再启用),`Enter` 执行,完成后刷新 status;Config 编辑区(Phase 3)在其下方
  - 验证:按键触发对应 HTTP 调用(假 server 日志可见请求)
- [x] **1.26** `tui.go`:操作进行中显示 spinner(用 `bubbles/spinner`),完成后隐藏
  - 验证:编译通过
- [x] **1.27** `tui.go`:`c`→进入 `confirmingRecover` 子状态,View 渲染确认框;`y` 确认执行 `Recover()`,其他键取消
  - 验证:按 c 出现确认框,y 执行,其他键取消

### Phase 1h — 边界与健壮性

- [x] **1.28** `main.go`:进入 TUI 前先 `client.KernelStatus()` 探测;失败(连接拒绝/超时)打印清晰错误并退出,不进 TUI
  - 验证:endpoint 错误时输出友好提示、exit 1
- [x] **1.29** `tui.go`:HTTP 401 → 状态栏提示「认证失败:token 无效或 server 开启了认证」
  - 验证:token 错误时状态栏显示提示而非空屏
- [x] **1.30** `tui.go`:处理 `tea.WindowSizeMsg` 存 width/height,传给 logs viewport `SetContent`/`Height`;窄屏(<60 列)只显示日志
  - 验证:调整终端窗口大小,布局自适应
- [x] **1.31** `tui.go`:退出路径(`q`/`Ctrl+C`/`tea.Quit`)前 cancel SSE context,确保 goroutine 回收
  - 验证:退出后 `go test` 无 goroutine 泄漏(或手动 `kill -QUIT` 查 goroutine)
- [x] **1.32** `logs.go`:`/` 进入 filter 输入态,回车应用、过滤日志;`f` 切 follow;`PgUp/PgDn`/`↑↓` 翻页
  - 验证:输入过滤词后日志被过滤,f 切换自动跟随

### Phase 1i — 构建与发布

- [x] **1.33*(延后:构建/发布/文档暂缓,后续再启用)* `.goreleaser.yaml`:在 `builds:` 下追加一项 `main: ./cmd/mihomori-tui`,`binary: mihomori-tui`,env/goos/goarch/ldflags 与 server 项一致
  - 验证:`goreleaser build --snapshot --clean` 同时产出两个二进制
- [x] **1.34*(延后)* `Dockerfile`:构建阶段 `go build ... -o /out/mihomori-tui ./cmd/mihomori-tui`,运行阶段 `COPY` 进镜像
  - 验证:`docker build` 产出含两个二进制的镜像
- [x] **1.35*(延后)* `README.md` / `README.zh-CN.md`:新增「mihomori-tui」章节(安装、`tui`/flag、键位速查表)
  - 验证:文档渲染正常,示例可跑
- [x] **1.36*(延后:全量验证待上述启用后执行)* 全量验证:`go build ./... && go test ./...` 通过;`./mihomori-tui tui` 连真实 server 跑通状态+日志+启停
  - 验证:CI 绿

---

### Phase 2 — Profiles 管理

- [x] **2.1** `client.go`:`ProfilesList() ([]profile.Meta, error)`(GET)、`Activate/Refresh/RefreshAndActivate/Delete(id)`、`Import(url)` 各 POST
- [x] **2.2** `client_test.go`:覆盖 list 解码 + 各操作成功/失败
- [x] **2.3** `profiles.go`:用 `bubbles/table` 渲染 profiles(name/type/active/updated),高亮 active
- [x] **2.4** `tui.go`:Tab 切换(`1/2/3`),active profile 信息回显到状态栏第二行
- [x] **2.5** `profiles.go`:按键 `a`激活、`u`刷新、`d`删除(确认)、`i`导入(输入框粘订阅 URL)
- [x] **2.6** 操作后刷新列表 + status
- [x] **2.7** 验证:对真实 server 增删改 profiles 反映正确

### Phase 3 — 配置与维护

- [ ] **3.1** `client.go`:`GetConfig()/GetRuntimeConfig() (string,error)`、`PutSection(key,val,restart)`、`GeoUpdate()`、`Backup/Restore(webdav)`
- [ ] **3.2** `client_test.go`:覆盖各端点
- [ ] **3.3** `config.go`:只读 viewport 展示 active/runtime config(`2` Tab,与 kernel 操作同区)
- [ ] **3.4** section 编辑:选择 key(如 mixed-port)→ 输入新值 → `PutSection`
- [ ] **3.5** GEO 更新:`g` 触发,spinner 显示进度,完成提示写入文件
- [ ] **3.6** WebDAV 备份/恢复表单(评估是否切 `tview` 做表单)
- [ ] **3.7** 验证:配置改动持久化 + 重启生效;geo 文件落地;备份/恢复往返一致

---

## 七、改动清单(汇总)

**新增**
- `cmd/mihomori-tui/main.go`
- `internal/ctl/{client,client_test,sse,sse_test}.go`
- `internal/ctl/tui/{tui,theme,status,logs}.go`(Phase 1);`profiles.go`(Phase 2);`config.go`(Phase 3)

**修改**
- `go.mod` / `go.sum`:加 `charmbracelet/{bubbletea,bubbles,lipgloss}`
- `.goreleaser.yaml`:`builds:` 追加 `mihomori-tui` 项
- `Dockerfile`:构建 + COPY `mihomori-tui`
- `README.md` / `README.zh-CN.md`:补充用法

**不改**:`cmd/mihomori/**`、`internal/control/**`、`internal/auth/**` 等 server 全部代码。

---

## 八、测试策略

| 层 | 方法 |
|----|------|
| `client.go` | `httptest.NewServer` 挂假 handler,断言 Bearer 头、path、JSON 解码、错误传播 |
| `sse.go` | `text/event-stream` 解析单测(单行/多行/截断/注释) |
| TUI model | `Update()` 纯函数测试(给定 Msg → 断言 Cmd/状态) |
| 集成(可选) | 起真 server(无 UI)+ `ctl tui` smoke test |

目标:Phase 1 完成时 `go test ./internal/ctl/...` 覆盖 client + sse + 纯渲染函数。

---

## 九、风险与注意

1. **SSE 泄漏**:退出/重连必须 cancel context;Phase 1.13 / 1.31 验证无 goroutine 泄漏。
2. **认证失败**:401 → 状态栏提示,不崩溃(1.29)。
3. **endpoint 连不上**:启动探测,失败给清晰错误(1.28)。
4. **终端尺寸**:`WindowSizeMsg` 自适应,窄屏降级(1.30)。
5. **recover 危险操作**:二次确认弹层(1.27)。
6. **契约漂移风险**:因复用 `internal/supervisor` 类型,server 改字段时 TUI 编译期即暴露 —— 这是优势而非风险。

---

## 十、执行进度

- Phase 0:⬜ 未开始
- Phase 1:⬜ 未开始
- Phase 2:✅ 完成(用户已验证增删改 profiles)
- Phase 3:⬜ 未开始(未完成事项已整理至 tui_plan_1.md)

> 实施时按步骤勾选;每完成一个 Phase 更新本节状态。
