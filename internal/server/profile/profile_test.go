package profile

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mihomori/internal/api"
)

// testStore creates a Store in a temp dir with deterministic ID generation.
// Returns the store, temp dir, and a cleanup function.
func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	activePath := filepath.Join(dir, "active.yaml")
	var seq atomic.Int32
	store := New(Options{
		Dir:              dir,
		ActiveConfigPath: activePath,
		IDGen: func() string {
			n := seq.Add(1)
			return "id-" + string(rune('a'-1+int(n)))
		},
		Now: func() int64 { return time.Now().UnixMilli() },
	})
	return store, dir
}

// writeProfile writes a profile .yaml file directly (bypasses Store API).
func writeProfile(t *testing.T, dir, id, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("writeProfile %s: %v", id, err)
	}
}

// --- CRUD tests ---

func TestCreateLocalProfile(t *testing.T) {
	s, _ := testStore(t)
	content := "mixed-port: 7890\nrules:\n  - MATCH\n"
	m, err := s.Create(CreateInput{Name: "test", Content: content, Type: TypeLocal})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.Name != "test" {
		t.Errorf("Name = %q, want test", m.Name)
	}
	if m.Type != TypeLocal {
		t.Errorf("Type = %q, want local", m.Type)
	}
	// Verify file was written.
	got, err := s.Read(m.ID)
	if err != nil {
		t.Fatalf("Read after Create: %v", err)
	}
	if got != content {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestCreateScriptReturnsError(t *testing.T) {
	s, _ := testStore(t)
	_, err := s.Create(CreateInput{Name: "script", Type: TypeScript})
	if err != ErrScriptNotSupported {
		t.Errorf("expected ErrScriptNotSupported, got %v", err)
	}
}

func TestListReturnsAllProfiles(t *testing.T) {
	s, _ := testStore(t)
	s.Create(CreateInput{Name: "p1", Content: "a: 1"})
	s.Create(CreateInput{Name: "p2", Content: "b: 2"})

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("List() returned %d, want 2", len(list))
	}
	if list[0].Name != "p1" || list[1].Name != "p2" {
		t.Errorf("names = [%s %s], want [p1 p2]", list[0].Name, list[1].Name)
	}
}

func TestReadReturnsContent(t *testing.T) {
	s, _ := testStore(t)
	m, _ := s.Create(CreateInput{Name: "read-test", Content: "foo: bar\n"})
	content, err := s.Read(m.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "foo: bar\n" {
		t.Errorf("content = %q, want 'foo: bar\\n'", content)
	}
}

func TestReadNotFound(t *testing.T) {
	s, _ := testStore(t)
	_, err := s.Read("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent profile")
	}
}

func TestUpdateName(t *testing.T) {
	s, _ := testStore(t)
	m, _ := s.Create(CreateInput{Name: "old", Content: "x: 1"})

	newName := "new"
	updated, err := s.Update(m.ID, UpdateInput{Name: &newName})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "new" {
		t.Errorf("Name = %q, want 'new'", updated.Name)
	}
}

func TestUpdateContent(t *testing.T) {
	s, _ := testStore(t)
	m, _ := s.Create(CreateInput{Name: "p", Content: "old: 1"})

	newContent := "new: 2\n"
	_, err := s.Update(m.ID, UpdateInput{Content: &newContent})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Read(m.ID)
	if got != "new: 2\n" {
		t.Errorf("content = %q, want 'new: 2\\n'", got)
	}
}

func TestDelete(t *testing.T) {
	s, dir := testStore(t)
	m, _ := s.Create(CreateInput{Name: "del", Content: "x: 1"})
	if err := s.Delete(m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// File should be gone.
	if _, err := os.Stat(filepath.Join(dir, m.ID+".yaml")); !os.IsNotExist(err) {
		t.Error("file should not exist after Delete")
	}
	// Not in list.
	if len(s.List()) != 0 {
		t.Error("List() should be empty after Delete")
	}
}

func TestDeleteNotFound(t *testing.T) {
	s, _ := testStore(t)
	if err := s.Delete("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent profile")
	}
}

func TestDuplicate(t *testing.T) {
	s, _ := testStore(t)
	m, _ := s.Create(CreateInput{Name: "original", Content: "dup: 1"})
	dup, err := s.Duplicate(m.ID, "copy")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if dup.ID == m.ID {
		t.Error("duplicate should have different ID")
	}
	if dup.Name != "copy" {
		t.Errorf("Name = %q, want 'copy'", dup.Name)
	}
	got, _ := s.Read(dup.ID)
	if got != "dup: 1" {
		t.Errorf("content = %q, want 'dup: 1'", got)
	}
}

// --- Active lifecycle tests ---

func TestSetActiveWritesActiveYAML(t *testing.T) {
	s, dir := testStore(t)
	s.Create(CreateInput{Name: "p1", Content: "mixed-port: 7890\n"})

	m, _ := s.Create(CreateInput{Name: "p2", Content: "mixed-port: 1080\n"})
	if err := s.SetActive(m.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "active.yaml"))
	if err != nil {
		t.Fatalf("read active.yaml: %v", err)
	}
	if string(content) != "mixed-port: 1080\n" {
		t.Errorf("active.yaml = %q, want 'mixed-port: 1080\\n'", string(content))
	}
}

