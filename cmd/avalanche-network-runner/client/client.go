// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import "github.com/spf13/cobra"

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "start network",
		RunE:  startFunc,
	}

	return cmd
}

// TODO: define and implement the CLI interface
// do we just care about a single network?
func startFunc(cmd *cobra.Command, args []string) error {
	return nil
}
