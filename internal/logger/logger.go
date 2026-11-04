package logger

import (
	"log/slog"
	"os"

	"github.com/google/uuid"
)

func Init() {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	handler := slog.NewJSONHandler(os.Stdout, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)
}

func NewTraceID() string {
	return uuid.NewString()
}
