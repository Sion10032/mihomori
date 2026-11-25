package manage

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"mihomori/internal/server/clashproxy"
	"mihomori/internal/server/profile"
	"mihomori/internal/server/supervisor"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestNativeAutomaticGroupRecoversAfterLateUplink(t *testing.T) {
	binary := os.Getenv("MIHOMO_TEST_BINARY")
	if binary == "" {
		t.Skip("set MIHOMO_TEST_BINARY for real-kernel integration")
	}
	var uplink atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !uplink.Load() {
			c, _, _ := w.(http.Hijacker).Hijack()
			if c != nil {
				_ = c.Close()
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
		if r.Method == http.MethodConnect {
			c, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			_ = rw.Flush()
			if _, err = http.ReadRequest(rw.Reader); err == nil {
				_, _ = rw.WriteString("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
				_ = rw.Flush()
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	address := proxy.Listener.Addr().(*net.TCPAddr)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := yaml.Marshal(map[string]any{"proxies": []any{map[string]any{"name": "Japan", "type": "http", "server": "127.0.0.1", "port": address.Port}}})
		_, _ = w.Write(b)
	}))
	defer upstream.Close()
	home := t.TempDir()
	var manager *Manager
	profiles := wiredProfiles(home, &manager)
	if _, err := profiles.ImportFromURL(upstream.URL, "A"); err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(supervisor.Options{BinaryPath: binary, HomeDir: home, ActiveConfigPath: filepath.Join(home, "active.yaml"), ExternalController: fmt.Sprintf("127.0.0.1:%d", freePort(t)), Secret: "test", MixedPort: freePort(t), ValidateTimeout: 5 * time.Second})
	defer sup.Dispose()
	web := httptest.NewUnstartedServer(nil)
	defer web.Close()
	m, err := New(Options{Home: home, Port: web.Listener.Addr().(*net.TCPAddr).Port, Token: "test", Profiles: profiles, Supervisor: sup})
	if err != nil {
		t.Fatal(err)
	}
	state, caches := m.copyData()
	state.Plans[0].Groups = []Group{{Name: "auto", Type: "url-test", Lazy: true, Interval: 300}}
	if err = m.commit(state, caches); err != nil {
		t.Fatal(err)
	}
	manager = m
	if err = m.PrepareBoot(); err != nil {
		t.Fatal(err)
	}
	// Keep real native groups/providers, substituting an offline HTTP probe so
	// this regression is deterministic and never needs an Internet connection.
	path := filepath.Join(home, "active.yaml")
	config, _ := os.ReadFile(path)
	config = []byte(strings.ReplaceAll(string(config), "https://www.gstatic.com/generate_204", "http://probe.invalid/generate_204"))
	if err = os.WriteFile(path, config, 0o600); err != nil {
		t.Fatal(err)
	}
	web.Config.Handler = m.Handler()
	web.Start()
	if _, err = sup.Start(); err != nil {
		t.Fatal(err)
	}
	state, _ = m.copyData()
	if m.warmProviders(context.Background(), state.Sources) {
		t.Fatal("failed initial native probes must not count as healthy")
	}
	before, _ := os.ReadFile(path)
	pid := *sup.State().PID
	m.Start()
	defer m.Stop()
	uplink.Store(true)
	deadline := time.Now().Add(12 * time.Second)
	for {
		body, err := m.core(context.Background(), http.MethodGet, "/proxies/auto", nil)
		var group struct {
			Now string `json:"now"`
		}
		_ = json.Unmarshal(body, &group)
		if err == nil && group.Now == "[A] Japan" {
			break
		}
		if time.Now().After(deadline) {
			providers, _ := m.core(context.Background(), http.MethodGet, "/providers/proxies", nil)
			t.Fatalf("automatic group failed to recover after uplink: %s %v; providers=%s", body, err, providers)
		}
		time.Sleep(100 * time.Millisecond)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || *sup.State().PID != pid {
		t.Fatal("startup recovery rewrote config or restarted core")
	}
}
func echoSOCKS(t *testing.T) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[c] = true
			mu.Unlock()
			go func() {
				defer c.Close()
				defer func() { mu.Lock(); delete(connections, c); mu.Unlock() }()
				r := bufio.NewReader(c)
				head := make([]byte, 2)
				if _, err := io.ReadFull(r, head); err != nil {
					return
				}
				if _, err := io.CopyN(io.Discard, r, int64(head[1])); err != nil {
					return
				}
				_, _ = c.Write([]byte{5, 0})
				header := make([]byte, 4)
				if _, err := io.ReadFull(r, header); err != nil {
					return
				}
				length := 0
				switch header[3] {
				case 1:
					length = 4
				case 4:
					length = 16
				case 3:
					n, err := r.ReadByte()
					if err != nil {
						return
					}
					length = int(n)
				default:
					return
				}
				if _, err := io.CopyN(io.Discard, r, int64(length+2)); err != nil {
					return
				}
				_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				_, _ = io.Copy(c, r)
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for c := range connections {
			_ = c.Close()
		}
	}
}
func connectSOCKS(t *testing.T, port int) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	_, _ = c.Write([]byte{5, 1, 0})
	var reply [2]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil || reply[1] != 0 {
		t.Fatal("SOCKS handshake", err)
	}
	host := "echo.example"
	request := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
	request = binary.BigEndian.AppendUint16(request, 80)
	_, _ = c.Write(request)
	response := make([]byte, 10)
	if _, err := io.ReadFull(c, response); err != nil || response[1] != 0 {
		t.Fatal("SOCKS connect", err)
	}
	return c
}
func TestNativeProviderHotUpdatesKeepConnectionAndProcess(t *testing.T) {
	binary := os.Getenv("MIHOMO_TEST_BINARY")
	if binary == "" {
		t.Skip("set MIHOMO_TEST_BINARY for real-kernel integration")
	}
	port, closeEcho := echoSOCKS(t)
	defer closeEcho()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodes := []any{map[string]any{"name": "日本", "type": "socks5", "server": "127.0.0.1", "port": port}, map[string]any{"name": "新加坡", "type": "socks5", "server": "127.0.0.1", "port": port}}
		b, _ := yaml.Marshal(map[string]any{"proxies": nodes})
		_, _ = w.Write(b)
	}))
	defer upstream.Close()
	home := t.TempDir()
	corePort, mixedPort := freePort(t), freePort(t)
	var manager *Manager
	profiles := wiredProfiles(home, &manager)
	sup := supervisor.New(supervisor.Options{BinaryPath: binary, HomeDir: home, ActiveConfigPath: filepath.Join(home, "active.yaml"), ExternalController: fmt.Sprintf("127.0.0.1:%d", corePort), Secret: "test", MixedPort: mixedPort, ValidateTimeout: 5 * time.Second})
	defer sup.Dispose()
	web := httptest.NewUnstartedServer(nil)
	defer web.Close()
	webPort := web.Listener.Addr().(*net.TCPAddr).Port
	m, err := New(Options{Home: home, Port: webPort, Token: "test", MixedPort: mixedPort, Profiles: profiles, Supervisor: sup})
	if err != nil {
		t.Fatal(err)
	}
	manager = m
	if err = m.PrepareBoot(); err != nil {
		t.Fatal(err)
	}
	proxy := clashproxy.New(func() (string, string, bool) {
		s := sup.State()
		return s.ExternalController, s.Secret, s.Status == "running"
	})
	mux := http.NewServeMux()
	mux.Handle("/api/control/manage/", m.Handler())
	mux.Handle("/api/clash/", m.WrapClash(proxy))
	web.Config.Handler = mux
	web.Start()
	if _, err = sup.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err = profiles.ImportFromURL(upstream.URL+"/a", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err = profiles.ImportFromURL(upstream.URL+"/b", "B"); err != nil {
		t.Fatal(err)
	}
	var result Result
	state, _ := m.copyData()
	p := state.Plans[0]
	p.Groups = []Group{{Name: "manual", Type: "select", Lazy: true}}
	if result, err = m.SavePlan(p); err != nil || !result.Applied {
		t.Fatal(result, err)
	}
	for group, node := range map[string]string{"🚀 节点选择": "manual", "manual": "[A] 日本"} {
		if _, err = m.core(context.Background(), http.MethodPut, "/proxies/"+url.PathEscape(group), map[string]any{"name": node}); err != nil {
			t.Fatal(err)
		}
	}
	connection := connectSOCKS(t, mixedPort)
	defer connection.Close()
	checkEcho := func(text string) {
		t.Helper()
		if _, err := connection.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		out := make([]byte, len(text))
		if _, err := io.ReadFull(connection, out); err != nil || string(out) != text {
			t.Fatalf("existing connection broken: %q %v", out, err)
		}
	}
	checkEcho("before")
	initialPID := *sup.State().PID
	before, _ := os.ReadFile(filepath.Join(home, "active.yaml"))
	state, _ = m.copyData()
	a := state.Sources[0]
	a.Interval = 60
	if _, err = profiles.Update(a.ProfileID, profile.UpdateInput{UpdateInterval: &a.Interval}); err != nil {
		t.Fatal(err)
	}
	checkEcho("interval")
	a.URL = upstream.URL + "/changed"
	if _, err = profiles.Update(a.ProfileID, profile.UpdateInput{URL: &a.URL}); err != nil {
		t.Fatal(err)
	}
	checkEcho("url")
	excluded := true
	if result, err = m.EditNode(context.Background(), a.ID, nodeID(a.ID, "日本"), &excluded, nil); err != nil || !result.Applied {
		t.Fatal(result, err)
	}
	checkEcho("exclude")
	after, _ := os.ReadFile(filepath.Join(home, "active.yaml"))
	if string(before) != string(after) || *sup.State().PID != initialPID {
		t.Fatal("hot operation rewrote runtime or restarted core")
	}
	body, err := m.core(context.Background(), http.MethodGet, "/providers/proxies/A", nil)
	if err != nil {
		t.Fatal(err)
	}
	var provider struct {
		Vehicle string `json:"vehicleType"`
		Proxies []struct {
			Name string `json:"name"`
		} `json:"proxies"`
	}
	if err = json.Unmarshal(body, &provider); err != nil {
		t.Fatal(err)
	}
	if provider.Vehicle != "HTTP" || len(provider.Proxies) != 1 || !strings.Contains(provider.Proxies[0].Name, "新加坡") {
		t.Fatalf("native provider did not reflect exclusion: %s", body)
	}
	checkEcho("after")
	if result, err = m.Activate(state.Active); err != nil || !result.Applied {
		t.Fatal(result, err)
	}
	checkEcho("reapply")
	if *sup.State().PID != initialPID {
		t.Fatal("reapplying unchanged plan restarted core")
	}
}
