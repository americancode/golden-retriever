package npm

import "sort"

func sortPackages(pkgs []Package) {
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Name == pkgs[j].Name {
			return compareVersion(pkgs[i].Version, pkgs[j].Version) < 0
		}
		return pkgs[i].Name < pkgs[j].Name
	})
}
