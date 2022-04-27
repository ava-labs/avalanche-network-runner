// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// Package logutil implements various log utilities.
package logutil

import (
	"log"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var DefaultLogLevel = zapcore.InfoLevel

func init() {
	logger, err := GetDefaultZapLogger()
	if err != nil {
		log.Fatalf("Failed to initialize global logger, %v", err)
	}
	_ = zap.ReplaceGlobals(logger)
}

// GetDefaultZapLoggerConfig returns a new default zap logger configuration.
func GetDefaultZapLoggerConfig() zap.Config {
	return zap.Config{
		Level: zap.NewAtomicLevelAt(DefaultLogLevel),

		Development: false,
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},

		Encoding: "json",

		// copied from "zap.NewProductionEncoderConfig" with some updates
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},

		// Use "/dev/null" to discard all
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}
}

// GetDefaultZapLogger returns a new default logger.
func GetDefaultZapLogger() (*zap.Logger, error) {
	lcfg := GetDefaultZapLoggerConfig()
	return lcfg.Build()
}
