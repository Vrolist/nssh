package base_core

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

var (
	currentLevel    = INFO
	logger          *CustomLogger
	once            sync.Once
	logContext      string
	httpClient      *http.Client
	httpClientOnce  sync.Once
	lokiEnabled     bool
	lokiBufferPool  = sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 1024))
		},
	}
)

// 【daemon; worker】全局变量内存占用分析：
// - currentLevel: int, 8 bytes
// - logger: 指针8B，单例实例约1-2KB
// - once: sync.Once, 8 bytes
// - logContext: string, ~32-64 bytes
// - httpClient: 指针8B，实例约1-2KB (包含连接池)
// - httpClientOnce: sync.Once, 8 bytes
// - lokiEnabled: bool, 1 byte
// - lokiBufferPool: sync.Pool, 约100-200 bytes + 缓冲区
//   每个buffer: 1KB，可能缓存多个
// 总计: 约3-6KB (全局单例)
// 生命周期: 进程启动时初始化，进程结束时释放

func GetLogger() *CustomLogger {
	once.Do(func() {
		var output io.Writer = os.Stdout
		if output == nil {
			output = io.Discard
		}
		logger = NewCustomLogger(output)
	})
	return logger
}

func GetHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		transport := &http.Transport{
			MaxIdleConns:        1,
			MaxIdleConnsPerHost:  1,
			IdleConnTimeout:      30 * time.Second,
		}
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		if lokiEnabled {
			httpClient = &http.Client{
				Timeout:   3 * time.Second,
				Transport: transport,
			}
		} else {
			httpClient = &http.Client{
				Timeout:   5 * time.Second,
				Transport: transport,
			}
		}
	})
	return httpClient
}

func SetLogContext(username, serverHost string, serverPort int) {
	logContext = fmt.Sprintf("%s:%s:%d", username, serverHost, serverPort)
}

type CustomLogger struct {
	logger     zerolog.Logger
	lokiURL     string
	lokiJob     string
	enableLoki  bool
	processType string
	lokiChan    chan struct{}
	stopChan    chan struct{}
	output      io.Writer
}

// 【daemon; worker】CustomLogger 结构体内存占用分析：
// - zerolog.Logger: 约200-500 bytes (内部缓冲区)
// - string (lokiURL): ~64 bytes
// - string (lokiJob): ~32 bytes
// - bool (enableLoki): 1 byte
// - string (processType): ~16-32 bytes
// - chan struct{}: 缓冲区4，约128 bytes
// - io.Writer: 接口，约16 bytes
// 总计: 约 500B-1KB/实例
// 生命周期: 全局单例，进程启动时创建，进程结束时释放

func NewCustomLogger(output io.Writer) *CustomLogger {
	l := &CustomLogger{
		output:     output,
		lokiJob:    "nssh",
		enableLoki: false,
		lokiChan:  make(chan struct{}, 2),
		stopChan:  make(chan struct{}),
	}

	zerolog.TimeFieldFormat = "2006/01/02 15:04:05"
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	l.logger = zerolog.New(output).Output(zerolog.ConsoleWriter{
		Out:        output,
		TimeFormat: "2006/01/02 15:04:05",
	}).With().Timestamp().Logger()

	return l
}

func (l *CustomLogger) EnableLoki(url, job string) {
	l.lokiURL = url
	if job != "" {
		l.lokiJob = job
	}
	l.enableLoki = true
	lokiEnabled = true
}

func (l *CustomLogger) SetProcessType(processType string) {
	l.processType = processType
}

func (l *CustomLogger) Stop() {
	if l.stopChan != nil {
		close(l.lokiChan)
		close(l.stopChan)
	}
}

