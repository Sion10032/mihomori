package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"mihomori/internal/api"
	"mihomori/internal/server/profile"
	"mihomori/internal/server/supervisor"
)

// Original profiles own all source metadata, subscription bytes and schedules.
// These derived bindings retain existing source IDs/exclusions during migration.
func (m *Manager) syncProfilesLocked() ([]string, error) {
	state, caches := m.copyData()
	oldSources := state.Sources
	state.Sources = []Source{}
	changed := []string{}
	used := map[string]bool{}
	labels := map[string]bool{"DIRECT": true, "REJECT": true, "GLOBAL": true, "🚀 节点选择": true, "🎯 本地直连": true}
	for _, p := range state.Plans {
		for _, g := range p.Groups {
			labels[g.Name] = true
		}
	}
	for _, meta := range m.opts.Profiles.List() {
		if meta.Type != profile.TypeRemote || meta.URL == "" || (meta.Enabled != nil && !*meta.Enabled) {
			continue
		}
		body, err := m.opts.Profiles.Read(meta.ID)
		if err != nil {
			return nil, err
		}
		nodes, err := subscriptionNodes(body)
		if err != nil {
			return nil, fmt.Errorf("订阅 %s：%w", meta.Name, err)
		}
		var previous Source
		for _, source := range oldSources {
			if !used[source.ID] && (source.ProfileID == meta.ID || (source.ProfileID == "" && source.URL == meta.URL)) {
				previous = source
				break
			}
		}
		source := previous
		if source.ID == "" {
			source.ID = identifier("profile/" + meta.ID)
		}
		source.ProfileID, source.URL, source.UserAgent = meta.ID, meta.URL, meta.UserAgent
		source.Name, source.UpdatedAt = meta.Name, meta.UpdatedAt
		if source.Name == "" {
			source.Name = "订阅 " + source.ID[:4]
		}
		source.Interval = 0
		if meta.UpdateInterval != nil {
			source.Interval = *meta.UpdateInterval
		}
		if source.Provider == "" {
			source.Provider = source.Name
		}
		if labels[source.Provider] {
			source.Provider = source.Name + " [" + source.ID[:4] + "]"
		}
		labels[source.Provider], used[source.ID] = true, true
		if previous.ID == "" || caches[source.ID].Raw != body || previous.Name != source.Name || previous.URL != source.URL || previous.UserAgent != source.UserAgent || (previous.UpdatedAt != source.UpdatedAt && previous.Interval == source.Interval) {
			changed = append(changed, source.ID)
		}
		state.Sources = append(state.Sources, source)
		caches[source.ID] = sourceCache{Raw: body, Nodes: nodes}
	}
	for id := range caches {
		if !used[id] {
			delete(caches, id)
		}
	}
	before, _ := json.Marshal(m.state)
	after, _ := json.Marshal(state)
	if !bytes.Equal(before, after) || len(changed) > 0 {
		if err := m.commit(state, caches); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

func (m *Manager) SourceChanged(_ string) {
	if m.suppress.Load() {
		return
	}
	m.op.Lock()
	defer m.op.Unlock()
	owned := m.activeOwned()
	changed, err := m.syncProfilesLocked()
	if err == nil {
		err = m.applyProfileLocked("", owned && !m.activeOwned(), changed)
	}
	m.setError(err)
}

// ApplyProfile only takes over imported remote sources. Ordinary local profiles
// retain the original activate/validate/rollback/restart implementation.
func (m *Manager) ApplyProfile(id string, original func() (api.KernelState, error)) (api.KernelState, error) {
	m.op.Lock()
	defer m.op.Unlock()
	state, _ := m.copyData()
	for _, source := range state.Sources {
		if source.ProfileID == id {
			err := m.applyProfileLocked(id, true, nil)
			m.setError(err)
			return m.opts.Supervisor.State(), err
		}
	}
	return original()
}

func (m *Manager) AppliedRefresh(string) bool { return m.activeOwned() }

func (m *Manager) ValidateProfile(id string, draft profile.UpdateInput) (supervisor.ValidationResult, bool) {
	state, _ := m.copyData()
	for _, source := range state.Sources {
		if source.ProfileID != id {
			continue
		}
		body, err := m.opts.Profiles.PreviewUpdate(id, draft)
		if err == nil {
			err = ValidateSource(m.opts.Supervisor, m.opts.Home, body)
		}
		result := supervisor.ValidationResult{Valid: err == nil}
		if err != nil {
			result.Message = err.Error()
		}
		return result, true
	}
	return supervisor.ValidationResult{}, false
}

// Validation happens before the original Store commits a changed remote
// source; a failed fetch/parse never replaces its last successful source bytes.
func ValidateSource(sup *supervisor.Supervisor, home, body string) error {
	nodes, err := subscriptionNodes(body)
	if err != nil {
		return err
	}
	payload, err := providerBody(Source{ID: "validation", Name: "validation"}, sourceCache{Nodes: nodes})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(home, ".subscription-candidate-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(append(payload, []byte("\nrules: ['MATCH,REJECT']\n")...))
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if result := sup.Validate(f.Name()); !result.Valid {
		return fmt.Errorf("订阅校验失败：%s", result.Message)
	}
	return nil
}

// Decorate existing small JSON responses only. All operations still go through
// the original control routes; no second CRUD API or page is introduced.
func (m *Manager) WrapControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decorate := r.URL.Path == "/api/control/info" || strings.HasPrefix(r.URL.Path, "/api/control/profiles")
		if !decorate {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		body := rec.Body.Bytes()
		if rec.Code < 300 && r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/api/control/info":
				var value map[string]any
				if json.Unmarshal(body, &value) == nil {
					features, _ := value["features"].([]any)
					value["features"] = append(features, "subscription-pool")
					body, _ = json.Marshal(value)
				}
			case "/api/control/profiles":
				var list []map[string]any
				if json.Unmarshal(body, &list) == nil {
					state, _ := m.copyData()
					active := m.opts.Profiles.GetActiveID()
					for _, item := range list {
						item["active"] = item["id"] == active
						for _, source := range state.Sources {
							if item["id"] == source.ProfileID {
								item["pooled"] = true
							}
						}
					}
					body, _ = json.Marshal(list)
				}
			}
		}
		for key, values := range rec.Header() {
			w.Header()[key] = values
		}
		if rec.Code < 300 && r.Method != http.MethodGet {
			m.mu.Lock()
			pending := m.lastError != ""
			m.mu.Unlock()
			if pending {
				w.Header().Set("X-Mihomori-Pool-Pending", "true")
			}
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	})
}

func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/control/manage/providers/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, err := m.Feed(context.Background(), r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /api/control/pool", func(w http.ResponseWriter, r *http.Request) {
		state, _ := m.copyData()
		m.mu.Lock()
		last := m.lastError
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "active": m.activeOwned(), "sourceCount": len(state.Sources), "lastError": last})
	})
	return mux
}
