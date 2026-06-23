package base_core

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// BuildVersion 由 main 包注入，记录客户端版本号
var BuildVersion = "dev"

type Config struct {
	ServerHost       string
	ServerPort       int
	Username         string
	Password         string
	SSHKeyPath       string
	LocalHost        string
	LocalPort        int
	RemotePort       int
	ReconnectDelay   int
	MaxOfflineCount  int
	Version          string
}

// 【daemon; worker】Config 结构体内存占用分析：
// - string (ServerHost): ~16-128 bytes
// - int (ServerPort): 8 bytes
// - string (Username): ~16-64 bytes
// - string (Password): ~16-128 bytes
// - string (SSHKeyPath): ~16-256 bytes
// - string (LocalHost): ~16-32 bytes
// - int (LocalPort): 8 bytes
// - int (RemotePort): 8 bytes
// - int (ReconnectDelay): 8 bytes
// 总计: 约 120-700 bytes/实例
// 生命周期: 
//   - daemon: 在ProcessInfo中保存，随worker生命周期
//   - worker: 进程启动时创建，进程结束时释放

func LoadConfig(reverseTunnel string, port int, password string, sshKey string, reconnectDelay int) *Config {
	config := &Config{
		ServerHost:     getEnv("SERVER_HOST", "0.0.0.0"),
		ServerPort:     getEnvInt("SERVER_PORT", 20022),
		Username:       getEnv("USERNAME", ""),
		Password:       getEnv("PASSWORD", ""),
		SSHKeyPath:     getEnv("SSH_KEY", ""),
		LocalHost:      "localhost",
		LocalPort:      getEnvInt("LOCAL_PORT", 80),
		RemotePort:     getEnvInt("REMOTE_PORT", 8000),
		ReconnectDelay: getEnvInt("RECONNECT_DELAY", 30),
	}

	args := flag.Args()
	if len(args) > 0 {
		userHost := args[0]
		if strings.Contains(userHost, "@") {
			parts := strings.SplitN(userHost, "@", 2)
			config.Username = parts[0]
			config.ServerHost = parts[1]
		} else {
			config.ServerHost = userHost
		}
	}

	if reverseTunnel != "" {
		parts := strings.Split(reverseTunnel, ":")
		if len(parts) == 3 {
			remotePort, err := strconv.Atoi(parts[0])
			if err != nil {
				log.Fatalf("Invalid remote port: %v", err)
			}
			config.RemotePort = remotePort
			config.LocalHost = parts[1]
			localPort, err := strconv.Atoi(parts[2])
			if err != nil {
				log.Fatalf("Invalid local port: %v", err)
			}
			config.LocalPort = localPort
		} else {
			log.Fatalf("Invalid reverse tunnel format. Expected: remote_port:local_host:local_port")
		}
	}

	if port > 0 {
		config.ServerPort = port
	}

	if password != "" {
		config.Password = password
	}

	if sshKey != "" {
		config.SSHKeyPath = sshKey
	}

	if reconnectDelay > 0 {
		config.ReconnectDelay = reconnectDelay
	}

	config.Version = BuildVersion

	return config
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var intVal int
		if _, err := fmt.Sscanf(value, "%d", &intVal); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func LoadConfigFromEnv() *Config {
	return &Config{
		ServerHost:      getEnv("NSSH_SERVER_HOST", ""),
		ServerPort:      getEnvInt("NSSH_SERVER_PORT", 0),
		Username:        getEnv("NSSH_USERNAME", ""),
		Password:        getEnv("NSSH_PASSWORD", ""),
		SSHKeyPath:      getEnv("NSSH_SSH_KEY", ""),
		LocalHost:       getEnv("NSSH_LOCAL_HOST", "localhost"),
		LocalPort:       getEnvInt("NSSH_LOCAL_PORT", 0),
		RemotePort:      getEnvInt("NSSH_REMOTE_PORT", 0),
		ReconnectDelay:  getEnvInt("RECONNECT_DELAY", 60),
		MaxOfflineCount: getEnvInt("MAX_OFFLINE_COUNT", 14400),
		Version:         BuildVersion,
	}
}

func IsValidDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
		for _, r := range part {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || 
				(r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}
