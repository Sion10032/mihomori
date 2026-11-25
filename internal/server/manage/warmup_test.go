package manage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mihomori/internal/server/supervisor"
)

func TestStartupWarmupRetriesNativeChecksWithoutChangingConfiguration(t *testing.T) {
	m, profiles := fixture(t)
	var uplink, healthy atomic.Bool
	var relinquish, block atomic.Bool
	var checks atomic.Int32
	entered := make(chan struct{})
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/version":
			_, _ = w.Write([]byte(`{"version":"test"}`))
		case strings.HasSuffix(r.URL.Path, "/healthcheck"):
			if r.Method != http.MethodGet {
				t.Error("warmup must use native health checks only")
			}
			checks.Add(1)
			if block.Load() {
				close(entered)
				<-r.Context().Done()
				return
			}
			healthy.Store(uplink.Load())
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/providers/proxies":
			if relinquish.CompareAndSwap(true, false) {
				if err := profiles.SetActive(profiles.List()[0].ID); err != nil {
					t.Error(err)
				}
			}
			delay := 0
			if healthy.Load() {
				delay = 100
			}
			node := map[string]any{"name": "[A] Japan", "type": "Socks5", "alive": healthy.Load(), "history": []any{map[string]any{"delay": delay}}}
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{"A": map[string]any{"proxies": []any{node}}}})
		default:
			t.Errorf("unexpected warmup API %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer core.Close()
	bin := filepath.Join(t.TempDir(), "idle-core")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(supervisor.Options{BinaryPath: bin, HomeDir: m.opts.Home, ActiveConfigPath: filepath.Join(m.opts.Home, "active.yaml"), ExternalController: strings.TrimPrefix(core.URL, "http://"), Secret: "test", StartTimeout: time.Second})
	defer sup.Dispose()
	m.opts.Supervisor = sup
	if _, err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(m.opts.Home, "active.yaml"))
	pid := *sup.State().PID
	sources := []Source{{Provider: "A"}}
	if m.warmProviders(context.Background(), sources) {
		t.Fatal("a completed failed probe must not mark startup healthy")
	}
	uplink.Store(true)
	if !m.warmProviders(context.Background(), sources) {
		t.Fatal("native retry did not recover when uplink became ready")
	}
	if !m.warmProviders(context.Background(), sources) || checks.Load() != 2 {
		t.Fatal("healthy nodes should stop extra startup probes")
	}
	for range 2 {
		if m.warmProviders(context.Background(), []Source{{Provider: "A"}, {Provider: "B"}}) {
			t.Fatal("one healthy source must not suppress retries for another source")
		}
	}
	if checks.Load() != 4 {
		t.Fatal("a recovered source should not be probed again while another retries")
	}
	after, _ := os.ReadFile(filepath.Join(m.opts.Home, "active.yaml"))
	if string(before) != string(after) || *sup.State().PID != pid {
		t.Fatal("startup health checks changed runtime or process")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m.warmProviders(ctx, sources) {
		t.Fatal("cancelled warmup must terminate")
	}
	healthy.Store(false)
	relinquish.Store(true)
	beforeChecks := checks.Load()
	if m.warmProviders(context.Background(), sources) || checks.Load() != beforeChecks {
		t.Fatal("switching ordinary profiles must cancel pending managed probes")
	}
	block.Store(true)
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.warmProviders(ctx, sources); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled in-flight probe did not terminate")
	}
}