func (l *CustomLogger) logMessage(level Level, format string, args ...interface{}) {
	if level < currentLevel {
		return
	}

	_, file, line, ok := runtime.Caller(2)
	if !ok {
		file = "unknown"
		line = 0
	}
	fileName := file
	if idx := strings.LastIndex(file, "/"); idx != -1 {
		fileName = file[idx+1:]
	}

	message := fmt.Sprintf(format, args...)

	event := l.logger.With().Str("file", fmt.Sprintf("%s:%d", fileName, line)).Logger()
	if logContext != "" {
		event = event.With().Str("context", logContext).Logger()
	}

	switch level {
	case DEBUG:
		event.Debug().Msg(message)
	case INFO:
		event.Info().Msg(message)
	case WARN:
		event.Warn().Msg(message)
	case ERROR:
		event.Error().Msg(message)
	case FATAL:
		event.Fatal().Msg(message)
	}

	if l.enableLoki && level >= ERROR {
		// ⚠️⚠️ 【内存泄漏风险 - 中等】
		// 风险等级: 🟡 40% 概率
		// 泄漏原因:
		// 1. 每个ERROR级别日志创建一个goroutine
		// 2. lokiChan缓冲区为4，最多允许4个并发goroutine
		// 3. 如果Loki服务响应慢，goroutine会积压
		// 4. HTTP请求可能超时（3秒），但期间goroutine一直存在
		// 
		// 内存占用:
		// - 最多4个并发goroutine × 2KB = 8KB
		// - 加上HTTP请求缓冲区: 约20-40KB
		// 
		// 优化建议:
		// - 减小lokiChan缓冲区到2
		// - 使用worker pool而不是每次创建goroutine
		// - 批量发送日志，而不是每条日志一个请求
		select {
		case l.lokiChan <- struct{}{}:
			go func() {
				defer func() { <-l.lokiChan }()
				select {
				case <-l.stopChan:
					return
				default:
					l.pushLogToLoki(levelToString(level), message, fileName, line)
				}
			}()
		default:
		}
	}
}

func (l *CustomLogger) Debug(format string, args ...interface{}) {
	l.logMessage(DEBUG, format, args...)
}

func (l *CustomLogger) Info(format string, args ...interface{}) {
	l.logMessage(INFO, format, args...)
}

func (l *CustomLogger) Warn(format string, args ...interface{}) {
	l.logMessage(WARN, format, args...)
}

func (l *CustomLogger) Error(format string, args ...interface{}) {
	l.logMessage(ERROR, format, args...)
}

func (l *CustomLogger) Fatal(format string, args ...interface{}) {
	l.logMessage(FATAL, format, args...)
	os.Exit(1)
}

func (l *CustomLogger) SetLevel(level Level) {
	currentLevel = level
	zerolog.SetGlobalLevel(levelToZerologLevel(level))
}

func (l *CustomLogger) WithFields(fields map[string]interface{}) *LoggedEntry {
	return &LoggedEntry{
		logger: l,
		fields: fields,
	}
}

type LoggedEntry struct {
	logger *CustomLogger
	fields map[string]interface{}
}

func (e *LoggedEntry) Debug(format string, args ...interface{}) {
	e.logger.logMessage(DEBUG, formatWithFields(format, e.fields), args...)
}

func (e *LoggedEntry) Info(format string, args ...interface{}) {
	e.logger.logMessage(INFO, formatWithFields(format, e.fields), args...)
}

func (e *LoggedEntry) Warn(format string, args ...interface{}) {
	e.logger.logMessage(WARN, formatWithFields(format, e.fields), args...)
}

func (e *LoggedEntry) Error(format string, args ...interface{}) {
	e.logger.logMessage(ERROR, formatWithFields(format, e.fields), args...)
}

func formatWithFields(format string, fields map[string]interface{}) string {
	if len(fields) == 0 {
		return format
	}

	var sb strings.Builder
	sb.WriteString(format)
	sb.WriteString(" | fields: ")

	first := true
	for k, v := range fields {
		if !first {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s=%v", k, v))
		first = false
	}

	return sb.String()
}

func levelToString(level Level) string {
	switch level {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

func levelToZerologLevel(level Level) zerolog.Level {
	switch level {
	case DEBUG:
		return zerolog.DebugLevel
	case INFO:
		return zerolog.InfoLevel
	case WARN:
		return zerolog.WarnLevel
	case ERROR:
		return zerolog.ErrorLevel
	case FATAL:
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}

func (l *CustomLogger) pushLogToLoki(level, message, file string, line int) {
	if l.lokiURL == "" {
		return
	}

	buf := lokiBufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		lokiBufferPool.Put(buf)
	}()

	logEntry := map[string]interface{}{
		"level":     level,
		"message":   message,
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"context":   logContext,
		"file":      file,
		"line":      line,
	}

	buf.Reset()
	if err := json.NewEncoder(buf).Encode(logEntry); err != nil {
		return
	}

	timestamp := time.Now().UnixNano()
	stream := map[string]string{"job": l.lokiJob}
	if l.processType != "" {
		stream["process_type"] = l.processType
	}
	lokiData := map[string]interface{}{
		"streams": []map[string]interface{}{
			{
				"stream": stream,
				"values": [][]string{{fmt.Sprintf("%d", timestamp), buf.String()}},
			},
		},
	}

	buf.Reset()
	if err := json.NewEncoder(buf).Encode(lokiData); err != nil {
		return
	}

	req, err := http.NewRequest("POST", l.lokiURL, bytes.NewBuffer(buf.Bytes()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := GetHTTPClient().Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}
