package npm

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const DefaultRegistry = "https://registry.npmjs.org"

type Config struct {
	Registry        string
	ScopeRegistries map[string]string
	values          map[string]string
}

func DefaultConfig() *Config {
	return &Config{
		Registry:        DefaultRegistry,
		ScopeRegistries: map[string]string{},
		values:          map[string]string{},
	}
}

func LoadConfig(paths ...string) (*Config, error) {
	cfg := DefaultConfig()
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := cfg.LoadFile(path); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func DiscoverConfig(projectDir, explicit string) (*Config, error) {
	paths := defaultNPMRCPaths(projectDir)
	if explicit != "" {
		paths = append(paths, explicit)
	}
	return LoadConfig(paths...)
}

func defaultNPMRCPaths(projectDir string) []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".npmrc"))
	}
	if projectDir != "" {
		paths = append(paths, filepath.Join(projectDir, ".npmrc"))
	}
	return paths
}

func (c *Config) ApplyEnvAuthForRegistry(registry string) {
	if c == nil || registry == "" {
		return
	}
	targetURL := strings.TrimRight(registry, "/") + "/"
	key := c.longestAuthKey(targetURL)
	if key == "" {
		key = nerfDart(targetURL)
	}
	if key == "" {
		return
	}
	if token := firstEnv("NPM_TARGET_TOKEN", "NPM_AUTH_TOKEN", "NODE_AUTH_TOKEN", "NPM_TOKEN", "CI_JOB_TOKEN"); token != "" {
		c.values[key+":_authToken"] = token
		delete(c.values, key+":_auth")
		delete(c.values, key+":username")
		delete(c.values, key+":_password")
		return
	}
	username, password := firstUserPassEnv(
		"NPM_TARGET_USERNAME", "NPM_TARGET_PASSWORD",
		"CI_DEPLOY_USER", "CI_DEPLOY_PASSWORD",
		"NPM_USERNAME", "NPM_PASSWORD",
	)
	if username != "" && password != "" {
		delete(c.values, key+":_authToken")
		delete(c.values, key+":_auth")
		c.values[key+":username"] = username
		c.values[key+":_password"] = password
	}
}

func (c *Config) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = expandNPMRCValue(strings.Trim(strings.TrimSpace(value), `"'`))
		c.values[key] = value
		switch {
		case key == "registry":
			c.Registry = strings.TrimRight(value, "/")
		case strings.HasPrefix(key, "@") && strings.HasSuffix(key, ":registry"):
			scope := strings.TrimSuffix(key, ":registry")
			c.ScopeRegistries[scope] = strings.TrimRight(value, "/")
		}
	}
	return nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func firstUserPassEnv(names ...string) (string, string) {
	for i := 0; i+1 < len(names); i += 2 {
		username := os.Getenv(names[i])
		password := os.Getenv(names[i+1])
		if username != "" && password != "" {
			return username, password
		}
	}
	return "", ""
}

func expandNPMRCValue(value string) string {
	for {
		start := strings.Index(value, "${")
		if start == -1 {
			return value
		}
		end := strings.Index(value[start+2:], "}")
		if end == -1 {
			return value
		}
		end += start + 2
		name := value[start+2 : end]
		value = value[:start] + os.Getenv(name) + value[end+1:]
	}
}

func (c *Config) RegistryForPackage(name string) string {
	if strings.HasPrefix(name, "@") {
		scope, _, ok := strings.Cut(name, "/")
		if ok {
			if reg := c.ScopeRegistries[scope]; reg != "" {
				return reg
			}
		}
	}
	if c.Registry == "" {
		return DefaultRegistry
	}
	return c.Registry
}

type Auth struct {
	Header string
}

func (c *Config) AuthFor(rawURL string) Auth {
	if c == nil {
		return Auth{}
	}
	key := c.longestAuthKey(rawURL)
	if key == "" {
		return Auth{}
	}
	if token := c.values[key+":_authToken"]; token != "" {
		return Auth{Header: "Bearer " + token}
	}
	if auth := c.values[key+":_auth"]; auth != "" {
		return Auth{Header: "Basic " + auth}
	}
	username := c.values[key+":username"]
	password := c.values[key+":_password"]
	if username != "" && password != "" {
		decoded, err := base64.StdEncoding.DecodeString(password)
		if err == nil {
			password = string(decoded)
		}
		return Auth{Header: "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))}
	}
	return Auth{}
}

func (c *Config) longestAuthKey(rawURL string) string {
	nerfed := nerfDart(rawURL)
	var best string
	for key := range c.values {
		authKey, ok := strings.CutSuffix(key, ":_authToken")
		if !ok {
			authKey, ok = strings.CutSuffix(key, ":_auth")
		}
		if !ok {
			authKey, ok = strings.CutSuffix(key, ":username")
		}
		if !ok {
			authKey, ok = strings.CutSuffix(key, ":_password")
		}
		if ok && strings.HasPrefix(nerfed, authKey) && len(authKey) > len(best) {
			best = authKey
		}
	}
	return best
}

func nerfDart(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !strings.HasSuffix(path, "/") {
		idx := strings.LastIndex(path, "/")
		if idx >= 0 {
			path = path[:idx+1]
		}
	}
	return "//" + u.Host + path
}
