package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type PublishOptions struct {
	Concurrency int
	Source      string
	Tag         string
	Access      string
}

type PublishReport struct {
	Pushed  int
	Skipped int
	Present int
	Failed  int
}

type publishManifest struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description,omitempty"`
	Private     bool           `json:"private,omitempty"`
	Dist        map[string]any `json:"dist,omitempty"`
	Raw         map[string]any `json:"-"`
}

func PublishAll(ctx context.Context, target *Client, state *State, opts PublishOptions) (PublishReport, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.Source == "" {
		opts.Source = "target-publish"
	}
	if opts.Tag == "" {
		opts.Tag = "latest"
	}
	if opts.Access == "" {
		opts.Access = "public"
	}
	normalizeState(state)

	jobs := make(chan StateRecord)
	var stateMu sync.Mutex
	var reportMu sync.Mutex
	var report PublishReport
	var firstErr error
	var wg sync.WaitGroup

	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rec := range jobs {
				result, pkg, err := publishOne(ctx, target, rec, opts)
				reportMu.Lock()
				if err != nil {
					report.Failed++
					if firstErr == nil {
						firstErr = err
					}
				} else if result == publishPushed {
					report.Pushed++
					stateMu.Lock()
					MarkTargetPresent(state, pkg, opts.Source)
					stateMu.Unlock()
				} else if result == publishPresent {
					report.Present++
					stateMu.Lock()
					MarkTargetPresent(state, pkg, opts.Source)
					stateMu.Unlock()
				} else {
					report.Skipped++
				}
				reportMu.Unlock()
			}
		}()
	}

	for key, rec := range state.Local {
		if _, ok := state.Target[key]; ok {
			report.Skipped++
			continue
		}
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return report, ctx.Err()
		case jobs <- rec:
		}
	}
	close(jobs)
	wg.Wait()
	state.UpdatedAt = time.Now().UTC()
	return report, firstErr
}

type publishResult int

const (
	publishPushed publishResult = iota
	publishPresent
	publishSkipped
)

func publishOne(ctx context.Context, target *Client, rec StateRecord, opts PublishOptions) (publishResult, Package, error) {
	if rec.Path == "" {
		return publishSkipped, Package{}, nil
	}
	tarballData, err := os.ReadFile(rec.Path)
	if err != nil {
		return publishSkipped, Package{}, err
	}
	manifest, err := manifestFromTarball(tarballData)
	if err != nil {
		return publishSkipped, Package{}, err
	}
	if manifest.Private {
		return publishSkipped, Package{}, fmt.Errorf("%s@%s is private and cannot be published", manifest.Name, manifest.Version)
	}
	if rec.Name != "" && manifest.Name != rec.Name {
		return publishSkipped, Package{}, fmt.Errorf("state package %s does not match tarball manifest %s", rec.Name, manifest.Name)
	}
	if rec.Version != "" && manifest.Version != rec.Version {
		return publishSkipped, Package{}, fmt.Errorf("state version %s does not match tarball manifest %s", rec.Version, manifest.Version)
	}

	doc, pkg, err := buildPublishDocument(target.registryForPackage(manifest.Name), manifest, tarballData, opts)
	if err != nil {
		return publishSkipped, Package{}, err
	}
	endpoint := publishEndpoint(target.registryForPackage(manifest.Name), manifest.Name)
	body, err := json.Marshal(doc)
	if err != nil {
		return publishSkipped, Package{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return publishSkipped, Package{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	target.applyAuth(req)
	res, err := target.HTTPClient.Do(req)
	if err != nil {
		return publishSkipped, Package{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		return publishPresent, pkg, nil
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return publishSkipped, Package{}, fmt.Errorf("%s: publish returned %w", pkg.Key(), httpStatusError{StatusCode: res.StatusCode, Status: res.Status})
	}
	return publishPushed, pkg, nil
}

func manifestFromTarball(tarballData []byte) (publishManifest, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarballData))
	if err != nil {
		return publishManifest{}, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return publishManifest{}, err
		}
		if path.Clean(header.Name) != "package/package.json" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return publishManifest{}, err
		}
		raw := map[string]any{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return publishManifest{}, err
		}
		manifest := publishManifest{Raw: raw}
		if name, _ := raw["name"].(string); name != "" {
			manifest.Name = name
		}
		if version, _ := raw["version"].(string); version != "" {
			manifest.Version = version
		}
		manifest.Description, _ = raw["description"].(string)
		manifest.Private, _ = raw["private"].(bool)
		if dist, _ := raw["dist"].(map[string]any); dist != nil {
			manifest.Dist = dist
		}
		if manifest.Name == "" || manifest.Version == "" {
			return publishManifest{}, fmt.Errorf("tarball package.json missing name or version")
		}
		return manifest, nil
	}
	return publishManifest{}, fmt.Errorf("tarball missing package/package.json")
}

func buildPublishDocument(registry string, manifest publishManifest, tarballData []byte, opts PublishOptions) (map[string]any, Package, error) {
	sha512Sum := sha512.Sum512(tarballData)
	sha1Sum := sha1.Sum(tarballData)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])
	shasum := hex.EncodeToString(sha1Sum[:])
	tarballName := manifest.Name + "-" + manifest.Version + ".tgz"
	tarballURL := strings.TrimRight(registry, "/") + "/" + manifest.Name + "/-/" + tarballName
	if strings.HasPrefix(tarballURL, "https://") {
		tarballURL = "http://" + strings.TrimPrefix(tarballURL, "https://")
	}

	versionManifest := map[string]any{}
	for key, value := range manifest.Raw {
		versionManifest[key] = value
	}
	versionManifest["_id"] = manifest.Name + "@" + manifest.Version
	dist := map[string]any{}
	if manifest.Dist != nil {
		for key, value := range manifest.Dist {
			dist[key] = value
		}
	}
	dist["integrity"] = integrity
	dist["shasum"] = shasum
	dist["tarball"] = tarballURL
	versionManifest["dist"] = dist

	doc := map[string]any{
		"_id":         manifest.Name,
		"name":        manifest.Name,
		"description": manifest.Description,
		"dist-tags": map[string]string{
			opts.Tag: manifest.Version,
		},
		"versions": map[string]any{
			manifest.Version: versionManifest,
		},
		"access": opts.Access,
		"_attachments": map[string]any{
			tarballName: map[string]any{
				"content_type": "application/octet-stream",
				"data":         base64.StdEncoding.EncodeToString(tarballData),
				"length":       len(tarballData),
			},
		},
	}
	pkg := Package{
		Name:      manifest.Name,
		Version:   manifest.Version,
		Tarball:   tarballURL,
		Integrity: integrity,
		Shasum:    shasum,
	}
	return doc, pkg, nil
}

func publishEndpoint(registry, name string) string {
	escaped := url.PathEscape(name)
	escaped = strings.ReplaceAll(escaped, "%40", "@")
	return strings.TrimRight(registry, "/") + "/" + escaped
}
