package manage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"mihomori/internal/api"
	"mihomori/internal/server/profile"
)

func (m *Manager) selected(state State, p Plan) []Source {
	var out []Source
	for _, s := range state.Sources {
		if p.All || contains(p.Sources, s.ID) {
			out = append(out, s)
		}
	}
	return out
}
func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
func (m *Manager) base() (map[string]any, error) {
	path := filepath.Join(m.dir(), "base.yaml")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		for _, candidate := range []string{filepath.Join(m.opts.Home, "pool-template.yaml"), filepath.Join(m.opts.Home, "active.yaml")} {
			if b, err = os.ReadFile(candidate); err == nil {
				break
			}
		}
		if err != nil {
			b = []byte("rules: ['MATCH,🚀 节点选择']\n")
		}
		var doc map[string]any
		if err = yaml.Unmarshal(b, &doc); err != nil {
			return nil, err
		}
		for _, key := range []string{"proxies", "proxy-providers", "proxy-groups", "x-subscription-pool", "external-controller", "secret"} {
			delete(doc, key)
		}
		if len(doc) == 0 {
			doc = map[string]any{"rules": []any{"MATCH,🚀 节点选择"}}
		}
		b, err = yaml.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(path, b); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}
func (m *Manager) render(state State, p Plan) (string, error) {
	if err := validatePlan(p); err != nil {
		return "", err
	}
	doc, err := m.base()
	if err != nil {
		return "", err
	}
	selected := m.selected(state, p)
	providers := map[string]any{}
	for _, s := range selected {
		if _, exists := providers[s.Provider]; exists {
			return "", errors.New("提供者标识重复")
		}
		// The upstream URL and refresh policy live in manager state. These kernel
		// references stay stable through URL edits, refreshes and node exclusions.
		providers[s.Provider] = map[string]any{
			"type": "http", "url": fmt.Sprintf("http://127.0.0.1:%d/api/control/manage/providers/%s", m.opts.Port, s.ID),
			"path": "./manage/providers/" + s.ID + ".yaml", "proxy": "DIRECT", "interval": 0,
			"header":       map[string]any{"Authorization": []any{"Bearer " + m.opts.Token}},
			"health-check": map[string]any{"enable": true, "url": "https://www.gstatic.com/generate_204", "interval": 300, "timeout": 10000, "lazy": true},
		}
	}
	var groups []any
	choices := []any{}
	for _, g := range p.Groups {
		if _, collision := providers[g.Name]; collision {
			return "", errors.New("分组与订阅提供者名称冲突")
		}
		filter, err := groupFilter(g)
		if err != nil {
			return "", err
		}
		uses := []any{}
		for _, s := range selected {
			if len(g.Sources) == 0 || contains(g.Sources, s.ID) {
				uses = append(uses, s.Provider)
			}
		}
		item := map[string]any{"name": g.Name, "type": g.Type, "proxies": []any{"REJECT"}}
		if len(uses) > 0 {
			item["use"] = uses
		}
		if filter != "" {
			item["filter"] = filter
		}
		if g.Name == "台湾" {
			item["exclude-filter"] = "(?i)(台湾隧道|台灣隧道|taiwan[ _-]*tunnel|tw[ _-]*tunnel)"
		}
		if g.Type != "select" {
			testURL := g.URL
			if testURL == "" {
				testURL = "https://www.gstatic.com/generate_204"
			}
			interval := g.Interval
			if interval == 0 {
				interval = 300
			}
			timeout := g.Timeout
			if timeout == 0 {
				timeout = 10000
			}
			item["url"] = testURL
			item["interval"] = interval
			item["timeout"] = timeout
			item["lazy"] = g.Lazy
			item["tolerance"] = 50
		}
		groups = append(groups, item)
		choices = append(choices, g.Name)
	}
	groups = append(groups, map[string]any{"name": "🚀 节点选择", "type": "select", "proxies": choices}, map[string]any{"name": "🎯 本地直连", "type": "select", "proxies": []any{"DIRECT"}})
	doc["proxy-providers"] = providers
	doc["proxy-groups"] = groups
	delete(doc, "proxies")
	// Keep existing routing, DNS and router settings. Only subscription node
	// entities are forbidden in the runtime's top-level config.
	if _, ok := doc["rules"]; !ok {
		doc["rules"] = []any{"MATCH,🚀 节点选择"}
	}
	b, err := yaml.Marshal(doc)
	return string(b), err
}
func (m *Manager) normalized(content string) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", err
	}
	if nodes, ok := doc["proxies"].([]any); ok && len(nodes) > 0 {
		return "", errors.New("组合输出不允许内嵌订阅节点")
	}
	providers, _ := doc["proxy-providers"].(map[string]any)
	for _, raw := range providers {
		p, ok := raw.(map[string]any)
		if !ok {
			return "", errors.New("无效的提供者")
		}
		if _, inline := p["payload"]; inline {
			return "", errors.New("组合输出不允许内联节点")
		}
	}
	st := m.opts.Supervisor.State()
	doc["external-controller"] = st.ExternalController
	doc["secret"] = st.Secret
	if m.opts.MixedPort > 0 {
		doc["mixed-port"] = m.opts.MixedPort
	}
	if rules, ok := doc["rules"].([]any); ok {
		seen := map[string]bool{}
		unique := []any{}
		for _, raw := range rules {
			r, ok := raw.(string)
			if !ok {
				return "", errors.New("无效规则")
			}
			if !seen[r] {
				unique = append(unique, r)
				seen[r] = true
			}
		}
		doc["rules"] = unique
	}
	b, err := yaml.Marshal(doc)
	return string(b), err
}
func (m *Manager) validateRender(state State, p Plan) error {
	content, err := m.render(state, p)
	if err != nil {
		return err
	}
	// Rendering is side-effect free except first-run base initialization. The
	// final composed configuration is validated again before activation.
	if strings.TrimSpace(content) == "" {
		return errors.New("组合配置为空")
	}
	return nil
}

