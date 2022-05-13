// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// Package logutil implements various log utilities.
package logutil

import (
	"fmt"
	"strings"
)

// DefaultNodeLogLevel is the default node log level.
var DefaultNodeLogLevel = "info"

func AvalanchegoToCorethLogLevel(logLevel string) (string, error) {
	switch strings.ToUpper(logLevel) {
	case "VERBO":
		return "trace", nil
	case "DEBUG":
		return "debug", nil
	case "TRACE":
		return "debug", nil
	case "INFO":
		return "info", nil
	case "WARN":
		return "warn", nil
	case "ERROR":
		return "error", nil
	case "FATAL":
		return "crit", nil
	case "OFF":
		return "crit", nil
	default:
		return "", fmt.Errorf("unknown error level %q", logLevel)
	}
}
