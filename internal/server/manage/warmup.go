package manage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"mihomori/internal/api"
)

// A repeater's uplink can become ready after the core's first probes fail.
// Briefly retry native health checks after each core start. Selection, normal
// health-check intervals and lazy checks remain entirely owned by Mihomo.
func (m *Manager) warmStart(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var pid int
	var until time.Time
	complete := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			core := m.opts.Supervisor.State()
			if core.Status != api.StatusRunning || core.PID == nil || !m.activeOwned() {
				continue
			}
			if *core.PID != pid {
				pid, until, complete = *core.PID, time.Now().Add(2*time.Minute), false
			}
			if complete || time.Now().After(until) {
				continue
			}
			state, _ := m.copyData()
			i, err := planAt(state, state.Active)
			if err != nil {
				continue
			}
			probeCtx, cancel := context.WithDeadline(ctx, until)
			complete = m.warmProviders(probeCtx, m.selected(state, state.Plans[i]))
			cancel()
		}
	}
}

func (m *Manager) warmProviders(ctx context.Context, sources []Source) bool {
	if len(sources) == 0 {
		return true
	}
	core := m.opts.Supervisor.State()
	if core.PID == nil || ctx.Err() != nil {
		return false
	}
	pid, owner := *core.PID, m.opts.Profiles.GetActiveID()
	sameOwner := func() bool {
		current := m.opts.Supervisor.State()
		return ctx.Err() == nil && current.Status == api.StatusRunning && current.PID != nil && *current.PID == pid && m.opts.Profiles.GetActiveID() == owner
	}
	healthy := m.healthyProviders(ctx, sources)
	if !sameOwner() {
		return false
	}
	var probes sync.WaitGroup
	for _, source := range sources {
		if healthy[source.Provider] {
			continue
		}
		probes.Add(1)
		go func(name string) {
			defer probes.Done()
			if sameOwner() {
				_, _ = m.core(ctx, http.MethodGet, "/providers/proxies/"+url.PathEscape(name)+"/healthcheck", nil)
			}
		}(source.Provider)
	}
	probes.Wait()
	if !sameOwner() {
		return false
	}
	healthy = m.healthyProviders(ctx, sources)
	for _, source := range sources {
		if !healthy[source.Provider] {
			return false
		}
	}
	return true
}

func (m *Manager) healthyProviders(ctx context.Context, sources []Source) map[string]bool {
	healthy := map[string]bool{}
	body, err := m.core(ctx, http.MethodGet, "/providers/proxies", nil)
	if err != nil {
		return healthy
	}
	var response struct {
		Providers map[string]struct {
			Proxies []struct {
				Type    string `json:"type"`
				Alive   bool   `json:"alive"`
				History []struct {
					Delay int `json:"delay"`
				} `json:"history"`
			} `json:"proxies"`
		} `json:"providers"`
	}
	if json.Unmarshal(body, &response) != nil {
		return healthy
	}
	for _, source := range sources {
		provider, exists := response.Providers[source.Provider]
		realNodes := 0
		for _, node := range provider.Proxies {
			if node.Type != "Reject" {
				realNodes++
			}
			if node.Type != "Reject" && node.Alive && len(node.History) > 0 && node.History[len(node.History)-1].Delay > 0 {
				healthy[source.Provider] = true
			}
		}
		// An intentionally empty, reject-only source has nothing to probe.
		if exists && len(provider.Proxies) > 0 && realNodes == 0 {
			healthy[source.Provider] = true
		}
	}
	return healthy
}
