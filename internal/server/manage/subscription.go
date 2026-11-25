package manage

import (
	"encoding/base64"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"net/url"
	"strconv"
	"strings"
)

var ErrInvalidSubscription = errors.New("invalid subscription")

func validateSubscriptionURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: URL must be an absolute HTTP or HTTPS URL", ErrInvalidSubscription)
	}
	return nil
}

// subscriptionNodes accepts full Mihomo YAML or a raw/base64 list of Trojan
// and AnyTLS links. YAML remains the lossless path for every other protocol.
func subscriptionNodes(body string) ([]map[string]any, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("subscription is empty; cached nodes were retained")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(body), &doc); err == nil && doc != nil {
		items, ok := doc["proxies"].([]any)
		if !ok || len(items) == 0 {
			return nil, errors.New("pool sources must contain a nonempty proxies list")
		}
		nodes := make([]map[string]any, 0, len(items))
		for _, item := range items {
			node, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("invalid subscription node")
			}
			for _, key := range []string{"name", "type", "server"} {
				if v, ok := node[key].(string); !ok || strings.TrimSpace(v) == "" {
					return nil, fmt.Errorf("subscription node requires %s", key)
				}
			}
			port, err := strconv.Atoi(fmt.Sprint(node["port"]))
			if err != nil || port < 1 || port > 65535 {
				return nil, errors.New("subscription node has an invalid port")
			}
			nodes = append(nodes, node)
		}
		return nodes, nil
	}
	if !strings.Contains(body, "://") {
		compact := strings.Join(strings.Fields(body), "")
		var decoded []byte
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(compact); err == nil {
				decoded = b
				break
			}
		}
		if len(decoded) == 0 {
			return nil, errors.New("subscription is neither Mihomo YAML nor a supported link list")
		}
		return subscriptionNodes(string(decoded))
	}
	var nodes []map[string]any
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			return nil, errors.New("invalid subscription link")
		}
		// Some providers prepend SSR account/usage metadata to Trojan lists.
		// SSR is not a Mihomo protocol; it must never become a pool node.
		if u.Scheme == "ssr" {
			continue
		}
		if u.Scheme != "trojan" && u.Scheme != "anytls" {
			return nil, errors.New("use Mihomo YAML for protocols other than Trojan and AnyTLS")
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || u.Hostname() == "" || u.User == nil {
			return nil, errors.New("invalid subscription link endpoint")
		}
		password := u.User.Username()
		if p, ok := u.User.Password(); ok {
			password += ":" + p
		}
		if password == "" {
			return nil, errors.New("subscription link has no password")
		}
		name := u.Fragment
		if name == "" {
			name = u.Host
		}
		node := map[string]any{"name": name, "type": u.Scheme, "server": u.Hostname(), "port": port, "password": password, "udp": true}
		q := u.Query()
		for _, key := range []string{"sni", "peer"} {
			if q.Get(key) != "" {
				node["sni"] = q.Get(key)
				break
			}
		}
		if alpn := q.Get("alpn"); alpn != "" {
			node["alpn"] = strings.Split(alpn, ",")
		}
		if fp := q.Get("fp"); fp != "" {
			node["client-fingerprint"] = fp
		}
		for _, key := range []string{"insecure", "allowInsecure"} {
			if v := q.Get(key); v != "" {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return nil, errors.New("invalid TLS verification option")
				}
				node["skip-cert-verify"] = b
			}
		}
		if transport := q.Get("type"); transport != "" && transport != "tcp" {
			if transport != "ws" && transport != "grpc" {
				return nil, errors.New("unsupported link transport; use Mihomo YAML")
			}
			node["network"] = transport
			if transport == "ws" {
				opts := map[string]any{"path": q.Get("path")}
				if q.Get("host") != "" {
					opts["headers"] = map[string]any{"Host": q.Get("host")}
				}
				node["ws-opts"] = opts
			} else {
				node["grpc-opts"] = map[string]any{"grpc-service-name": q.Get("serviceName")}
			}
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, errors.New("subscription contains no supported nodes")
	}
	return nodes, nil
}
