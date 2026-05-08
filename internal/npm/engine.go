package npm

import "fmt"

type PackageEngineError struct {
	Package     string
	Engine      string
	Wanted      string
	Current     string
	EngineField map[string]string
}

func (e *PackageEngineError) Error() string {
	return fmt.Sprintf("%s requires %s@%s, current %s", e.Package, e.Engine, e.Wanted, e.Current)
}

func engineCompatible(manifest VersionManifest, opts ResolveOptions) (bool, *PackageEngineError) {
	if len(manifest.Engines) == 0 || opts.NodeVersion == "" {
		return true, nil
	}
	wanted := manifest.Engines["node"]
	if wanted == "" || satisfies(opts.NodeVersion, wanted) {
		return true, nil
	}
	return false, &PackageEngineError{
		Package:     manifest.Name + "@" + manifest.Version,
		Engine:      "node",
		Wanted:      wanted,
		Current:     opts.NodeVersion,
		EngineField: manifest.Engines,
	}
}
