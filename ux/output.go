// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package ux

import (
	"fmt"

	"github.com/ava-labs/avalanchego/utils/logging"
	"go.uber.org/zap"
)

func Print(log logging.Logger, msg string, args ...zap.Field) {
	fmt.Println(zap.Any("", args))
	log.Info(msg, args...)
}
