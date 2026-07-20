package logx

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New 构造一个结构化 logger。level: debug/info/warn/error。
func New(level string) (*zap.Logger, error) {
	lv := zapcore.InfoLevel
	_ = lv.UnmarshalText([]byte(level))

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lv)
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	return cfg.Build()
}

// Must 在构造失败时 panic，用于程序启动早期。
func Must(level string) *zap.Logger {
	l, err := New(level)
	if err != nil {
		panic(err)
	}
	return l
}
