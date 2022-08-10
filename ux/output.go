// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package ux

import (
	"fmt"

	"github.com/ava-labs/avalanchego/utils/logging"
)

func Print(log logging.Logger, msg string, args ...interface{}) {
	fmt.Println(fmt.Sprintf(msg, args...))
	log.Info(msg, args)
}
