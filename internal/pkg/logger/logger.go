package logger

import (
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	log  *zap.Logger
	once sync.Once
)

// InitLogger 初始化 Zap 日志库
// outputPath: 日志文件路径，例如 "logs/app.log"
// errorPath: 错误日志文件路径，例如 "logs/error.log"
// level: 日志级别 (debug, info, warn, error, dpanic, panic, fatal)
func InitLogger(outputPath, errorPath string, level string) {
	once.Do(func() {
		var l zapcore.Level
		var err error
		if err = l.UnmarshalText([]byte(level)); err != nil {
			l = zap.InfoLevel // 默认 INFO 级别
			fmt.Fprintf(os.Stderr, "Failed to parse log level '%s', defaulting to info: %v\n", level, err)
		}

		// 创建生产环境配置
		cfg := zap.NewProductionConfig()

		cfg.OutputPaths = []string{outputPath, "stdout"}
		cfg.ErrorOutputPaths = []string{errorPath, "stderr"}
		cfg.Encoding = "json"
		cfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

		log, err = cfg.Build()
		if err != nil {
			panic(fmt.Sprintf("Failed to build zap logger: %v", err))
		}
		zap.ReplaceGlobals(log)
	})
}

// 返回全局logger
func GetLogger() *zap.Logger {
	if log == nil {
		// 如果在调用 InitLogger 之前调用 GetLogger，则初始化一个默认 logger
		// 生产环境中应确保 InitLogger 在应用启动时被调用
		InitLogger("stdout", "stderr", "info")
	}
	return log
}

// Sugar 返回 Zap 的 SugaredLogger，它提供了更灵活的 API (类似 fmt.Printf)
// 适合性能很好但不是很关键的上下文中
func Sugar() *zap.SugaredLogger {
	return GetLogger().Sugar()
}

// 刷新缓冲区,确保程序退出前使用
func Sync() {
	if log != nil {
		if err := log.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to sync zap logger: %v\n", err)
		}
	}
}

// 为方便使用，可以封装常用的日志方法
func Debug(msg string, fields ...zap.Field) {
	GetLogger().Debug(msg, fields...)
}

func Info(msg string, fields ...zap.Field) {
	GetLogger().Info(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	GetLogger().Warn(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	GetLogger().Error(msg, fields...)
}

func Fatal(msg string, fields ...zap.Field) {
	GetLogger().Fatal(msg, fields...)
}
