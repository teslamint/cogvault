package main

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/index"
)

func handleConsistencyResult(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, index.ErrConsistencySystemic) {
		return err
	}
	cmd.PrintErrln("warning: some files had errors during consistency check:", err)
	return nil
}
