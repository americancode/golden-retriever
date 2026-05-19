package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"golden-retriever/internal/npm"
)

func stateInspect(args []string) error {
	fs := flag.NewFlagSet("state inspect", flag.ExitOnError)
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	validate := fs.Bool("validate-files", false, "verify local tarballs and remove invalid local records")
	if err := fs.Parse(args); err != nil {
		return err
	}
	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	var validation npm.StateValidationReport
	if *validate {
		validation = npm.ValidateStateFiles(state)
		if err := npm.SaveState(*statePath, state); err != nil {
			return err
		}
	}
	summary := npm.SummarizeState(state)
	if *jsonOut {
		payload := struct {
			npm.StateSummary
			Validation npm.StateValidationReport `json:"validation,omitempty"`
			StatePath  string                    `json:"statePath"`
		}{
			StateSummary: summary,
			Validation:   validation,
			StatePath:    *statePath,
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("state=%s schema=%d target=%d local=%d failures=%d updated=%s\n",
		*statePath, summary.SchemaVersion, summary.Target, summary.Local, summary.Failures, summary.UpdatedAt.Format(time.RFC3339))
	if *validate {
		fmt.Printf("validation checked_local=%d valid_local=%d removed_local=%d\n",
			validation.CheckedLocal, validation.ValidLocal, validation.RemovedLocal)
	}
	return nil
}
