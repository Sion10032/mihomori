# mihomori-tui 待办 / 延后事项

> 记录因范围调整、依赖缺口或用户决策而**暂缓**的事项，避免散落在会话记忆中。恢复时按条目逐一处理。

## Phase 1（构建与发布）延后

| 条目 | 内容 | 说明 |
|------|------|------|
| 1.33 | `.goreleaser.yaml` 追加 `mihomori-tui` 构建项 | 用户决定暂缓构建/发布/文档 |
| 1.34 | `Dockerfile` 构建 + COPY `mihomori-tui` | 同上 |
| 1.35 | `README.md` / `README.zh-CN.md` 新增「mihomori-tui」章节 | 同上 |
| 1.36 | 全量验证（`go build ./... && go test ./...` + 连真实 server） | 待上述启用后执行；内核 start/stop 已由用户验证，无需重复 |

## 内核操作

| 条目 | 内容 | 说明 |
|------|------|------|
| Rollback / Recover | `internal/ctl/tui/tui.go` 的 `kernelOps` 中两条目已注释 | 危险逃生通道，用户决定先注释，后续再启用；恢复时取消注释 + 启用 `_TestRecoverConfirm`（kernel_tab_test.go 中已改名禁用） |

## Server 缺口（需决策）

| 条目 | 内容 | 说明 |
|------|------|------|
| activeId 端点 | server 的 `GET /api/control/profiles` 不返回当前激活的 profile id（`profile.Store.GetActiveID()` 无 HTTP 端点） | TUI 目前只能跟踪**会话内**执行的 activate/refresh-and-activate 操作来标记 active profile；重启后无法恢复显示。候选方案：server 增加只读端点（如 `GET /api/control/profiles/active` → `{"id":"..."}`），用户暂缓 |
