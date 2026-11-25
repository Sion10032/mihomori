package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"mihomori/internal/server/profile"
	"mihomori/internal/server/supervisor"
)

func wiredProfiles(home string, manager **Manager) *profile.Store {
	return profile.New(profile.Options{Dir: filepath.Join(home, "profiles"), ActiveConfigPath: filepath.Join(home, "active.yaml"), OnChange: func(id string) {
		if *manager != nil {
			(*manager).SourceChanged(id)
		}
	}, TransformBase: func(id, body string) (string, error) {
		if *manager != nil {
			return (*manager).TransformBase(id, body)
		}
		return body, nil
	}})
}
func fixture(t *testing.T) (*Manager, *profile.Store) {
	t.Helper()
	dir := t.TempDir()
	var manager *Manager
	p := wiredProfiles(dir, &manager)
	sources := []string{"proxies: [{name: 'HK.(备用)+', type: socks5, server: localhost, port: 1080}, {name: Singapore, type: socks5, server: localhost, port: 1081}]\nrules: ['MATCH,DIRECT']\n", "proxies: [{name: 'HK.(备用)+', type: socks5, server: localhost, port: 1082}, {name: Japan, type: socks5, server: localhost, port: 1083}]\n"}
	for i, body := range sources {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		t.Cleanup(up.Close)
		if _, err := p.ImportFromURL(up.URL, []string{"A", "B"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	sup := supervisor.New(supervisor.Options{BinaryPath: "/usr/bin/false", HomeDir: dir, ActiveConfigPath: filepath.Join(dir, "active.yaml"), ExternalController: "127.0.0.1:19998", Secret: "test"})
	t.Cleanup(func() { _ = sup.Dispose() })
	m, err := New(Options{Home: dir, Port: 18080, Token: "test", Profiles: p, Supervisor: sup})
	if err != nil {
		t.Fatal(err)
	}
	manager = m
	if _, err = m.syncProfilesLocked(); err != nil {
		t.Fatal(err)
	}
	return m, p
}
func TestIndependentSourcesDerivedNamesAndReferences(t *testing.T) {
	m, p := fixture(t)
	state, _ := m.copyData()
	if len(state.Sources) != 2 || len(state.Plans) != 3 {
		t.Fatal("independent sources or three plans missing")
	}
	originals := map[string]string{}
	for _, meta := range p.List() {
		b, _ := p.Read(meta.ID)
		originals[meta.ID] = b
	}
	src := state.Sources[0]
	id := nodeID(src.ID, "HK.(备用)+")
	excluded := true
	tags := []string{"work"}
	if _, err := m.EditNode(context.Background(), src.ID, id, &excluded, &tags); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Feed(context.Background(), src.ID)
	b, _ := m.Feed(context.Background(), state.Sources[1].ID)
	if strings.Contains(string(a), "HK.(备用)+") || !strings.Contains(string(b), "[B] HK.(备用)+") {
		t.Fatal("precise exclusion crossed source boundaries")
	}
	again, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := again.Feed(context.Background(), src.ID)
	if !bytes.Equal(a, restored) {
		t.Fatal("exclusion did not survive reconstruction")
	}
	for id, body := range originals {
		got, _ := p.Read(id)
		if body != got {
			t.Fatal("original subscription bytes were modified")
		}
	}
	state, caches := again.copyData()
	if caches[src.ID].Nodes[0]["name"] != "HK.(备用)+" || !state.Sources[0].Excluded[id] {
		t.Fatal("raw node cache or exclusion metadata lost")
	}
}
func TestCommitFailurePreservesMemoryDiskAndPublishedCache(t *testing.T) {
	m, _ := fixture(t)
	state, caches := m.copyData()
	beforeState, _ := os.ReadFile(m.statePath())
	source := state.Sources[0]
	beforeCache, _ := os.ReadFile(m.cachePath(source.ID))
	beforeFeed, _ := m.Feed(context.Background(), source.ID)
	if err := os.Rename(m.statePath(), m.statePath()+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.statePath(), 0700); err != nil {
		t.Fatal(err)
	}
	state.Sources[0].Excluded = map[string]bool{nodeID(source.ID, "Singapore"): true}
	if err := m.commit(state, caches); err == nil {
		t.Fatal("save should fail")
	}
	afterCache, _ := os.ReadFile(m.cachePath(source.ID))
	afterFeed, _ := m.Feed(context.Background(), source.ID)
	if !bytes.Equal(beforeCache, afterCache) || !bytes.Equal(beforeFeed, afterFeed) {
		t.Fatal("failed commit changed published cache")
	}
	current, _ := m.copyData()
	if current.Sources[0].Excluded[nodeID(source.ID, "Singapore")] {
		t.Fatal("failed commit changed memory")
	}
	_ = os.Remove(m.statePath())
	_ = os.Rename(m.statePath()+".saved", m.statePath())
	afterState, _ := os.ReadFile(m.statePath())
	if !bytes.Equal(beforeState, afterState) {
		t.Fatal("saved state changed")
	}
	if _, err := New(m.opts); err != nil {
		t.Fatal(err)
	}
}
func TestRenderUsesStableProviderReferencesAndTags(t *testing.T) {
	m, _ := fixture(t)
	state, caches := m.copyData()
	p := state.Plans[0]
	p.Groups = []Group{{Name: "work", Type: "url-test", Tags: []string{"work"}, Lazy: true}}
	first, err := m.render(state, p)
	if err != nil {
		t.Fatal(err)
	}
	state.Sources[0].URL = "https://different.example/sub"
	state.Sources[0].Interval = 60
	state.Sources[0].Excluded = map[string]bool{nodeID(state.Sources[0].ID, "Singapore"): true}
	second, err := m.render(state, p)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("daily source edits changed runtime structure")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(first), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["proxies"] != nil || len(doc["proxy-providers"].(map[string]any)) != 2 {
		t.Fatal("source node entities were expanded into runtime")
	}
	group := doc["proxy-groups"].([]any)[0].(map[string]any)
	if len(group["use"].([]any)) != 2 || !strings.Contains(group["filter"].(string), "标签") {
		t.Fatal("native multi-source or label filter missing")
	}
	source := state.Sources[0]
	source.Tags = map[string][]string{nodeID(source.ID, "HK.(备用)+"): {"work"}}
	b, err := providerBody(source, caches[source.ID])
	if err != nil || !strings.Contains(string(b), "[标签:work]") {
		t.Fatal("derived tags missing")
	}
}
func TestFailedURLReplacementKeepsOriginalStoreAndNativeCache(t *testing.T) {
	m, p := fixture(t)
	state, _ := m.copyData()
	source := state.Sources[0]
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	before, _ := m.Feed(context.Background(), source.ID)
	raw, _ := p.Read(source.ProfileID)
	next := bad.URL
	if _, err := p.Update(source.ProfileID, profile.UpdateInput{URL: &next}); err == nil {
		t.Fatal("failed source replacement accepted")
	}
	after, _ := m.Feed(context.Background(), source.ID)
	cached, _ := p.Read(source.ProfileID)
	updated, _ := m.copyData()
	if !bytes.Equal(before, after) || raw != cached || updated.Sources[0].URL != source.URL {
		t.Fatal("failed URL edit replaced last good source")
	}
}
func TestEmptyProviderFailsClosedAndFeedDoesNotFetch(t *testing.T) {
	m, _ := fixture(t)
	state, caches := m.copyData()
	s := &state.Sources[0]
	s.URL = "http://127.0.0.1:1/never"
	s.Excluded = map[string]bool{}
	for _, node := range caches[s.ID].Nodes {
		s.Excluded[nodeID(s.ID, node["name"].(string))] = true
	}
	if err := m.commit(state, caches); err != nil {
		t.Fatal(err)
	}
	b, err := m.Feed(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Proxies) != 1 || doc.Proxies[0]["type"] != "reject" {
		t.Fatal("empty provider did not fail closed")
	}
}
func TestCorruptStateAndSeparateSourceAPIIsAbsent(t *testing.T) {
	m, _ := fixture(t)
	r := httptest.NewRequest(http.MethodPost, "http://router/api/control/manage/sources", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("separate source CRUD API remains exposed")
	}
	if err := os.WriteFile(m.statePath(), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(m.opts); err == nil {
		t.Fatal("corrupt state was silently reset")
	}
	b, _ := os.ReadFile(m.statePath())
	if json.Valid(b) {
		t.Fatal("corrupt state overwritten")
	}
}

func TestOriginalProfilesRemainTheOnlySources(t *testing.T) {
	m, p := fixture(t)
	state, _ := m.copyData()
	source := state.Sources[0]
	renamed := "renamed"
	if _, err := p.Update(source.ProfileID, profile.UpdateInput{Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ImportFromURL(source.URL, "A"); err != nil {
		t.Fatal(err)
	}
	state, _ = m.copyData()
	labels := map[string]bool{}
	for _, source := range state.Sources {
		if labels[source.Provider] {
			t.Fatal("duplicate provider identity")
		}
		labels[source.Provider] = true
	}
	if len(p.List()) != 3 || len(state.Sources) != 3 {
		t.Fatal("sources are not original profiles or generated profiles leaked")
	}
	if err := p.Delete(source.ProfileID); err != nil {
		t.Fatal(err)
	}
	restored, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restored.syncProfilesLocked(); err != nil {
		t.Fatal(err)
	}
	state, _ = restored.copyData()
	if len(p.List()) != 2 || len(state.Sources) != 2 {
		t.Fatal("deleted source resurrected or generated profiles created")
	}
	if !filepath.IsAbs(m.opts.Home) {
		t.Fatal("home is relative")
	}
}

func TestBootMigratesOnlyRecordedGeneratedProfiles(t *testing.T) {
	m, profiles := fixture(t)
	validator := supervisor.New(supervisor.Options{BinaryPath: "/usr/bin/true", HomeDir: m.opts.Home, ActiveConfigPath: filepath.Join(m.opts.Home, "active.yaml"), Secret: "test"})
	defer validator.Dispose()
	m.opts.Supervisor = validator
	m.suppress.Store(true)
	ordinary, err := profiles.Create(profile.CreateInput{Name: "A", Content: "rules: ['MATCH,DIRECT']\n"})
	if err != nil {
		t.Fatal(err)
	}
	state, caches := m.copyData()
	for i := range state.Plans {
		meta, e := profiles.Create(profile.CreateInput{Name: state.Plans[i].Name, Content: "# /api/control/manage/providers/legacy\nrules: ['MATCH,REJECT']\n"})
		if e != nil {
			t.Fatal(e)
		}
		state.Plans[i].ProfileID = meta.ID
	}
	if err = m.commit(state, caches); err != nil {
		t.Fatal(err)
	}
	if err = profiles.SetActive(state.Plans[0].ProfileID); err != nil {
		t.Fatal(err)
	}
	m.suppress.Store(false)
	if err = m.PrepareBoot(); err != nil {
		t.Fatal(err)
	}
	if len(profiles.List()) != 3 {
		t.Fatal("generated records were not removed or an ordinary profile was deleted")
	}
	if _, err = profiles.Read(ordinary.ID); err != nil {
		t.Fatal("ordinary same-name local profile lost")
	}
	state, _ = m.copyData()
	if profiles.GetActiveID() != state.Sources[0].ProfileID {
		t.Fatal("active identity is not an original source")
	}
	for _, p := range state.Plans {
		if p.ProfileID != "" {
			t.Fatal("generated binding survived migration")
		}
	}
}
