package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mihomori/internal/server/profile"
	"mihomori/internal/server/supervisor"
)

// contractRouter builds a minimal Router with real ProfileStore + stub Supervisor.
func contractRouter(t *testing.T) Router {
	t.Helper()
	dir := t.TempDir()
	store := profile.New(profile.Options{
		Dir:              filepath.Join(dir, "profiles"),
		ActiveConfigPath: filepath.Join(dir, "active.yaml"),
	})
	sup := supervisor.New(supervisor.Options{
		BinaryPath:       "/usr/bin/false",
		HomeDir:          dir,
		ActiveConfigPath: filepath.Join(dir, "active.yaml"),
		StartTimeout:     time.Second,
		StopTimeout:      time.Second,
		ValidateTimeout:  time.Second,
	})
	return Router{
		Supervisor:       sup,
		Profiles:         store,
		Token:            "",
		MihomoBin:        "/usr/bin/false",
		MihomoVersion:    "test",
		HomeDir:          dir,
		ActiveConfigPath: filepath.Join(dir, "active.yaml"),
	}
}

// endpoint records one contract test case.
type endpoint struct {
	method  string
	path    string
	body    string
	wantMin int // minimum acceptable status (inclusive); 0 = skip range check
	wantMax int // maximum acceptable status (inclusive)
	desc    string
}

func TestContractEndpoints(t *testing.T) {
	r := contractRouter(t)
	handler := New(r)

	// Create a test profile so GET /profiles/{id} works.
	createResp := doRequest(handler, "POST", "/api/control/profiles",
		`{"name":"c1","content":"a: 1"}`)
	var created map[string]any
	jsonDecode(t, createResp.Body, &created)
	profileID, _ := created["id"].(string)
	if profileID == "" {
		t.Fatalf("failed to create test profile: %v", created)
	}

	endpoints := []endpoint{
		// Health / Info (public, no auth)
		{"GET", "/api/control/health", "", 200, 200, "health check"},
		{"GET", "/api/control/info", "", 200, 200, "server info"},

		// Kernel lifecycle
		{"GET", "/api/control/kernel/status", "", 200, 200, "kernel status"},
		{"POST", "/api/control/kernel/start", "", 200, 503, "start (no real mihomo)"},
		{"POST", "/api/control/kernel/stop", "", 200, 503, "stop"},
		{"POST", "/api/control/kernel/restart", "", 200, 503, "restart"},
		{"POST", "/api/control/kernel/rollback", "", 404, 503, "rollback (no backup)"},
		{"POST", "/api/control/kernel/recover", "", 200, 503, "recover"},

		// Profiles CRUD
		{"GET", "/api/control/profiles", "", 200, 200, "list profiles"},
		{"POST", "/api/control/profiles", `{"name":"test","content":"a: 1"}`, 200, 201, "create profile"},
		{"GET", "/api/control/profiles/" + profileID, "", 200, 200, "get profile"},
		{"PUT", "/api/control/profiles/" + profileID, `{"name":"updated"}`, 200, 200, "update profile"},
		{"POST", "/api/control/profiles/" + profileID + "/duplicate", "", 200, 201, "duplicate profile"},
		{"POST", "/api/control/profiles/" + profileID + "/activate", "", 200, 400, "activate profile"},
		{"POST", "/api/control/profiles/" + profileID + "/validate", "", 200, 503, "validate profile"},
		{"GET", "/api/control/profiles/nonexistent-id", "", 404, 404, "get nonexistent profile"},
		{"DELETE", "/api/control/profiles/nonexistent-id", "", 404, 404, "delete nonexistent profile"},

		// Config
		{"GET", "/api/control/config", "", 200, 200, "get active config"},
		{"GET", "/api/control/config/runtime", "", 200, 200, "get runtime config"},
		{"GET", "/api/control/config/section?key=rules", "", 200, 204, "get config section"},

		// 404 for unknown routes
		{"GET", "/api/control/nonexistent", "", 404, 404, "unknown route"},
		{"POST", "/api/control/unknown/action", "", 404, 404, "unknown action"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			resp := doRequest(handler, ep.method, ep.path, ep.body)
			status := resp.Code

			if ep.wantMin > 0 && ep.wantMax > 0 {
				if status < ep.wantMin || status > ep.wantMax {
					t.Errorf("status %d not in [%d, %d] — %s", status, ep.wantMin, ep.wantMax, ep.desc)
				}
			} else {
				if status >= 500 && status != 503 {
					t.Errorf("unexpected %d — %s", status, ep.desc)
				}
			}

			ct := resp.Header().Get("Content-Type")
			if !strings.Contains(ct, "text/event-stream") && !strings.Contains(ct, "text/yaml") {
				if !strings.Contains(ct, "json") && !strings.Contains(ct, "text/plain") {
					t.Errorf("Content-Type %q should be JSON — %s", ct, ep.desc)
				}
			}
		})
	}
}

