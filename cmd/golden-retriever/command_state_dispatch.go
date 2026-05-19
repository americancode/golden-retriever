package main

import "fmt"

func stateCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing state subcommand")
	}
	switch args[0] {
	case "inspect":
		return stateInspect(args[1:])
	case "mark-target":
		return stateMarkTarget(args[1:])
	case "sync-target":
		return stateSyncTarget(args[1:])
	default:
		return fmt.Errorf("unknown state subcommand %q", args[0])
	}
}