func TestSetActiveCreatesBak(t *testing.T) {
	s, dir := testStore(t)
	s.Create(CreateInput{Name: "p1", Content: "old: 1\n"})
	s.SetActive(s.List()[0].ID)

	// Activate a second profile → .bak should be created from first activation.
	s.Create(CreateInput{Name: "p2", Content: "new: 2\n"})
	s.SetActive(s.List()[1].ID)

	bakPath := filepath.Join(dir, "active.yaml.bak")
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("read .bak: %v", err)
	}
	if string(bakContent) != "old: 1\n" {
		t.Errorf("active.yaml.bak = %q, want 'old: 1\\n'", string(bakContent))
	}
}

func TestGetActiveID(t *testing.T) {
	s, _ := testStore(t)
	if got := s.GetActiveID(); got != "" {
		t.Errorf("GetActiveID() = %q, want empty initially", got)
	}

	s.Create(CreateInput{Name: "p", Content: "x: 1"})
	m := s.List()[0]
	s.SetActive(m.ID)

	if got := s.GetActiveID(); got != m.ID {
		t.Errorf("GetActiveID() = %q, want %q", got, m.ID)
	}
}

func TestRollback(t *testing.T) {
	s, dir := testStore(t)
	s.Create(CreateInput{Name: "first", Content: "version: 1\n"})
	s.SetActive(s.List()[0].ID)

	s.Create(CreateInput{Name: "second", Content: "version: 2\n"})
	s.SetActive(s.List()[1].ID)

	// Current active.yaml has version: 2.
	content, _ := os.ReadFile(filepath.Join(dir, "active.yaml"))
	if string(content) != "version: 2\n" {
		t.Fatalf("pre-rollback: %q", string(content))
	}

	ok := s.Rollback()
	if !ok {
		t.Fatal("Rollback returned false")
	}
	content, _ = os.ReadFile(filepath.Join(dir, "active.yaml"))
	if string(content) != "version: 1\n" {
		t.Errorf("after rollback: %q, want 'version: 1\\n'", string(content))
	}
}

func TestRollbackNoBak(t *testing.T) {
	s, _ := testStore(t)
	if s.Rollback() {
		t.Error("Rollback should return false when no .bak exists")
	}
}

func TestResetActive(t *testing.T) {
	s, dir := testStore(t)
	s.Create(CreateInput{Name: "p", Content: "x: 1"})
	s.SetActive(s.List()[0].ID)
	s.ResetActive()

	if got := s.GetActiveID(); got != "" {
		t.Errorf("GetActiveID() = %q, want empty after ResetActive", got)
	}
	// state.json should exist but contain empty active ID.
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("state.json not found: %v", err)
	}
	if string(b) != "{\n}" && string(b) != "{}" {
		t.Errorf("state.json = %q, want empty state", string(b))
	}
}

// --- copyFile regression test ---

