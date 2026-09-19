# mihomori-tui 计划：获取 active profile（tui_plan_2）

> 对应 `tui_plan_1.md` §6「activeId 端点（需用户拍板）」的落地方案。
> 官方 API 已支持（agent #2148），本计划按官方契约同步本地实现。

---

## 背景与结论

**结论：官方 API 已加上 `active` 字段，直接同步，无需自建端点。**

官方实现（`official/packages/agent/src/http.ts` ~L245，#2148）：

```ts
// GET /api/control/profiles
const [list, activeId] = await Promise.all([profiles.list(), profiles.getActiveId()])
return list.map((p) => ({ ...p, active: p.id === activeId }))
```

- 类型契约见 `official/packages/ui/types/control.ts` 的 `ProfileMeta.active?: boolean`
- 官方注释明确：`active` 是**派生字段（derived, not stored）**，由 handler 在响应时打标，**不持久化**到 index.json
- 这击中原 §6 缺口：profiles 表格高亮与状态栏回显重启后无法恢复（当前仅会话内跟踪）；Phase 3.3 的 config 展示同样依赖

**放弃原候选方案**（新增 `GET /api/control/profiles/active` 端点）——官方选择了在 list 响应上打标，跟随官方契约。

本地现状（均已确认）：

- `internal/api/profile.go` 的 `Meta` 无 `Active` 字段
- `internal/server/control/profiles.go` 的 `GET /api/control/profiles` 直接返回 `p.List()`；`Store.GetActiveID()` 已存在可直接复用
- TUI 侧 `pages/profiles.Model` 仅会话内跟踪 `activeID`

---

## 修改步骤

### 1. `internal/api/profile.go` — 增加派生字段

```go
Active bool `json:"active,omitempty"` // derived: stamped by GET /profiles, never persisted
```

### 2. `internal/server/control/profiles.go` — list handler 打标

与官方一致，在 handler 层派生，不进 index.json：

```go
r.Get("/api/control/profiles", func(w http.ResponseWriter, req *http.Request) {
    list := p.List()
    active := p.GetActiveID()
    for i := range list {
        list[i].Active = list[i].ID == active
    }
    writeJSON(w, http.StatusOK, list)
})
```

其他端点（create/update/refresh 等）保持返回原始 meta，与官方行为一致。

### 3. TUI 客户端 — 无需改动

`internal/tui/client/client.go` 的 `ProfilesList()` 解析 `api.Meta`，自动获得新字段。

### 4. `internal/tui/pages/profiles/profiles.go` — 从列表派生 activeID

- `ProfilesLoadedMsg` 处理路径（`SetProfiles`）：若列表中存在 `meta.Active == true` 的项，以其 id 作为 activeID；否则保留当前值（fallback 到会话内跟踪）
- 更新 `Model` doc comment（移除 "the server does not expose the active id via /profiles" 的过时说明）

### 5. 测试

- `internal/server/control/` 合同测试：
  - 激活某 profile 后 `GET /profiles`，该项含 `"active":true`，其余不含
  - `POST /profiles` 创建返回的 meta 不含 active 字段
- `internal/tui/profiles_test.go`：带 `Active: true` 的列表加载后，表格高亮与状态栏 `active: xxx` 正确恢复（模拟重启恢复场景）

### 6. 验证

- `go build ./... && go test ./...`
- 连真实 server：激活某 profile → 退出重进 TUI → 高亮与状态栏直接恢复
- 确认 profiles 的 `index.json` 中未写入 active 字段

### 7. 文档收尾

- 本文件即为落地方案；`tui_plan_1.md` §6 更新为指向本文件并标记已拍板
- Phase 3.3（config 展示 active profile）因此解除依赖阻塞

---

## 兼容性说明

官方 list 响应中每项总是带 `active` key（true/false 显式输出）；本地用 `omitempty` 只在 true 时输出。两者对消费方 `p.active` 的真值判断等价，且官方类型标注为可选字段，无兼容性问题。
