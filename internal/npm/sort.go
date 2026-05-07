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

func sortNodes(nodes []*Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Name == nodes[j].Name {
			return compareVersion(nodes[i].Version, nodes[j].Version) < 0
		}
		return nodes[i].Name < nodes[j].Name
	})
}