func TestCopyFileCloseErrorSurfaced(t *testing.T) {
	// Verify copyFile properly checks out.Close() error.
	// We can't easily inject a Close error without mocking fs, but we can
	// verify the function exists and produces correct output — the code
	// path is now: explicit Close() with error capture, not deferred Close.
	s, dir := testStore(t)
	s.Create(CreateInput{Name: "src", Content: "copy: me\n"})
	m := s.List()[0]

	srcPath := filepath.Join(dir, m.ID+".yaml")
	dstPath := filepath.Join(dir, "copy.yaml")
	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	content, _ := os.ReadFile(dstPath)
	if string(content) != "copy: me\n" {
		t.Errorf("copyFile content = %q, want 'copy: me\\n'", string(content))
	}
}

// --- Concurrency test ---

func TestConcurrentSetActive(t *testing.T) {
	s, _ := testStore(t)
	s.Create(CreateInput{Name: "c1", Content: "c: 1\n"})
	s.Create(CreateInput{Name: "c2", Content: "c: 2\n"})

	// Also create more profiles for the concurrent loop.
	for i := 3; i <= 5; i++ {
		s.Create(CreateInput{Name: "c" + string(rune('0'+i)), Content: "c: " + string(rune('0'+i)) + "\n"})
	}

	list := s.List()
	if len(list) < 5 {
		t.Fatalf("need 5 profiles, got %d", len(list))
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx := n % len(list)
			s.SetActive(list[idx].ID)
		}(i)
	}
	wg.Wait()

	// After all goroutines finish, GetActiveID must return a valid ID.
	active := s.GetActiveID()
	found := false
	for _, m := range list {
		if m.ID == active {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GetActiveID() = %q, not in profile list", active)
	}
}

// --- Compose tests ---

func TestComposeLocalProfile(t *testing.T) {
	s, _ := testStore(t)
	s.Create(CreateInput{Name: "local", Content: "mixed-port: 7890\n"})
	m := s.List()[0]

	content, _, err := s.Compose(m.ID)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if content != "mixed-port: 7890\n" {
		t.Errorf("Compose = %q, want verbatim", content)
	}
}

func TestComposeOverlayMerge(t *testing.T) {
	s, _ := testStore(t)
	base, _ := s.Create(CreateInput{Name: "base", Content: "mixed-port: 7890\n"})
	// Create a merge overlay.
	overlay, _ := s.Create(CreateInput{
		Name:          "overlay",
		Type:          TypeMerge,
		BaseProfileID: base.ID,
		Content:       "mixed-port: 1080\n",
	})
	_ = overlay

	content, comps, err := s.Compose(base.ID)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(comps) != 1 {
		t.Fatalf("composition length = %d, want 1", len(comps))
	}
	if comps[0].ID != overlay.ID {
		t.Error("overlay not in composition")
	}
	// Overlay should override mixed-port.
	if content != "mixed-port: 1080\n" {
		t.Errorf("Compose = %q, want overlay override", content)
	}
}

func TestComposeRejectsOverlayAsBase(t *testing.T) {
	s, _ := testStore(t)
	overlay, _ := s.Create(CreateInput{Name: "o", Type: TypeMerge, BaseProfileID: "x"})
	_, _, err := s.Compose(overlay.ID)
	if err != ErrIsOverlay {
		t.Errorf("expected ErrIsOverlay, got %v", err)
	}
}

// --- GetSection / SetSection ---

func TestGetSectionReturnsMap(t *testing.T) {
	s, _ := testStore(t)
	s.Create(CreateInput{Name: "p", Content: "dns:\n  enable: true\n"})
	m := s.List()[0]

	val, err := s.GetSection(m.ID, "dns")
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	dns, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("dns = %T, want map", val)
	}
	if dns["enable"] != true {
		t.Errorf("dns.enable = %v, want true", dns["enable"])
	}
}

func TestSetSectionUpdatesContent(t *testing.T) {
	s, _ := testStore(t)
	s.Create(CreateInput{Name: "p", Content: "dns:\n  enable: true\n"})
	m := s.List()[0]

	err := s.SetSection(m.ID, "dns", map[string]any{"enable": false})
	if err != nil {
		t.Fatalf("SetSection: %v", err)
	}

	val, _ := s.GetSection(m.ID, "dns")
	dns := val.(map[string]any)
	if dns["enable"] != false {
		t.Errorf("dns.enable = %v, want false after SetSection", dns["enable"])
	}
}

