# mihomori-tui 计划：Proxies 页节点延迟显示（tui_plan_3）

> 在 Proxy Tab（`internal/tui/pages/proxies/`）为节点添加延迟显示：被动展示 mihomo 已有测速历史，并支持按键对整组发起手动测速。
> 服务端零改动——所有延迟数据经 `/api/clash/` 同源代理直达 mihomo 原生接口。

---

## 背景

- mihomo 的 `GET /proxies` 响应中每个节点自带 `history: [{time, delay}]`（URLTest/Fallback 组自动测速产生），但当前 TUI 的 `Proxy` struct（`internal/tui/client/client.go`）未解析该字段，延迟信息被丢弃。
- mihomo 原生组测速接口经 `clashproxy` 透传即可使用：
  - `GET /api/clash/group/{name}/delay?timeout=5000&url=http://www.gstatic.com/generate_204` → 一次返回整组所有成员延迟 `{"delay": {"节点名": ms}}`
  - `GET /api/clash/proxies/{name}/delay?...` → 单节点 `{"delay": ms}`（本期可选）
- 组测速是阻塞请求（最长 timeout 5s），bubbletea Cmd 异步执行不卡 UI，但需防重复触发。

---

## 数据与展示约定

- **延迟分级**（沿用 `shared/theme.go` 的 `ld(...)` 明暗双主题模式）：

  | 延迟 | 含义 | 颜色 |
  |------|------|------|
  | `< 200ms` | 好 | 绿（2/10） |
  | `200–499ms` | 一般 | 黄（3/11） |
  | `≥ 500ms` 或 `0`（timeout） | 差 | 红（1/9） |
  | 无数据 | 未测速 | 灰（8/7），显示 `--` |

- **delay 语义**：`delay == 0` 表示 timeout；节点延迟是全局属性，按节点名做 key，同一节点出现在多个组不冲突。
- **手动测速参数**：URL/timeout 先写死（`gstatic 204` + 5s），配置化留到后续。
- **渲染示例**：

  ```
  ▾ 自动选择
     ● 香港01 ss  152ms
     ○ 香港02 ss  timeout
     ○ 美国01 vmess  --
  ```

---

## 实施步骤

### ① `internal/tui/client/client.go` — 数据层

- [ ] `Proxy` struct 增加 `History []DelayHistory \`json:"history,omitempty"\``
- [ ] 新增类型：
  ```go
  type DelayHistory struct {
      Time  string `json:"time"`
      Delay int    `json:"delay"` // 0 表示 timeout
  }
  ```
- [ ] 新增方法 `GetGroupDelay(group string, timeout int, testURL string) (map[string]int, error)`（`doJSON` + query 参数拼接）
- 验证：编译通过

### ② `internal/tui/shared/theme.go` — 延迟分级样式

- [ ] 新增 4 个样式：`DelayGoodStyle` / `DelayMidStyle` / `DelayBadStyle` / `DelayNoneStyle`，在 `SetTheme` 中按明暗重建
- [ ] 新增辅助函数 `DelayStyle(ms int) lipgloss.Style` 按上表分级
- 验证：编译通过

### ③ `internal/tui/pages/proxies/msg.go` — 消息

- [ ] 新增 `GroupDelayMsg{ Group string; Delays map[string]int; Err error }`
- 验证：编译通过

### ④ `internal/tui/pages/proxies/cmd.go` — 命令

- [ ] 新增 `GroupDelayCmd(c *client.Client, group string) tea.Cmd`
- 验证：编译通过

### ⑤ `internal/tui/pages/proxies/proxies.go` — 核心逻辑

- [ ] Model 增加字段：`delays map[string]int`（节点名 → 延迟）、`testing map[string]bool`（组名 → 测速中）
- [ ] `ProxiesLoadedMsg` 处理时从每个节点 `History` 取最后一条记录写入 `delays`
- [ ] 新按键 `d`：对当前行所在 group 发起组测速（`testing` 判断防重复；返回 `GroupDelayCmd`）
- [ ] 组测速期间 `Busy()` 返回 true（显示 spinner），该组行尾显示 `testing…`
- [ ] `GroupDelayMsg` 处理：合并 `Delays` 进 `delays`、清除 `testing` 标记；`Err != nil` 时走错误提示
- [ ] `View()` 渲染：成员行行尾追加延迟（分级着色；`0` → 红 `timeout`；无数据 → 灰 `--`）；组行可显示当前选中节点延迟（可选）
- [ ] `Help()` 更新为包含 `d:delay`
- 验证：编译通过，连接真实 server 手动验证

### ⑥ `internal/tui/pages/proxies/proxies_test.go` — 测试

- [ ] history 解析进 `delays`
- [ ] `GroupDelayMsg` 合并更新 + 清除 `testing`
- [ ] 延迟分级渲染（各档颜色 / `timeout` / `--`）
- [ ] `d` 键触发 cmd（含测速中防重复）
- 验证：`go test ./internal/tui/...` 通过

---

## 顺序与整体验证

```
① client → ② theme → ③ msg → ④ cmd → ⑤ model → ⑥ tests
go build ./... && go vet ./... && go test ./internal/tui/...
```

## 后续可选（不在本期）

- 单节点测速（`GET /proxies/{name}/delay`）
- 测速 URL/timeout 配置化
- 组行展示当前选中节点延迟
