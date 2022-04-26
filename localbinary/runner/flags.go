// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package runner

import (
	"flag"
	"strings"

	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	dataDirectoryKey         = "data-directory"
	logLevelKey              = "log-level"
	avalanchegoBinaryPathKey = "avalanchego-binary-path"
	cleanDataDirKey          = "clean-data-directory"
)

func buildFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("avalanche-network-runner", flag.ContinueOnError)
	addLocalBinaryFlags(fs)
	return fs
}

func addLocalBinaryFlags(fs *flag.FlagSet) {
	fs.String(dataDirectoryKey, constants.BaseDataDir, "This flag sets the data directory where the Avalanche Network Runner stores network data.")
	fs.String(logLevelKey, "info", "Sets the log level of the Avalanche Network Runner.")
	fs.String(avalanchegoBinaryPathKey, constants.AvalancheGoBinary, "Sets the path to the AvalancheGo binary.")
	fs.Bool(cleanDataDirKey, false, "If enabled, the data directory will be wiped after the network runner exits.")
}

// buildFlagSet converts a flag set into a pflag set
func buildPFlagSet(fs *flag.FlagSet) *pflag.FlagSet {
	pfs := pflag.NewFlagSet(fs.Name(), pflag.ContinueOnError)
	pfs.AddGoFlagSet(fs)

	return pfs
}

func buildViper(fs *flag.FlagSet, args []string) (*viper.Viper, error) {
	pfs := buildPFlagSet(fs)

	if err := pfs.Parse(args); err != nil {
		return nil, err
	}

	v := viper.New()
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.SetEnvPrefix("anr")
	if err := v.BindPFlags(pfs); err != nil {
		return nil, err
	}
	return v, nil
}