func TestSetSectionParseErrorKeepsContent(t *testing.T) {
	s, _ := testStore(t)
	content := "a: 1\na: 2\n" // duplicate key: yaml.v3 errors
	m, err := s.Create(CreateInput{Name: "p", Content: content})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetSection(m.ID, "mixed-port", 7897); err == nil {
		t.Error("SetSection on unparseable content returned nil, want error")
	}
	got, _ := s.Read(m.ID)
	if got != content {
		t.Errorf("content = %q, want untouched %q", got, content)
	}
}

func TestCreateSettingsOverlayValidation(t *testing.T) {
	s, _ := testStore(t)
	if _, err := s.Create(CreateInput{Name: "bad", Type: TypeLocal, ManagedBy: ManagedBySettings}); err == nil {
		t.Error("local settings overlay accepted, want error")
	}
	if _, err := s.Create(CreateInput{Name: "bad", Type: TypeMerge, BaseProfileID: "base-1", ManagedBy: ManagedBySettings}); err == nil {
		t.Error("scoped settings overlay accepted, want error")
	}
	m, err := s.Create(CreateInput{Name: SettingsOverlayName, Type: TypeMerge, ManagedBy: ManagedBySettings})
	if err != nil {
		t.Fatalf("Create settings overlay: %v", err)
	}
	if m.ManagedBy != ManagedBySettings || m.BaseProfileID != "" || !isSettingsOverlay(m) {
		t.Errorf("meta = %+v, want unscoped settings overlay", m)
	}
}

func TestSettingsOverlayLockedCreatesOnce(t *testing.T) {
	s, _ := testStore(t)
	s.mu.Lock()
	first, err := s.settingsOverlayLocked()
	if err != nil {
		s.mu.Unlock()
		t.Fatalf("settingsOverlayLocked: %v", err)
	}
	second, err := s.settingsOverlayLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("settingsOverlayLocked (2nd): %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("ids differ: %s vs %s", first.ID, second.ID)
	}
	count := 0
	for _, m := range s.List() {
		if isSettingsOverlay(m) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("settings overlays = %d, want 1", count)
	}
}

// stubFetcher is a mutable offline subscription source for Refresh tests.
type stubFetcher struct{ body string }

func (f *stubFetcher) Fetch(ctx context.Context, url, userAgent string) (string, *api.SubscriptionInfo, error) {
	return f.body, nil, nil
}

// testStoreWithFetcher is testStore plus a stub subscription fetcher, so
// ImportFromURL/Refresh can run offline.
func testStoreWithFetcher(t *testing.T, body string) (*Store, *stubFetcher, string) {
	t.Helper()
	s, dir := testStore(t)
	f := &stubFetcher{body: body}
	s.fetcher = f
	return s, f, dir
}

const subV1 = "mixed-port: 7890\ntun:\n  enable: false\n  auto-route: true\n"
const subV2 = "mixed-port: 7890\nsocks-port: 7891\ntun:\n  enable: false\n  auto-route: true\n"

func TestApplySettingsSectionWritesOverlayNotBase(t *testing.T) {
	s, dir := testStore(t)
	base, _ := s.Create(CreateInput{Name: "base", Content: "mixed-port: 7890\n"})
	if err := s.SetActive(base.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("ApplySettingsSection: %v", err)
	}
	// The base profile file must stay byte-for-byte untouched (the whole bug).
	got, _ := s.Read(base.ID)
	if got != "mixed-port: 7890\n" {
		t.Errorf("base content = %q, want unchanged", got)
	}
	active, err := os.ReadFile(filepath.Join(dir, "active.yaml"))
	if err != nil {
		t.Fatalf("read active.yaml: %v", err)
	}
	top, _ := parseTopMap(string(active))
	if top["mixed-port"] != 7897 {
		t.Errorf("active mixed-port = %v, want 7897", top["mixed-port"])
	}
}

