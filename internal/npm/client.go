package npm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Client struct {
	Registry          string
	HTTPClient        *http.Client
	Config            *Config
	CacheDir          string
	CacheTTL          time.Duration
	Offline           bool
	PackumentRetries  int
	UseStaleOnFailure bool

	mu         sync.Mutex
	packuments map[string]*Packument
	inflight   map[string]*packumentCall
}

type packumentCall struct {
	done chan struct{}
	pack *Packument
	err  error
}

func NewClient(registry string) *Client {
	cfg := DefaultConfig()
	if registry != "" {
		cfg.Registry = strings.TrimRight(registry, "/")
	}
	return &Client{
		Registry: cfg.Registry,
		Config:   cfg,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		CacheTTL:          24 * time.Hour,
		PackumentRetries:  3,
		UseStaleOnFailure: true,
		packuments:        map[string]*Packument{},
		inflight:          map[string]*packumentCall{},
	}
}

func NewClientWithConfig(cfg *Config) *Client {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	client := NewClient(cfg.Registry)
	client.Config = cfg
	client.Registry = cfg.Registry
	return client
}

type Packument struct {
	Name     string                     `json:"name"`
	DistTags map[string]string          `json:"dist-tags"`
	Versions map[string]VersionManifest `json:"versions"`
}

type VersionManifest struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	BundleDependencies   dependencyBundle  `json:"bundleDependencies"`
	BundledDependencies  dependencyBundle  `json:"bundledDependencies"`
	OS                   stringList        `json:"os"`
	CPU                  stringList        `json:"cpu"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	PeerDependenciesMeta map[string]struct {
		Optional bool `json:"optional"`
	} `json:"peerDependenciesMeta"`
	Dist struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
		Shasum    string `json:"shasum"`
	} `json:"dist"`
	Deprecated any `json:"deprecated"`
}

func (c *Client) Packument(ctx context.Context, name string) (*Packument, error) {
	c.mu.Lock()
	if p, ok := c.packuments[name]; ok {
		c.mu.Unlock()
		return p, nil
	}
	if call, ok := c.inflight[name]; ok {
		c.mu.Unlock()
		<-call.done
		return call.pack, call.err
	}
	call := &packumentCall{done: make(chan struct{})}
	c.inflight[name] = call
	c.mu.Unlock()

	cached, err := c.readCachedPackument(name)
	if err != nil {
		c.finishPackument(name, call, nil, err)
		return nil, err
	}
	if cached != nil && c.cacheFresh(cached) {
		c.finishPackument(name, call, &cached.Packument, nil)
		return &cached.Packument, nil
	}
	if c.Offline {
		if cached != nil {
			c.finishPackument(name, call, &cached.Packument, nil)
			return &cached.Packument, nil
		}
		err := fmt.Errorf("packument %s: not present in metadata cache", name)
		c.finishPackument(name, call, nil, err)
		return nil, err
	}

	registry := c.registryForPackage(name)
	escaped := url.PathEscape(name)
	escaped = strings.ReplaceAll(escaped, "%40", "@")
	reqURL := registry + "/" + escaped
	p, err := c.fetchPackumentWithRetries(ctx, name, reqURL, registry, cached)
	if err != nil {
		if cached != nil && c.UseStaleOnFailure {
			c.finishPackument(name, call, &cached.Packument, nil)
			return &cached.Packument, nil
		}
		c.finishPackument(name, call, nil, err)
		return nil, err
	}

	c.finishPackument(name, call, p, nil)
	return p, nil
}

func (c *Client) fetchPackumentWithRetries(ctx context.Context, name, reqURL, registry string, cached *cachedPackument) (*Packument, error) {
	retries := c.PackumentRetries
	if retries < 0 {
		retries = 0
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		pack, err := c.fetchPackument(ctx, name, reqURL, registry, cached)
		if err == nil {
			return pack, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == retries {
			break
		}
		backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return nil, lastErr
}

func (c *Client) fetchPackument(ctx context.Context, name, reqURL, registry string, cached *cachedPackument) (*Packument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json, application/json")
	if cached != nil {
		if cached.ETag != "" {
			req.Header.Set("If-None-Match", cached.ETag)
		}
		if cached.LastModified != "" {
			req.Header.Set("If-Modified-Since", cached.LastModified)
		}
	}
	c.applyAuth(req)

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		if cached == nil {
			return nil, fmt.Errorf("packument %s: registry returned %s without cached metadata", name, res.Status)
		}
		cached.CachedAt = time.Now().UTC()
		if err := c.writeCachedPackumentRecord(cached); err != nil {
			return nil, err
		}
		return &cached.Packument, nil
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("packument %s: registry returned %w", name, httpStatusError{StatusCode: res.StatusCode, Status: res.Status})
	}

	var p Packument
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("decode packument %s: %w", name, err)
	}
	if p.Name == "" {
		p.Name = name
	}
	if err := c.writeCachedPackument(name, registry, res.Header.Get("ETag"), res.Header.Get("Last-Modified"), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) registryForPackage(name string) string {
	if c.Config != nil {
		return strings.TrimRight(c.Config.RegistryForPackage(name), "/")
	}
	if c.Registry == "" {
		return DefaultRegistry
	}
	return strings.TrimRight(c.Registry, "/")
}

func (c *Client) applyAuth(req *http.Request) {
	if c.Config == nil {
		return
	}
	if auth := c.Config.AuthFor(req.URL.String()); auth.Header != "" {
		req.Header.Set("Authorization", auth.Header)
	}
}

func (c *Client) cachePath(name, registry string) string {
	if c.CacheDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(registry + "\n" + name))
	return filepath.Join(c.CacheDir, hex.EncodeToString(sum[:])+".json")
}

type cachedPackument struct {
	Registry     string    `json:"registry"`
	Name         string    `json:"name"`
	CachedAt     time.Time `json:"cachedAt"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"lastModified,omitempty"`
	Packument    Packument `json:"packument"`
}

func (c *Client) cacheFresh(cached *cachedPackument) bool {
	if cached == nil {
		return false
	}
	if c.CacheTTL < 0 {
		return true
	}
	if c.CacheTTL == 0 {
		return false
	}
	return time.Since(cached.CachedAt) <= c.CacheTTL
}

func (c *Client) readCachedPackument(name string) (*cachedPackument, error) {
	registry := c.registryForPackage(name)
	path := c.cachePath(name, registry)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cached cachedPackument
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	if cached.Registry != registry || cached.Name != name {
		return nil, nil
	}
	return &cached, nil
}

func (c *Client) writeCachedPackument(name, registry, etag, lastModified string, pack *Packument) error {
	return c.writeCachedPackumentRecord(&cachedPackument{
		Registry:     registry,
		Name:         name,
		CachedAt:     time.Now().UTC(),
		ETag:         etag,
		LastModified: lastModified,
		Packument:    *pack,
	})
}

func (c *Client) writeCachedPackumentRecord(cached *cachedPackument) error {
	if cached == nil {
		return nil
	}
	path := c.cachePath(cached.Name, cached.Registry)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (c *Client) finishPackument(name string, call *packumentCall, pack *Packument, err error) {
	c.mu.Lock()
	if pack != nil && err == nil {
		c.packuments[name] = pack
	}
	call.pack = pack
	call.err = err
	delete(c.inflight, name)
	close(call.done)
	c.mu.Unlock()
}
