package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	Registry   string
	HTTPClient *http.Client

	mu          sync.Mutex
	packuments  map[string]*Packument
	inflight    map[string]*packumentCall
}

type packumentCall struct {
	done chan struct{}
	pack *Packument
	err  error
}

func NewClient(registry string) *Client {
	return &Client{
		Registry: strings.TrimRight(registry, "/"),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		packuments: map[string]*Packument{},
		inflight:   map[string]*packumentCall{},
	}
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

	escaped := url.PathEscape(name)
	escaped = strings.ReplaceAll(escaped, "%40", "@")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Registry+"/"+escaped, nil)
	if err != nil {
		c.finishPackument(name, call, nil, err)
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json, application/json")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		c.finishPackument(name, call, nil, err)
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		err := fmt.Errorf("packument %s: registry returned %s", name, res.Status)
		c.finishPackument(name, call, nil, err)
		return nil, err
	}

	var p Packument
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		err = fmt.Errorf("decode packument %s: %w", name, err)
		c.finishPackument(name, call, nil, err)
		return nil, err
	}
	if p.Name == "" {
		p.Name = name
	}

	c.finishPackument(name, call, &p, nil)
	return &p, nil
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