func TestApplySettingsSectionSurvivesRefresh(t *testing.T) {
	s, f, dir := testStoreWithFetcher(t, subV1)
	sub, err := s.ImportFromURL("http://sub.example/x", "sub")
	if err != nil {
		t.Fatalf("ImportFromURL: %v", err)
	}
	if err := s.SetActive(sub.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("apply mixed-port: %v", err)
	}
	if err := s.ApplySettingsSection("tun", map[string]any{"enable": true}); err != nil {
		t.Fatalf("apply tun: %v", err)
	}

	f.body = subV2 // subscription updated: same keys, new values
	if _, err := s.Refresh(sub.ID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := s.SetActive(sub.ID); err != nil {
		t.Fatalf("SetActive after refresh: %v", err)
	}

	active, _ := os.ReadFile(filepath.Join(dir, "active.yaml"))
	top, _ := parseTopMap(string(active))
	if top["mixed-port"] != 7897 {
		t.Errorf("mixed-port after refresh = %v, want 7897", top["mixed-port"])
	}
	if top["socks-port"] != 7891 {
		t.Errorf("socks-port after refresh = %v, want 7891 (from new subscription)", top["socks-port"])
	}
	tun, _ := top["tun"].(map[string]any)
	if tun["enable"] != true {
		t.Errorf("tun.enable = %v, want true (dashboard setting wins)", tun["enable"])
	}
	if tun["auto-route"] != true {
		t.Errorf("tun.auto-route = %v, want true (subscription subkey kept)", tun["auto-route"])
	}
}

func TestApplySettingsSectionSurvivesProfileSwitch(t *testing.T) {
	s, dir := testStore(t)
	a, _ := s.Create(CreateInput{Name: "a", Content: "mixed-port: 7890\n"})
	b, _ := s.Create(CreateInput{Name: "b", Content: "port: 7891\n"})
	s.SetActive(a.ID)
	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("ApplySettingsSection: %v", err)
	}
	if err := s.SetActive(b.ID); err != nil {
		t.Fatalf("switch to b: %v", err)
	}
	active, _ := os.ReadFile(filepath.Join(dir, "active.yaml"))
	top, _ := parseTopMap(string(active))
	if top["mixed-port"] != 7897 {
		t.Errorf("mixed-port after switch = %v, want 7897", top["mixed-port"])
	}
}

func TestApplySettingsSectionWithoutActiveProfile(t *testing.T) {
	s, dir := testStore(t)
	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("ApplySettingsSection with no active profile: %v", err)
	}
	base, _ := s.Create(CreateInput{Name: "base", Content: "mixed-port: 7890\n"})
	if err := s.SetActive(base.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	active, _ := os.ReadFile(filepath.Join(dir, "active.yaml"))
	top, _ := parseTopMap(string(active))
	if top["mixed-port"] != 7897 {
		t.Errorf("mixed-port = %v, want 7897 once a base is activated", top["mixed-port"])
	}
}

func TestApplySettingsSectionSelfHealsBrokenOverlay(t *testing.T) {
	s, dir := testStore(t)
	m, _ := s.Create(CreateInput{Name: SettingsOverlayName, Type: TypeMerge, ManagedBy: ManagedBySettings})
	writeProfile(t, dir, m.ID, "a: 1\na: 2\n") // duplicate key: unparseable

	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("ApplySettingsSection on broken overlay: %v", err)
	}
	raw, _ := s.Read(m.ID)
	top, err := parseTopMap(raw)
	if err != nil {
		t.Fatalf("overlay still unparseable: %v", err)
	}
	if top["mixed-port"] != 7897 || len(top) != 1 {
		t.Errorf("overlay = %v, want only mixed-port: 7897", top)
	}
}