func TestContractInfoFeatures(t *testing.T) {
	r := contractRouter(t)
	handler := New(r)

	resp := doRequest(handler, "GET", "/api/control/info", "")
	var info map[string]any
	jsonDecode(t, resp.Body, &info)

	features, ok := info["features"].([]any)
	if !ok {
		t.Fatalf("features not an array: %v", info["features"])
	}

	mustHave := []string{"profiles", "kernel-control", "logs-sse"}
	for _, f := range mustHave {
		found := false
		for _, feat := range features {
			if feat == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("feature %q missing from info.features", f)
		}
	}

	mustNotHave := []string{"visual-config-editor", "system-proxy", "kernel-version", "tun"}
	for _, f := range mustNotHave {
		for _, feat := range features {
			if feat == f {
				t.Errorf("feature %q should NOT be in info.features (cut)", f)
			}
		}
	}
}

func TestContractProfileCreateShape(t *testing.T) {
	r := contractRouter(t)
	handler := New(r)

	resp := doRequest(handler, "POST", "/api/control/profiles",
		`{"name":"shape-test","content":"x: 1","type":"local"}`)
	if resp.Code != 200 {
		t.Fatalf("create status = %d, want 200", resp.Code)
	}

	var m map[string]any
	jsonDecode(t, resp.Body, &m)

	for _, field := range []string{"id", "name", "type", "updatedAt"} {
		if _, ok := m[field]; !ok {
			t.Errorf("created profile missing field %q", field)
		}
	}
	if m["name"] != "shape-test" {
		t.Errorf("name = %v, want shape-test", m["name"])
	}
	if m["type"] != "local" {
		t.Errorf("type = %v, want local", m["type"])
	}
}

// TestContractProfilesActiveField verifies GET /profiles stamps the active
// field: the activated profile has active=true, others have active=false.
func TestContractProfilesActiveField(t *testing.T) {
	r := contractRouter(t)
	handler := New(r)

	// Create two profiles.
	resp1 := doRequest(handler, "POST", "/api/control/profiles",
		`{"name":"p1","content":"a: 1"}`)
	var m1 map[string]any
	jsonDecode(t, resp1.Body, &m1)
	id1, _ := m1["id"].(string)

	resp2 := doRequest(handler, "POST", "/api/control/profiles",
		`{"name":"p2","content":"b: 2"}`)
	var m2 map[string]any
	jsonDecode(t, resp2.Body, &m2)
	id2, _ := m2["id"].(string)

	// Activate p1 (may fail validation, but the store marks it active).
	doRequest(handler, "POST", "/api/control/profiles/"+id1+"/activate", "")

	// GET /profiles should stamp active on each item.
	listResp := doRequest(handler, "GET", "/api/control/profiles", "")
	var list []map[string]any
	jsonDecode(t, listResp.Body, &list)

	foundActive := false
	for _, item := range list {
		id, _ := item["id"].(string)
		active, _ := item["active"].(bool)
		if id == id1 {
			if !active {
				t.Errorf("profile %s should be active", id1)
			}
			foundActive = true
		} else if id == id2 {
			if active {
				t.Errorf("profile %s should not be active", id2)
			}
		}
	}
	if !foundActive {
		t.Errorf("no active profile found in list")
	}
}

