# mihomori-tui 后续实现计划（tui_plan_1）

> 从 `TUI_PLAN.md` 整理的**未完成事项**，按执行优先级与依赖关系重新组织（低风险 → 高依赖 → 收尾）。
> 已完成部分（Phase 0–2）见 `TUI_PLAN.md`；各项详细记录见 `TUI_DEFERRED.md`。

---

## 二、Phase 3 — 配置与维护

> Config 为 `3` Tab，承载 kernel 操作 + 下方配置区。

- [ ] **3.1** `client.go`:`GetConfig()/GetRuntimeConfig() (string,error)`、`PutSection(key,val,restart)`、`GeoUpdate()`、`Backup/Restore(webdav)`
  - 验证:编译通过
- [ ] **3.2** `client_test.go`:覆盖各端点
  - 验证:`go test ./internal/ctl -run TestConfig -v` 通过
- [ ] **3.3** `config.go`:只读 viewport 展示 active/runtime config(`3` Tab,与 kernel 操作同区)
  - 验证:编译通过
- [ ] **3.4** section 编辑:选择 key(如 mixed-port)→ 输入新值 → `PutSection`
  - 验证:编译通过
- [ ] **3.5** GEO 更新:`g` 触发,spinner 显示进度,完成提示写入文件
  - 验证:编译通过
- [ ] **3.6** WebDAV 备份/恢复表单(评估是否切 `tview` 做表单)
  - 验证:编译通过
- [ ] **3.7** 验证:配置改动持久化 + 重启生效;geo 文件落地;备份/恢复往返一致
  - 验证:对真实 server 验证

---

## 四、延后 / 待决策

### 5. Rollback / Recover（延后）
- 现状：`kernelOps` 中两条目已注释，`_TestRecoverConfirm` 禁用；**用户决定依旧延后**
- 启用时：取消注释 + 恢复确认流程（Recover 二次确认保留）；详见 `TUI_DEFERRED.md`

### 6. activeId 端点（已拍板，方案见 `tui_plan_2.md`）
- 缺口：server 的 `GET /api/control/profiles` 不返回当前激活 profile id（`profile.Store.GetActiveID()` 无 HTTP 暴露）
- 影响：profiles 表格高亮与状态栏回显**重启后无法恢复**（当前仅会话内跟踪）；Phase 3 的 config 展示（3.3）同样需要
- ~~候选方案：server 增加只读端点 `GET /api/control/profiles/active`~~ **已放弃**——官方 API 已在 list 响应中给激活项打 `active: true` 标（agent #2148），按官方契约同步
- 状态：已拍板，具体步骤见 `tui_plan_2.md`

---

## 五、发布收尾（最后执行）

### 7. 构建与发布（原 1.33–1.36）
- 1.33 `.goreleaser.yaml` 追加 `mihomori-tui` 构建项（验证:`goreleaser build --snapshot --clean`）
- 1.34 `Dockerfile` 构建 + COPY `mihomori-tui`（验证:`docker build`）
- 1.35 `README.md` / `README.zh-CN.md` 新增「mihomori-tui」章节
- 1.36 全量验证（`go build ./... && go test ./...` + 连真实 server；内核 start/stop 已由用户验证，无需重复）

---

## 备注

- `TUI_PLAN.md` 中 0.1/0.2、1.1–1.4、1.17、1.24、1.25 曾标注 `[ ]` 但**实际已完成**，已修正为 `[x]`；未完成部分仅剩本计划所列。
