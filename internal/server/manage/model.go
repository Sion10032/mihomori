// Package manage is an optional subscription-composition layer. It consumes the
// existing profile/supervisor interfaces and native Mihomo APIs; ordinary
// profiles, authentication, scheduling and the upstream dashboard stay intact.
package manage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Source struct {
	ID        string              `json:"id"`
	ProfileID string              `json:"profileId,omitempty"`
	Name      string              `json:"name"`
	Provider  string              `json:"provider"`
	URL       string              `json:"url"`
	UserAgent string              `json:"userAgent,omitempty"`
	Interval  int                 `json:"interval"` // minutes; zero disables automatic fetch
	UpdatedAt int64               `json:"updatedAt"`
	Error     string              `json:"error,omitempty"`
	Excluded  map[string]bool     `json:"excluded,omitempty"`
	Tags      map[string][]string `json:"tags,omitempty"`
}
type Group struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Match    string   `json:"match,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Sources  []string `json:"sources,omitempty"`
	URL      string   `json:"url,omitempty"`
	Interval int      `json:"interval,omitempty"`
	Timeout  int      `json:"timeout,omitempty"`
	Lazy     bool     `json:"lazy"`
}
type Plan struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	All       bool     `json:"all"`
	Sources   []string `json:"sources,omitempty"`
	Groups    []Group  `json:"groups"`
	ProfileID string   `json:"profileId,omitempty"`
}
type State struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
	Plans   []Plan   `json:"plans"`
	Active  string   `json:"activePlanId"`
}
type sourceCache struct {
	Raw   string           `json:"raw"`
	Nodes []map[string]any `json:"nodes"`
}
type Node struct {
	ID          string   `json:"id"`
	Source      string   `json:"source"`
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Type        string   `json:"type"`
	Excluded    bool     `json:"excluded"`
	Tags        []string `json:"tags,omitempty"`
}

func identifier(seed string) string {
	h := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(h[:8])
}
func newID() string                     { return identifier(time.Now().String()) }
func nodeID(source, name string) string { return identifier(source + "/" + name) }
func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".manage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, b)
}
func cloneState(s State) State {
	b, _ := json.Marshal(s)
	var out State
	_ = json.Unmarshal(b, &out)
	return out
}
func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}
func groupFilter(g Group) (string, error) {
	var parts []string
	if g.Match != "" {
		if _, err := regexp.Compile(g.Match); err != nil {
			return "", err
		}
		parts = append(parts, "(?:"+g.Match+")")
	}
	for _, tag := range cleanTags(g.Tags) {
		parts = append(parts, regexp.QuoteMeta("[标签:"+tag+"]"))
	}
	return strings.Join(parts, "|"), nil
}
func validatePlan(p Plan) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("组合名称不能为空")
	}
	seen := map[string]bool{"🚀 节点选择": true, "🎯 本地直连": true, "DIRECT": true, "REJECT": true, "GLOBAL": true}
	if len(p.Groups) == 0 {
		return errors.New("至少保留一个分组")
	}
	for _, g := range p.Groups {
		if g.Name == "" || seen[g.Name] {
			return errors.New("分组名称为空或重复")
		}
		seen[g.Name] = true
		switch g.Type {
		case "url-test", "fallback", "select", "load-balance":
		default:
			return errors.New("不支持的分组类型")
		}
		if _, err := groupFilter(g); err != nil {
			return err
		}
		if g.Interval < 0 || g.Timeout < 0 {
			return errors.New("测速时间不能为负数")
		}
	}
	return nil
}
func defaultGroups() []Group {
	specs := [][2]string{
		{"自动保活", ""},
		{"新加坡", `(?i)(新加坡|狮城|獅城|Singapore|🇸🇬|(^|[· _|/-])SG[0-9]*([· _|/-]|$))`},
		{"台湾", `(?i)(🇹🇼|台湾专线|台灣專線|台北|taipei|(^|[· _|/-])(台湾|台灣|taiwan|tw[0-9]*)([· _|/-]|$))`},
		{"美国", `(?i)(🇺🇸|美国|美國|(^|[· _|/-])(US|USA|United States|America)([· _|/-]|$))`},
		{"日本", `(?i)(🇯🇵|日本|东京|東京|(^|[· _|/-])(JP|JPN|Japan|Tokyo)([· _|/-]|$))`},
		{"GPT", `(?i)(🇹🇼|台湾专线|台灣專線|台北|taipei|美国|美國|🇺🇸|日本|🇯🇵|新加坡|Singapore|🇸🇬|(^|[· _|/-])(台湾|台灣|taiwan|tw[0-9]*|US|USA|JP|Japan|SG[0-9]*)([· _|/-]|$))`},
		{"游戏", `(?i)(游戏|遊戲|Game|Gaming|电竞|電競|Steam|PSN|Xbox|Switch)`},
		{"Netflix", `(?i)(Netflix|网飞|網飛|奈飞|奈飛)`},
	}
	out := make([]Group, 0, len(specs))
	for _, s := range specs {
		out = append(out, Group{Name: s[0], Type: "url-test", Match: s[1], URL: "https://www.gstatic.com/generate_204", Interval: 300, Timeout: 10000, Lazy: true})
	}
	return out
}
