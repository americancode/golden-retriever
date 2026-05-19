package main

import (
	"flag"
	"fmt"

	"golden-retriever/internal/npm"
)

func stateMarkTarget(args []string) error {
	fs := flag.NewFlagSet("state mark-target", flag.ExitOnError)
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	pkgKey := fs.String("package", "", "package version as name@version")
	integrity := fs.String("integrity", "", "known package integrity")
	shasum := fs.String("shasum", "", "known package sha1 shasum")
	tarball := fs.String("tarball", "", "source tarball URL")
	source := fs.String("source", "manual", "inventory source label")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, version, err := splitPackageKey(*pkgKey)
	if err != nil {
		return err
	}
	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	npm.MarkTargetPresent(state, npm.Package{
		Name: name, Version: version, Tarball: *tarball,
		Integrity: *integrity, Shasum: *shasum,
	}, *source)
	if err := npm.SaveState(*statePath, state); err != nil {
		return err
	}
	fmt.Printf("marked target_present package=%s@%s state=%s\n", name, version, *statePath)
	return nil
}
