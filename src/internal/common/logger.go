package common

import (
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.SugaredLogger

func init() {
	InitLogger()
}

func InitLogger() {
	config := zap.NewDevelopmentConfig()
	var level zapcore.Level
	if viper.IsSet("loglevel") {
		err := level.UnmarshalText([]byte(viper.GetString("loglevel")))
		if err == nil {
			config.Level.SetLevel(level)
		} else {
			config.Level.SetLevel(zapcore.Level(viper.GetInt("loglevel")))
		}
	} else if viper.IsSet("log_level") {
		err := level.UnmarshalText([]byte(viper.GetString("log_level")))
		if err == nil {
			config.Level.SetLevel(level)
		} else {
			config.Level.SetLevel(zapcore.Level(viper.GetInt("log_level")))
		}
	} else {
		config.Level.SetLevel(zapcore.InfoLevel)
	}
	// fmt.Printf("Log level set to %s\n", config.Level.Level().String())
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	zapLogger, err := config.Build()
	// defer func() { _ = zapLogger.Sync() }()
	if err != nil {
		panic(err)
	}
	Logger = zapLogger.Sugar()
}

// Logs an error and panics
func ReportError(err error, msg string) {
	if err != nil {
		Logger.Error(msg, " error: ", err)
	}
}
