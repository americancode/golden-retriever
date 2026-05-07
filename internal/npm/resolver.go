package npm

import (
	"context"
	"fmt"
	"sync"
)

type Resolver struct {
	Client  *Client
	Options ResolveOptions

	mu       sync.Mutex
	graph    *Graph
	inflight map[string]bool
	fetchSem chan struct{}
}

func (r *Resolver) Resolve(ctx context.Context, deps map[string]string) (*Graph, error) {
	r.graph = NewGraph()
	r.inflight = map[string]bool{}
	if r.Options.ResolveConcurrency <= 0 {
		r.Options.ResolveConcurrency = 32
	}
	r.fetchSem = make(chan struct{}, r.Options.ResolveConcurrency)
	if err := r.resolveDeps(ctx, deps); err != nil {
		return nil, err
	}
	return r.graph, nil
}

func (r *Resolver) resolveDeps(ctx context.Context, deps map[string]string) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(deps))
	for name, spec := range deps {
		name, spec := name, spec
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.resolveDep(ctx, name, spec); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Resolver) resolveDep(ctx context.Context, name, spec string) error {
	actualName, wanted, err := parsePackageSpec(name, spec)
	if err != nil {
		return err
	}
	if err := r.acquireFetchSlot(ctx); err != nil {
		return err
	}
	pack, err := r.Client.Packument(ctx, actualName)
	r.releaseFetchSlot()
	if err != nil {
		return err
	}
	version, err := pickVersion(pack, wanted)
	if err != nil {
		return err
	}
	key := actualName + "@" + version

	r.mu.Lock()
	if r.graph.Has(key) || r.inflight[key] {
		r.mu.Unlock()
		return nil
	}
	r.inflight[key] = true
	r.mu.Unlock()

	manifest, ok := pack.Versions[version]
	if !ok {
		return fmt.Errorf("%s@%s missing from packument", actualName, version)
	}
	pkgName := manifest.Name
	if pkgName == "" {
		pkgName = actualName
	}
	pkgVersion := manifest.Version
	if pkgVersion == "" {
		pkgVersion = version
	}

	r.mu.Lock()
	r.graph.Add(Package{
		Name:      pkgName,
		Version:   pkgVersion,
		Tarball:   manifest.Dist.Tarball,
		Integrity: manifest.Dist.Integrity,
		Shasum:    manifest.Dist.Shasum,
	})
	r.mu.Unlock()

	childDeps := map[string]string{}
	mergeDeps(childDeps, manifest.Dependencies)
	if r.Options.IncludeOptional {
		mergeDeps(childDeps, manifest.OptionalDependencies)
	}
	if err := r.resolveDeps(ctx, childDeps); err != nil {
		return fmt.Errorf("%s@%s dependency: %w", pkgName, pkgVersion, err)
	}
	return nil
}

func (r *Resolver) acquireFetchSlot(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.fetchSem <- struct{}{}:
		return nil
	}
}

func (r *Resolver) releaseFetchSlot() {
	<-r.fetchSem
}
