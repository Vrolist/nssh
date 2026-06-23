package sdk_go

import (
	"log"
	"os"
)

type Logger struct {
	logger *log.Logger
}

func NewLogger() *Logger {
	return &Logger{
		logger: log.New(os.Stdout, "", 0),
	}
}

func (l *Logger) Write(p []byte) (n int, err error) {
	l.logger.Print(string(p))
	return len(p), nil
}

func (l *Logger) Printf(format string, args ...interface{}) {
	l.logger.Printf(format, args...)
}