// TestSectionRouteUsesSettingsOverlay verifies PUT/GET /config/section now go
// through the managed settings overlay instead of requiring an active profile,
// and that the overlay itself is read-only through the profile routes.
func TestSectionRouteUsesSettingsOverlay(t *testing.T) {
	r := contractRouter(t)
	handler := New(r)

	// No active profile: previously 409 — now it must persist (restart=false
	// so the /usr/bin/false supervisor is never asked to start).
	rec := doRequest(handler, "PUT", "/api/control/config/section",
		`{"key":"mixed-port","value":7897,"restart":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT section without active profile = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(handler, "GET", "/api/control/config/section?key=mixed-port", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET section = %d, want 200", rec.Code)
	}
	var got any
	jsonDecode(t, rec.Body, &got)
	if got != float64(7897) {
		t.Errorf("section value = %v, want 7897", got)
	}

	// Exactly one program-owned settings overlay got created.
	rec = doRequest(handler, "GET", "/api/control/profiles", "")
	var list []map[string]any
	jsonDecode(t, rec.Body, &list)
	n := 0
	var id string
	for _, m := range list {
		if m["managedBy"] == "settings" {
			n++
			id, _ = m["id"].(string)
		}
	}
	if n != 1 || id == "" {
		t.Fatalf("settings overlays = %d (id %q), want exactly 1", n, id)
	}

	// Read-only: PUT and DELETE are refused, GET still exposes the content.
	if rec := doRequest(handler, "PUT", "/api/control/profiles/"+id, `{"name":"hacked"}`); rec.Code != http.StatusForbidden {
		t.Errorf("PUT settings overlay = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, "DELETE", "/api/control/profiles/"+id, ""); rec.Code != http.StatusForbidden {
		t.Errorf("DELETE settings overlay = %d, want 403", rec.Code)
	}
	rec = doRequest(handler, "GET", "/api/control/profiles/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings overlay = %d, want 200", rec.Code)
	}
	var detail struct {
		Meta    map[string]any `json:"meta"`
		Content string         `json:"content"`
	}
	jsonDecode(t, rec.Body, &detail)
	if !strings.Contains(detail.Content, "mixed-port: 7897") {
		t.Errorf("content = %q, want the persisted key", detail.Content)
	}
}

// TestCreateRejectsForgedSettingsOverlay pins that clients cannot mint their
// own managedBy=settings profile through POST /profiles.
func TestCreateRejectsForgedSettingsOverlay(t *testing.T) {
	r := contractRouter(t)
	handler := New(r)
	rec := doRequest(handler, "POST", "/api/control/profiles",
		`{"name":"fake","type":"merge","managedBy":"settings"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST forged settings overlay = %d, want 403", rec.Code)
	}
}

// TestVisualEditorOverlayRoutesUnchanged pins that the visual-editor overlay
// guard keeps its wording and that such overlays stay deletable.
func TestVisualEditorOverlayRoutesUnchanged(t *testing.T) {
	r := contractRouter(t)
	handler := New(r)
	base := doRequest(handler, "POST", "/api/control/profiles", `{"name":"b","content":"a: 1"}`)
	var bm map[string]any
	jsonDecode(t, base.Body, &bm)
	baseID, _ := bm["id"].(string)

	rec := doRequest(handler, "POST", "/api/control/profiles",
		`{"name":"ve","type":"merge","baseProfileId":"`+baseID+`","managedBy":"visual-editor"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create visual-editor overlay = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var vm map[string]any
	jsonDecode(t, rec.Body, &vm)
	veID, _ := vm["id"].(string)

	rec = doRequest(handler, "PUT", "/api/control/profiles/"+veID, `{"name":"hacked"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT visual-editor overlay = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "managed visual editor overlays are read-only") {
		t.Errorf("403 message = %s, want the existing wording", rec.Body.String())
	}
	// Visual-editor overlays stay deletable (deleting clears the pending edit).
	if rec := doRequest(handler, "DELETE", "/api/control/profiles/"+veID, ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE visual-editor overlay = %d, want 204", rec.Code)
	}
}

func doRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func jsonDecode(t *testing.T, buf interface{ Bytes() []byte }, v any) {
	t.Helper()
	if err := json.Unmarshal(buf.Bytes(), v); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, string(buf.Bytes()))
	}
}
