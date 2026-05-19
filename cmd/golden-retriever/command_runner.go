package main

import (
	"fmt"
	"os"
)

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("missing command")
	}

	switch args[0] {
	case "fetch":
		return fetch(args[1:])
	case "mirror":
		return mirror(args[1:])
	case "push", "publish":
		return push(args[1:])
	case "resolve":
		return resolve(args[1:])
	case "scan":
		return scan(args[1:])
	case "state":
		return stateCmd(args[1:])
	case "cache":
		return cacheCmd(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `golden-retriever collects npm tarballs for air-gapped installs.

Commands:
  fetch     resolve and download every package tarball
  mirror    resolve, optionally sync target state, fetch tarballs, and push missing packages
  push      publish local tarballs missing from target registry
  scan      evaluate local tarballs and persist scan status in state
  resolve   print the resolved package tarball set
  state     manage target registry inventory state; subcommands: inspect, sync-target, mark-target
  cache     manage metadata cache; subcommands: prune, clear

Run "golden-retriever fetch -h" for flags.`)
}
