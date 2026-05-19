package main

import (
	"context"
)

func scan(args []string) error {
	opts, err := parseScanArgs(args)
	if err != nil {
		return err
	}
	return runScan(context.Background(), opts)
}
