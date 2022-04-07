package main

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func initLogger() (*zap.Logger, error) {
	loggerConfig := zap.NewDevelopmentConfig()
	loggerConfig.DisableStacktrace = true
	loggerConfig.EncoderConfig.TimeKey = "timestamp"
	loggerConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	loggerConfig.EncoderConfig.MessageKey = "message"
	loggerConfig.Level = zap.NewAtomicLevel()
	loggerConfig.Level.SetLevel(zapcore.InfoLevel)

	logger, err := loggerConfig.Build()
	if err != nil {
		fmt.Println("Initialize logger failed")
		return nil, err
	}

	logger.Info("logger initialized")

	return logger, nil
}