// TransformBase is the optional pure step before the original Store applies
// overlays/settings. It never calls the Store (its lock is held by the caller).
func (m *Manager) TransformBase(id, content string) (string, error) {
	state, _ := m.copyData()
	owned := false
	for _, source := range state.Sources {
		if source.ProfileID == id {
			owned = true
			break
		}
	}
	if !owned {
		return content, nil
	}
	i, err := planAt(state, state.Active)
	if err != nil {
		return "", err
	}
	return m.render(state, state.Plans[i])
}
func (m *Manager) candidate(anchor string) (string, error) {
	var content string
	var err error
	if anchor != "" {
		content, _, err = m.opts.Profiles.Compose(anchor)
	} else {
		state, _ := m.copyData()
		i, e := planAt(state, state.Active)
		if e != nil {
			return "", e
		}
		content, err = m.render(state, state.Plans[i])
	}
	if err != nil {
		return "", err
	}
	content, err = m.normalized(content)
	if err != nil {
		return "", err
	}
	return content, nil
}
func (m *Manager) validateRuntime(content string) error {
	f, err := os.CreateTemp(m.opts.Home, ".pool-candidate-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(content)
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	result := m.opts.Supervisor.Validate(f.Name())
	if !result.Valid {
		return fmt.Errorf("组合配置校验失败：%s", result.Message)
	}
	return nil
}
func (m *Manager) PrepareBoot() error {
	m.suppress.Store(true)
	defer m.suppress.Store(false)
	m.op.Lock()
	defer m.op.Unlock()
	old, _ := m.copyData()
	legacyActive := false
	active := m.opts.Profiles.GetActiveID()
	for _, p := range old.Plans {
		if p.ProfileID == active && active != "" {
			legacyActive = true
		}
	}
	if _, err := m.syncProfilesLocked(); err != nil {
		return err
	}
	if !legacyActive && !m.activeOwned() {
		return nil
	}
	anchor := m.anchor("")
	content, err := m.candidate(anchor)
	if err != nil {
		return err
	}
	if err = m.validateRuntime(content); err != nil {
		return err
	}
	if anchor != "" {
		if err = m.opts.Profiles.SetActive(anchor); err != nil {
			return err
		}
	}
	if err = atomicWrite(filepath.Join(m.opts.Home, "active.yaml"), []byte(content)); err != nil {
		return err
	}
	// Remove only profiles recorded by the rejected generated-profile design.
	for _, p := range old.Plans {
		if p.ProfileID == "" {
			continue
		}
		for _, meta := range m.opts.Profiles.List() {
			if meta.ID == p.ProfileID && meta.Type == profile.TypeLocal {
				body, e := m.opts.Profiles.Read(meta.ID)
				if e == nil && strings.Contains(body, "/api/control/manage/providers/") {
					if e = m.opts.Profiles.Delete(meta.ID); e != nil {
						return e
					}
				}
			}
		}
	}
	state, caches := m.copyData()
	for i := range state.Plans {
		state.Plans[i].ProfileID = ""
	}
	state.Version = 2
	return m.commit(state, caches)
}
func (m *Manager) anchor(preferred string) string {
	state, _ := m.copyData()
	active := m.opts.Profiles.GetActiveID()
	for _, id := range []string{preferred, active} {
		for _, s := range state.Sources {
			if s.ProfileID == id {
				return id
			}
		}
	}
	if len(state.Sources) > 0 {
		return state.Sources[0].ProfileID
	}
	return ""
}
func (m *Manager) applyActiveLocked(force bool) error { return m.applyProfileLocked("", force, nil) }
func (m *Manager) applyProfileLocked(preferred string, force bool, changed []string) error {
	if !force && !m.activeOwned() {
		return nil
	}
	anchor := m.anchor(preferred)
	content, err := m.candidate(anchor)
	if err != nil {
		return err
	}
	path := filepath.Join(m.opts.Home, "active.yaml")
	oldRuntime, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	oldID := m.opts.Profiles.GetActiveID()
	st := m.opts.Supervisor.State()
	if st.Status != api.StatusRunning {
		if err = m.validateRuntime(content); err != nil {
			return err
		}
		if anchor != "" {
			if err = m.opts.Profiles.SetActive(anchor); err != nil {
				return err
			}
		}
		if err = atomicWrite(path, []byte(content)); err != nil {
			return err
		}
		return errors.New("配置已保存，内核尚未运行")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if runtimeEquivalent(oldRuntime, []byte(content)) && oldID == anchor {
		if force {
			state, _ := m.copyData()
			changed = nil
			for _, s := range state.Sources {
				changed = append(changed, s.ID)
			}
		}
		for _, id := range changed {
			if err = m.hotUpdate(ctx, id); err != nil {
				return err
			}
		}
		return nil
	}
	if !force && !m.activeOwned() {
		return nil
	}
	if err = m.validateRuntime(content); err != nil {
		return err
	}
	if anchor != "" {
		if err = m.opts.Profiles.SetActive(anchor); err != nil {
			return err
		}
	}
	if err = atomicWrite(path, []byte(content)); err != nil {
		return err
	}
	_, err = m.core(ctx, http.MethodPut, "/configs?force=true", map[string]any{"path": path})
	if err == nil {
		state, _ := m.copyData()
		i, e := planAt(state, state.Active)
		if e == nil {
			err = m.verifyProviders(ctx, m.selected(state, state.Plans[i]))
		}
	}
	if err != nil {
		if oldID != "" {
			_ = m.opts.Profiles.SetActive(oldID)
		}
		_ = atomicWrite(path, oldRuntime)
		recovery, c := context.WithTimeout(context.Background(), 45*time.Second)
		defer c()
		_, restoreErr := m.core(recovery, http.MethodPut, "/configs?force=true", map[string]any{"path": path})
		if restoreErr != nil {
			return fmt.Errorf("重载失败且恢复状态未知：%v；%v", err, restoreErr)
		}
		return err
	}
	return nil
}

// WrapClash preserves the native dashboard's provider refresh operation. Only
// this module's owned providers fetch upstream first; everything else passes
// unchanged to the original reverse proxy. Core requests never re-enter here.
func (m *Manager) WrapClash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/api/clash/providers/proxies/"
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, prefix) && m.activeOwned() {
			state, _ := m.copyData()
			name := strings.TrimPrefix(r.URL.Path, prefix)
			for _, s := range state.Sources {
				if s.Provider == name {
					if _, err := m.opts.Profiles.Refresh(s.ProfileID); err != nil {
						http.Error(w, err.Error(), http.StatusBadGateway)
						return
					}
					m.mu.Lock()
					pending := m.lastError
					m.mu.Unlock()
					if pending != "" {
						http.Error(w, pending, http.StatusBadGateway)
						return
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func runtimeEquivalent(a, b []byte) bool {
	var left, right map[string]any
	if yaml.Unmarshal(a, &left) != nil || yaml.Unmarshal(b, &right) != nil {
		return false
	}
	x, _ := yaml.Marshal(left)
	y, _ := yaml.Marshal(right)
	return string(x) == string(y)
}
