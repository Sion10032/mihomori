package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
	"mihomori/internal/api"
	"mihomori/internal/server/profile"
	"mihomori/internal/server/supervisor"
)

type Options struct {
	Home       string
	Port       int
	Token      string
	MixedPort  int
	Profiles   *profile.Store
	Supervisor *supervisor.Supervisor
}
type persisted struct {
	State  State                  `json:"state"`
	Caches map[string]sourceCache `json:"caches"`
}
type Manager struct {
	opts      Options
	op        sync.Mutex // serializes mutations, network I/O never holds mu
	mu        sync.Mutex
	state     State
	caches    map[string]sourceCache
	payloads  map[string][]byte
	lastError string
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
	cancel    context.CancelFunc
	suppress  atomic.Bool
}
type Result struct {
	Saved   bool   `json:"saved"`
	Applied bool   `json:"applied"`
	Warning string `json:"warning,omitempty"`
}

func New(opts Options) (*Manager, error) {
	home, err := filepath.Abs(opts.Home)
	if err != nil {
		return nil, err
	}
	opts.Home = home
	m := &Manager{opts: opts, caches: map[string]sourceCache{}, payloads: map[string][]byte{}, stop: make(chan struct{}), done: make(chan struct{})}
	b, err := os.ReadFile(m.statePath())
	if err == nil {
		var disk persisted
		if err = json.Unmarshal(b, &disk); err != nil {
			return nil, fmt.Errorf("read manager state: %w", err)
		}
		m.state = disk.State
		m.caches = disk.Caches
		if m.caches == nil {
			m.caches = map[string]sourceCache{}
		}
		if m.state.Version != 1 && m.state.Version != 2 {
			return nil, errors.New("unsupported manager state version")
		}
		seen := map[string]bool{}
		for _, source := range m.state.Sources {
			if len(source.ID) != 16 || strings.Trim(source.ID, "0123456789abcdef") != "" || source.Provider == "" || seen[source.Provider] {
				return nil, errors.New("invalid manager source identity")
			}
			seen[source.Provider] = true
			cache, ok := m.caches[source.ID]
			if !ok {
				return nil, errors.New("manager source cache missing")
			}
			encoded, encodeErr := yaml.Marshal(map[string]any{"proxies": cache.Nodes})
			if encodeErr != nil {
				return nil, encodeErr
			}
			if _, err := subscriptionNodes(string(encoded)); err != nil {
				return nil, fmt.Errorf("invalid source cache: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		if err = m.initialize(); err != nil {
			return nil, err
		}
	}
	if err = m.publish(); err != nil {
		return nil, err
	}
	return m, nil
}
func (m *Manager) dir() string                { return filepath.Join(m.opts.Home, "manage") }
func (m *Manager) statePath() string          { return filepath.Join(m.dir(), "state.json") }
func (m *Manager) cachePath(id string) string { return filepath.Join(m.dir(), "providers", id+".yaml") }
func (m *Manager) initialize() error {
	// One-time migration reads existing data through original public APIs. The
	// original profiles and their bytes are never rewritten or deleted.
	m.state = State{Version: 1, Sources: []Source{}, Plans: []Plan{}}
	for _, meta := range m.opts.Profiles.List() {
		if meta.Type != profile.TypeRemote || meta.URL == "" {
			continue
		}
		body, err := m.opts.Profiles.Read(meta.ID)
		if err != nil {
			continue
		}
		nodes, err := subscriptionNodes(body)
		if err != nil {
			continue
		}
		interval := 1440
		if meta.UpdateInterval != nil {
			interval = *meta.UpdateInterval
		}
		s := Source{ID: newID(), Name: meta.Name, Provider: meta.Name, URL: meta.URL, UserAgent: meta.UserAgent, Interval: interval, UpdatedAt: meta.UpdatedAt}
		if s.Name == "" {
			s.Name = "订阅"
		}
		if s.Provider == "" {
			s.Provider = s.Name
		}
		for _, other := range m.state.Sources {
			if other.Provider == s.Provider {
				s.Provider += " " + s.ID[:4]
			}
		}
		m.state.Sources = append(m.state.Sources, s)
		m.caches[s.ID] = sourceCache{Raw: body, Nodes: nodes}
	}
	plans := []Plan{{ID: newID(), Name: "融合", All: true, Groups: defaultGroups()}}
	for _, s := range m.state.Sources {
		if len(plans) >= 3 {
			break
		}
		plans = append(plans, Plan{ID: newID(), Name: s.Name, Groups: defaultGroups(), Sources: []string{s.ID}})
	}
	for len(plans) < 3 {
		plans = append(plans, Plan{ID: newID(), Name: fmt.Sprintf("组合 %d", len(plans)+1), All: true, Groups: defaultGroups()})
	}
	m.state.Plans = plans
	m.state.Active = plans[0].ID
	return m.persist(m.state, m.caches)
}
func (m *Manager) persist(state State, caches map[string]sourceCache) error {
	return writeJSONFile(m.statePath(), persisted{State: state, Caches: caches})
}
func (m *Manager) copyData() (State, map[string]sourceCache) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := cloneState(m.state)
	caches := make(map[string]sourceCache, len(m.caches))
	for id, c := range m.caches {
		caches[id] = c
	}
	return state, caches
}
func sourceAt(state State, id string) (int, error) {
	for i, s := range state.Sources {
		if s.ID == id {
			return i, nil
		}
	}
	return -1, errors.New("订阅不存在")
}
func planAt(state State, id string) (int, error) {
	for i, p := range state.Plans {
		if p.ID == id {
			return i, nil
		}
	}
	return -1, errors.New("组合不存在")
}
func displayName(s Source, node map[string]any) string {
	name := node["name"].(string)
	out := "[" + s.Name + "] " + name
	for _, tag := range cleanTags(s.Tags[nodeID(s.ID, name)]) {
		out += " [标签:" + tag + "]"
	}
	return out
}

var metadataNames = regexp.MustCompile(`剩余|到期|重置|官网|实时负载|套餐|流量|----------------`)

func providerBody(s Source, c sourceCache) ([]byte, error) {
	var nodes []any
	renames := map[string]string{}
	for _, n := range c.Nodes {
		original := n["name"].(string)
		id := nodeID(s.ID, original)
		if s.Excluded[id] || metadataNames.MatchString(original) {
			continue
		}
		renames[original] = displayName(s, n)
	}
	for _, n := range c.Nodes {
		name := n["name"].(string)
		if _, ok := renames[name]; !ok {
			continue
		}
		// Clone maps before applying derived names. Cached subscriptions stay raw.
		b, _ := json.Marshal(n)
		var item map[string]any
		_ = json.Unmarshal(b, &item)
		item["name"] = renames[name]
		if dialer, ok := item["dialer-proxy"].(string); ok {
			if mapped, exists := renames[dialer]; exists {
				item["dialer-proxy"] = mapped
			}
		}
		nodes = append(nodes, item)
	}
	if len(nodes) == 0 {
		nodes = []any{map[string]any{"name": "[" + s.Name + "] 无可用节点", "type": "reject"}}
	}
	return yaml.Marshal(map[string]any{"proxies": nodes})
}
func (m *Manager) publish() error {
	state, caches := m.copyData()
	payloads := map[string][]byte{}
	for _, s := range state.Sources {
		b, err := providerBody(s, caches[s.ID])
		if err != nil {
			return err
		}
		if err = atomicWrite(m.cachePath(s.ID), b); err != nil {
			return err
		}
		payloads[s.ID] = b
	}
	m.mu.Lock()
	m.payloads = payloads
	m.mu.Unlock()
	return nil
}
func (m *Manager) commit(state State, caches map[string]sourceCache) error {
	payloads := map[string][]byte{}
	for _, source := range state.Sources {
		body, err := providerBody(source, caches[source.ID])
		if err != nil {
			return err
		}
		payloads[source.ID] = body
	}
	m.mu.Lock()
	previous := m.payloads
	m.mu.Unlock()
	restore := func(cause error) error {
		for id, body := range previous {
			if err := atomicWrite(m.cachePath(id), body); err != nil {
				return fmt.Errorf("保存失败且缓存恢复失败：%v；%w", cause, err)
			}
		}
		return cause
	}
	for id, body := range payloads {
		if bytes.Equal(previous[id], body) {
			continue
		}
		if err := atomicWrite(m.cachePath(id), body); err != nil {
			return restore(err)
		}
	}
	if err := m.persist(state, caches); err != nil {
		return restore(err)
	}
	m.mu.Lock()
	m.state = state
	m.caches = caches
	m.payloads = payloads
	m.mu.Unlock()
	return nil
}
func (m *Manager) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = ""
	if err != nil {
		m.lastError = err.Error()
	}
}
func (m *Manager) finish(err error) Result {
	m.setError(err)
	r := Result{Saved: true, Applied: err == nil}
	if err != nil {
		r.Warning = err.Error()
	}
	return r
}
func (m *Manager) EditNode(ctx context.Context, source, id string, excluded *bool, tags *[]string) (Result, error) {
	m.op.Lock()
	defer m.op.Unlock()
	state, caches := m.copyData()
	i, err := sourceAt(state, source)
	if err != nil {
		return Result{}, err
	}
	found := false
	for _, n := range caches[source].Nodes {
		if nodeID(source, n["name"].(string)) == id {
			found = true
			break
		}
	}
	if !found {
		return Result{}, errors.New("节点不存在")
	}
	s := &state.Sources[i]
	if s.Excluded == nil {
		s.Excluded = map[string]bool{}
	}
	if s.Tags == nil {
		s.Tags = map[string][]string{}
	}
	if excluded != nil {
		s.Excluded[id] = *excluded
	}
	if tags != nil {
		s.Tags[id] = cleanTags(*tags)
	}
	if err = m.commit(state, caches); err != nil {
		return Result{}, err
	}
	return m.finish(m.hotUpdate(ctx, source)), nil
}
func (m *Manager) SavePlan(p Plan) (Result, error) {
	if err := validatePlan(p); err != nil {
		return Result{}, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	state, caches := m.copyData()
	if p.ID == "" {
		p.ID = newID()
		p.ProfileID = ""
		state.Plans = append(state.Plans, p)
	} else {
		i, err := planAt(state, p.ID)
		if err != nil {
			return Result{}, err
		}
		p.ProfileID = state.Plans[i].ProfileID
		state.Plans[i] = p
	}
	if err := m.validateRender(state, p); err != nil {
		return Result{}, err
	}
	if err := m.commit(state, caches); err != nil {
		return Result{}, err
	}
	return m.finish(m.applyActiveLocked(false)), nil
}
func (m *Manager) Activate(id string) (Result, error) {
	m.op.Lock()
	defer m.op.Unlock()
	state, caches := m.copyData()
	if _, err := planAt(state, id); err != nil {
		return Result{}, err
	}
	old := state.Active
	state.Active = id
	if err := m.commit(state, caches); err != nil {
		return Result{}, err
	}
	err := m.applyActiveLocked(true)
	if err != nil {
		rollback, _ := m.copyData()
		rollback.Active = old
		_ = m.commit(rollback, caches)
	}
	return m.finish(err), nil
}
func (m *Manager) core(ctx context.Context, method, path string, body any) ([]byte, error) {
	st := m.opts.Supervisor.State()
	if st.Status != api.StatusRunning {
		return nil, errors.New("内核尚未运行，设置已保存")
	}
	host := st.ExternalController
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return nil, err
	}
	if h == "0.0.0.0" || h == "::" || h == "" {
		h = "127.0.0.1"
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+net.JoinHostPort(h, p)+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+st.Secret)
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 40 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("内核请求失败 HTTP %d", resp.StatusCode)
	}
	return b, nil
}
func (m *Manager) activeOwned() bool {
	active := m.opts.Profiles.GetActiveID()
	if active == "" {
		return true
	}
	state, _ := m.copyData()
	for _, s := range state.Sources {
		if s.ProfileID == active {
			return true
		}
	}
	return false
}
func (m *Manager) hotUpdate(ctx context.Context, id string) error {
	if !m.activeOwned() {
		return nil
	}
	state, _ := m.copyData()
	i, _ := planAt(state, state.Active)
	selected := m.selected(state, state.Plans[i])
	var source *Source
	for j := range selected {
		if selected[j].ID == id {
			source = &selected[j]
			break
		}
	}
	if source == nil {
		return nil
	}
	_, err := m.core(ctx, http.MethodPut, "/providers/proxies/"+url.PathEscape(source.Provider), nil)
	if err != nil {
		return err
	}
	return m.verifyProviders(ctx, []Source{*source})
}