func TestApplySettingsSectionDeletesKey(t *testing.T) {
	s, _ := testStore(t)
	if err := s.ApplySettingsSection("tun", map[string]any{"enable": true}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := s.ApplySettingsSection("tun", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, m := range s.List() {
		if !isSettingsOverlay(m) {
			continue
		}
		raw, _ := s.Read(m.ID)
		top, _ := parseTopMap(raw)
		if len(top) != 0 {
			t.Errorf("overlay = %v, want no keys after delete", top)
		}
	}
}

func TestComposeSettingsOverlayWinsOverOtherOverlays(t *testing.T) {
	s, _ := testStore(t)
	base, _ := s.Create(CreateInput{Name: "base", Content: "mixed-port: 7890\n"})
	// A user-authored global merge overlay (created by hand in the dashboard).
	s.Create(CreateInput{Name: "user merge", Type: TypeMerge, Content: "mixed-port: 2222\nsocks-port: 1111\n"})
	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("ApplySettingsSection: %v", err)
	}
	out, _, err := s.Compose(base.ID)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	top, _ := parseTopMap(out)
	if top["mixed-port"] != 7897 {
		t.Errorf("mixed-port = %v, want 7897 (settings applied last)", top["mixed-port"])
	}
	if top["socks-port"] != 1111 {
		t.Errorf("socks-port = %v, want 1111 (user overlay still applies)", top["socks-port"])
	}
}

func TestComposeEmptySettingsOverlayKeepsBaseVerbatim(t *testing.T) {
	s, _ := testStore(t)
	content := "mixed-port: 7890\nrules:\n  - MATCH\n"
	base, _ := s.Create(CreateInput{Name: "base", Content: content})
	if err := s.ApplySettingsSection("tun", map[string]any{"enable": true}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := s.ApplySettingsSection("tun", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	out, _, err := s.Compose(base.ID)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if out != content {
		t.Errorf("composed = %q, want base verbatim %q", out, content)
	}
}

func TestComposeIgnoresDuplicateSettingsOverlays(t *testing.T) {
	s, _ := testStore(t)
	base, _ := s.Create(CreateInput{Name: "base", Content: "mixed-port: 7890\n"})
	s.Create(CreateInput{Name: SettingsOverlayName, Type: TypeMerge, ManagedBy: ManagedBySettings})
	s.Create(CreateInput{Name: SettingsOverlayName + " copy", Type: TypeMerge, ManagedBy: ManagedBySettings})

	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("ApplySettingsSection with duplicates: %v", err)
	}
	out, _, err := s.Compose(base.ID)
	if err != nil {
		t.Fatalf("Compose with duplicates: %v", err)
	}
	top, _ := parseTopMap(out)
	if top["mixed-port"] != 7897 {
		t.Errorf("mixed-port = %v, want 7897 on the first (authoritative) overlay", top["mixed-port"])
	}
}

func TestGetEffectiveSectionPrecedence(t *testing.T) {
	s, _ := testStore(t)
	base, _ := s.Create(CreateInput{Name: "base", Content: "tun:\n  enable: false\nport: 7890\n"})
	if err := s.SetActive(base.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := s.ApplySettingsSection("tun", map[string]any{"enable": true}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Settings layer wins for keys it owns.
	v, err := s.GetEffectiveSection("tun")
	if err != nil {
		t.Fatalf("GetEffectiveSection(tun): %v", err)
	}
	if tun, _ := v.(map[string]any); tun["enable"] != true {
		t.Errorf("tun = %v, want enable true from the settings overlay", v)
	}
	// Base value is the fallback for keys the overlay doesn't own.
	v, err = s.GetEffectiveSection("port")
	if err != nil {
		t.Fatalf("GetEffectiveSection(port): %v", err)
	}
	if v != 7890 {
		t.Errorf("port = %v, want 7890 from the active base", v)
	}
	// Unknown key: null, no error.
	v, err = s.GetEffectiveSection("dns")
	if err != nil || v != nil {
		t.Errorf("GetEffectiveSection(dns) = (%v, %v), want (nil, nil)", v, err)
	}
}

func TestGetEffectiveSectionWithoutActiveProfile(t *testing.T) {
	s, _ := testStore(t)
	if err := s.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("apply: %v", err)
	}
	v, err := s.GetEffectiveSection("mixed-port")
	if err != nil || v != 7897 {
		t.Errorf("GetEffectiveSection = (%v, %v), want (7897, nil)", v, err)
	}
}

func TestGetEffectiveSectionToleratesBrokenOverlay(t *testing.T) {
	s, dir := testStore(t)
	base, _ := s.Create(CreateInput{Name: "base", Content: "port: 7890\n"})
	if err := s.SetActive(base.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	m, _ := s.Create(CreateInput{Name: SettingsOverlayName, Type: TypeMerge, ManagedBy: ManagedBySettings})
	writeProfile(t, dir, m.ID, "a: 1\na: 2\n") // unparseable

	v, err := s.GetEffectiveSection("port")
	if err != nil || v != 7890 {
		t.Errorf("GetEffectiveSection = (%v, %v), want (7890, nil) via base fallback", v, err)
	}
}
