package logger

import (
	"context"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"io"
	"os"
)

var (
	global       *zap.SugaredLogger
	defaultLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
)

func Init() {
	SetLogger(NewStdOut(defaultLevel))
}

func New(level zapcore.LevelEnabler, w io.Writer, options ...zap.Option) *zap.SugaredLogger {
	if level == nil {
		level = defaultLevel
	}

	cfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	enc := zapcore.NewJSONEncoder(cfg)

	return zap.New(zapcore.NewCore(enc, zapcore.AddSync(w), level), options...).Sugar()

}

func NewStdOut(level zapcore.LevelEnabler, options ...zap.Option) *zap.SugaredLogger {
	return New(level, os.Stdout, options...)
}

func SetLogger(l *zap.SugaredLogger) {
	global = l
}

type ctxLogger struct{}

func WithTraceID(ctx context.Context, logger *zap.SugaredLogger) *zap.SugaredLogger {
	sc := trace.SpanContextFromContext(ctx)

	if sc.TraceID().IsValid() {
		return logger.With("traceID", sc.TraceID().String())
	}

	return logger
}

func WithLogger(ctx context.Context, l *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, ctxLogger{}, l)
}

func FromContext(ctx context.Context) *zap.SugaredLogger {
	l := ctx.Value(ctxLogger{})
	if l != nil {
		return l.(*zap.SugaredLogger)
	}
	return global
}