// A native config reload may return 204 before provider initialization finishes.
// Check the actual core state rather than treating an HTTP acknowledgement as
// proof that its providers have loaded.
func (m *Manager) verifyProviders(ctx context.Context, sources []Source) error {
	if len(sources) == 0 {
		return nil
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		body, err := m.core(ctx, http.MethodGet, "/providers/proxies", nil)
		if err == nil {
			var response struct {
				Providers map[string]struct {
					Vehicle string            `json:"vehicleType"`
					Proxies []json.RawMessage `json:"proxies"`
				} `json:"providers"`
			}
			if json.Unmarshal(body, &response) == nil {
				ready := true
				for _, source := range sources {
					p, ok := response.Providers[source.Provider]
					if !ok || p.Vehicle != "HTTP" || len(p.Proxies) == 0 {
						ready = false
						break
					}
				}
				if ready {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return errors.New("配置已接收，但提供者尚未就绪")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (m *Manager) Feed(ctx context.Context, id string) ([]byte, error) {

	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.payloads[id]
	if !ok {
		return nil, errors.New("订阅不存在")
	}
	return append([]byte(nil), b...), nil
}
func (m *Manager) Snapshot() map[string]any {
	state, caches := m.copyData()
	var nodes []Node
	for _, s := range state.Sources {
		for _, n := range caches[s.ID].Nodes {
			name := n["name"].(string)
			if metadataNames.MatchString(name) {
				continue
			}
			id := nodeID(s.ID, name)
			nodes = append(nodes, Node{ID: id, Source: s.ID, Name: name, DisplayName: displayName(s, n), Type: fmt.Sprint(n["type"]), Excluded: s.Excluded[id], Tags: s.Tags[id]})
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].DisplayName < nodes[j].DisplayName })
	m.mu.Lock()
	last := m.lastError
	m.mu.Unlock()
	return map[string]any{"state": state, "nodes": nodes, "active": m.activeOwned(), "lastError": last, "kernel": m.opts.Supervisor.State()}
}
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	go func() { defer close(m.done); m.warmStart(ctx) }()
}
func (m *Manager) Stop() {
	m.once.Do(func() {
		m.mu.Lock()
		cancel := m.cancel
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		close(m.stop)
	})
	<-m.done
}
