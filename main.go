package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/pflag"

	"nssh/base_core"
	"nssh/base_tunnel"
	"nssh/daemon"
	"nssh/platform"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	CommitID  = "unknown"
)

func main() {
	// runtime.GOMAXPROCS(1)
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 注入版本号，供 base_core 和 ClientVersion 使用
	base_core.BuildVersion = Version

	// ⚠️⚠️⚠️ 【重要提示 - Go Runtime 内存管理】
	// Go的内存管理特点:
	// 1. VmRSS包括: 堆内存 + 栈内存 + 数据段 + 共享库
	// 2. Go不会立即释放内存给操作系统
	// 3. 即使对象被GC回收，内存仍被Go runtime持有
	// 4. 需要手动触发GC或等待runtime自动释放
	// 
	// 这意味着:
	// - VmRSS高不代表实际内存泄漏
	// - 需要查看 runtime.MemStats.Alloc 而不是 VmRSS
	// - 如果 Alloc << VmRSS，说明大部分是Go runtime持有，不是泄漏
	// 
	// 建议添加内存监控:
	// var m runtime.MemStats
	// runtime.ReadMemStats(&m)
	// logger.Info("Memory: Alloc=%dMB Sys=%dMB", m.Alloc/1024/1024, m.Sys/1024/1024)

	versionCmd := pflag.BoolP("version", "v", false, "Show version information")
	daemonMode := pflag.Bool("daemon", false, "Run as daemon")
	daemonInnerMode := pflag.Bool("daemon-inner", false, "Internal: run daemon in foreground")
	listCmd := pflag.Bool("list", false, "List processes (no value for all, or specify username as argument)")
	restartCmd := pflag.String("restart", "", "Restart process by username")
	stopCmd := pflag.String("stop", "", "Stop process by username")
	logCmd := pflag.String("log", "", "View log by username")
	stopAllCmd := pflag.Bool("stop-all", false, "Stop all background processes")
	restartAllCmd := pflag.Bool("restart-all", false, "Restart all background processes")
	takeoverCmd := pflag.Bool("takeover", false, "Takeover running nssh version")
	takeoverForceCmd := pflag.Bool("takeover-force", false, "Force takeover without confirmation")

	var reverseTunnel string
	pflag.StringVarP(&reverseTunnel, "remote", "R", "", "Reverse tunnel binding (remote_port:local_host:local_port)")

	var port int
	pflag.IntVarP(&port, "port", "p", 0, "SSH server port")

	var password string
	pflag.StringVarP(&password, "passwd", "P", "", "SSH password")

	var sshKey string
	pflag.StringVarP(&sshKey, "identity", "i", "", "SSH private key path")

	var reconnectDelay int
	pflag.IntVarP(&reconnectDelay, "reconnect", "r", 30, "Reconnect delay in seconds")

	pflag.Parse()

	if *daemonInnerMode {
		runDaemonInner()
		return
	}

	if *takeoverCmd || *takeoverForceCmd {
		if *takeoverCmd && *takeoverForceCmd {
			log.Fatal("Cannot use both --takeover and --takeover-force")
		}
		handleTakeover(*takeoverForceCmd)
		return
	}

	if *daemonMode && reverseTunnel != "" {
		config := base_core.LoadConfig(reverseTunnel, port, password, sshKey, reconnectDelay)

		username, serverHost, serverPort := parsePositionalArgs()
		if username != "" {
			config.Username = username
		}
		if serverHost != "" {
			config.ServerHost = serverHost
		}
		
		if port > 0 && serverPort > 0 {
			log.Fatal("Conflicting port specifications: both -p flag and host:port format are used. Please use only one method.")
		}
		if serverPort > 0 {
			config.ServerPort = serverPort
		}

		validateConfig(config)

		if base_core.IsDaemonRunning() {
			fmt.Printf("Daemon already running, PID: %d\n", getDaemonPID())
			sendStartCommand(config)
			return
		}

		startDaemonBackground()
		time.Sleep(2 * time.Second)
		sendStartCommand(config)
		return
	}

	if *daemonMode {
		config := base_core.LoadConfig(reverseTunnel, port, password, sshKey, reconnectDelay)

		username, serverHost, serverPort := parsePositionalArgs()
		if username != "" {
			config.Username = username
		}
		if serverHost != "" {
			config.ServerHost = serverHost
		}

		if port > 0 && serverPort > 0 {
			log.Fatal("Conflicting port specifications: both -p flag and host:port format are used. Please use only one method.")
		}
		if serverPort > 0 {
			config.ServerPort = serverPort
		}

		validateConfig(config)

		if reverseTunnel == "" {
			if base_core.IsDaemonRunning() {
				fmt.Printf("Daemon already running, PID: %d\n", getDaemonPID())
				return
			}
			runDaemon()
		} else {
			sendStartCommand(config)
		}
		return
	}

	if *versionCmd {
		fmt.Printf("nssh version: %s\n", Version)
		fmt.Printf("commit: %s\n", CommitID)
		os.Exit(0)
	}

	if *listCmd {
		args := pflag.Args()
		if len(args) > 0 {
			sendGetCommand(args[0])
		} else {
			sendManagementCommand(daemon.CMD_LIST, "")
		}
		return
	}

	if *restartCmd != "" {
		sendManagementCommand(daemon.CMD_RESTART, *restartCmd)
		return
	}

	if *stopCmd != "" {
		sendManagementCommand(daemon.CMD_STOP, *stopCmd)
		return
	}

	if *stopAllCmd {
		sendManagementCommand(daemon.CMD_STOP_ALL, "")
		return
	}

	if *restartAllCmd {
		restartAllProcesses()
		return
	}

	if *logCmd != "" {
		sendManagementCommand(daemon.CMD_LOG, *logCmd)
		return
	}

	if *daemonMode {
		config := base_core.LoadConfig(reverseTunnel, port, password, sshKey, reconnectDelay)

		username, serverHost, serverPort := parsePositionalArgs()
		if username != "" {
			config.Username = username
		}
		if serverHost != "" {
			config.ServerHost = serverHost
		}
		
		if port > 0 && serverPort > 0 {
			log.Fatal("Conflicting port specifications: both -p flag and host:port format are used. Please use only one method.")
		}
		if serverPort > 0 {
			config.ServerPort = serverPort
		}

		validateConfig(config)

		if reverseTunnel == "" {
			if base_core.IsDaemonRunning() {
				fmt.Printf("Daemon already running, PID: %d\n", getDaemonPID())
				return
			}
			runDaemon()
		} else {
			sendStartCommand(config)
		}
		return
	}

	if reverseTunnel == "" {
		fmt.Println("Error: Missing required parameter -R (reverse tunnel)")
		// fmt.Println("Usage: nssh -R remote_port:local_host:local_port [options] user@host")
		// fmt.Println("Example: nssh -R 8000:localhost:80 user@ssh.example.com -p 22 --passwd password")
		os.Exit(1)
	}

	runForeground(reverseTunnel, port, password, sshKey, reconnectDelay)
}

func parsePositionalArgs() (username, serverHost string, serverPort int) {
	args := pflag.Args()
	if len(args) > 0 {
		userHost := args[0]
		if strings.Contains(userHost, "@") {
			parts := strings.SplitN(userHost, "@", 2)
			username = parts[0]
			if strings.Contains(parts[1], ":") {
				hostPort := strings.SplitN(parts[1], ":", 2)
				serverHost = hostPort[0]
				if port, err := strconv.Atoi(hostPort[1]); err == nil {
					serverPort = port
				}
			} else {
				serverHost = parts[1]
			}
		} else {
			if strings.Contains(userHost, ":") {
				hostPort := strings.SplitN(userHost, ":", 2)
				serverHost = hostPort[0]
				if port, err := strconv.Atoi(hostPort[1]); err == nil {
					serverPort = port
				}
			} else {
				serverHost = userHost
			}
		}
	}
	return
}

func validateConfig(config *base_core.Config) error {
	if config.ServerHost == "" {
		log.Fatal("ServerHost cannot be empty")
	}
	if ip := net.ParseIP(config.ServerHost); ip != nil {
		if ip.To4() == nil {
			log.Fatal("ServerHost must be IPv4 address, not IPv6")
		}
	} else {
		if !base_core.IsValidDomain(config.ServerHost) {
			log.Fatal("Invalid ServerHost: must be IPv4 or domain name")
		}
	}

	if config.ServerPort <= 0 || config.ServerPort > 65535 {
		log.Fatal("ServerPort must be between 1 and 65535")
	}
	if config.LocalPort <= 0 || config.LocalPort > 65535 {
		log.Fatal("LocalPort must be between 1 and 65535")
	}
	if config.RemotePort <= 0 || config.RemotePort > 65535 {
		log.Fatal("RemotePort must be between 1 and 65535")
	}

	if config.LocalHost == "" {
		log.Fatal("LocalHost cannot be empty")
	}
	if config.LocalHost != "localhost" {
		if ip := net.ParseIP(config.LocalHost); ip == nil || ip.To4() == nil {
			log.Fatal("LocalHost must be 'localhost' or IPv4 address")
		}
	}
	return nil
}

func runDaemon() {
	if base_core.IsDaemonRunning() {
		transport := daemon.NewTransport()
		respStr, err := transport.SendCommand(daemon.CMD_GET_DAEMON_PID, nil, time.Now().Unix())
		if err != nil {
			fmt.Println("Daemon running, but cannot get PID")
			return
		}
		var resp daemon.Response
		if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
			fmt.Println("Daemon running, but cannot parse response")
			return
		}
		dataMap, ok := resp.Data.(map[string]interface{})
		if !ok {
			fmt.Println("Daemon running, but cannot parse PID")
			return
		}
		pid := int(dataMap["daemon_pid"].(float64))
		fmt.Printf("Daemon already running, PID: %d\n", pid)
		return
	}

	version := Version
	if version == "" {
		version = "dev"
	}

	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("Failed to get executable path: %v\n", err)
		return
	}

	proc, err := platform.StartDaemonProcess(execPath)
	if err != nil {
		fmt.Printf("Failed to start daemon: %v\n", err)
		return
	}

	pid := proc.Pid
	fmt.Printf("Daemon started, PID: %d\n", pid)
	os.Exit(0)
}

func startDaemonBackground() {
	if base_core.IsDaemonRunning() {
		transport := daemon.NewTransport()
		respStr, err := transport.SendCommand(daemon.CMD_GET_DAEMON_PID, nil, time.Now().Unix())
		if err != nil {
			fmt.Println("Daemon running, but cannot get PID")
			return
		}
		var resp daemon.Response
		if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
			fmt.Println("Daemon running, but cannot parse response")
			return
		}
		dataMap, ok := resp.Data.(map[string]interface{})
		if !ok {
			fmt.Println("Daemon running, but cannot parse PID")
			return
		}
		pid := int(dataMap["daemon_pid"].(float64))
		fmt.Printf("Daemon already running, PID: %d\n", pid)
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("Failed to get executable path: %v\n", err)
		return
	}

	proc, err := platform.StartDaemonProcess(execPath)
	if err != nil {
		fmt.Printf("Failed to start daemon: %v\n", err)
		return
	}

	pid := proc.Pid
	fmt.Printf("Daemon started, PID: %d\n", pid)
}

func getDaemonPID() int {
	return base_core.GetDaemonPID()
}

func getDaemonVersion() string {
	transport := daemon.NewTransport()
	respStr, err := transport.SendCommand(daemon.CMD_GET_VERSION, nil, time.Now().Unix())
	if err != nil {
		return ""
	}
	var resp daemon.Response
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		return ""
	}
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		return ""
	}
	return dataMap["version"].(string)
}

func runDaemonInner() {
	if base_core.IsDaemonRunning() {
		return
	}

	version := Version
	if version == "" {
		version = "dev"
	}

	logger := base_core.GetLogger()
	logger.SetProcessType("daemon")

	d := daemon.NewDaemon(version)

	d.Run()
}

func runForeground(reverseTunnel string, port int, password string, sshKey string, reconnectDelay int) {
	config := base_core.LoadConfig(reverseTunnel, port, password, sshKey, reconnectDelay)

	username, serverHost, serverPort := parsePositionalArgs()
	if username != "" {
		config.Username = username
	}
	if serverHost != "" {
		config.ServerHost = serverHost
	}
	
	if port > 0 && serverPort > 0 {
		log.Fatal("Conflicting port specifications: both -p flag and host:port format are used. Please use only one method.")
	}
	if serverPort > 0 {
		config.ServerPort = serverPort
	}

	validateConfig(config)

	base_core.SetLogContext(config.Username, config.ServerHost, config.ServerPort)

	logger := base_core.GetLogger()
	logger.Info("SSH Reverse Proxy Client")
	logger.Info("Server: %s:%d", config.ServerHost, config.ServerPort)
	logger.Info("Port Mapping: %s:%d -> %d", config.LocalHost, config.LocalPort, config.RemotePort)
	logger.Info("Reconnect Delay: %ds", config.ReconnectDelay)
	logger.Info("Password: %s", password)

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	logger.Info("Initial memory stats - Alloc: %d KB, Sys: %d KB", memStats.Alloc/1024, memStats.Sys/1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	signal.Ignore(syscall.SIGHUP)

	go func() {
		sig := <-sigChan
		logger.Warn("Received signal: %v, shutting down...", sig)
		cancel()
	}()

	if err := runClient(ctx, config, false); err != nil {
		logger.Fatal("Client error: %v", err)
	}

	logger.Info("Client stopped")
}

func runClient(ctx context.Context, config *base_core.Config, enableDaemon bool) error {
	logger := base_core.GetLogger()
	statsManager := base_core.NewStatsManager(config, enableDaemon)
	defer statsManager.Stop()

	memoryMonitor := base_core.NewMemoryMonitor()
	if memoryMonitor != nil {
		statsManager.SetMemoryHook(memoryMonitor.PrintMemoryStats)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := base_tunnel.ConnectAndTunnel(ctx, config, statsManager); err != nil {
				logger.Error("Connection error: %v | Command: nssh -R %d:%s:%d %s@%s:%d -P %s -r %d",
					err,
					config.RemotePort,
					config.LocalHost,
					config.LocalPort,
					config.Username,
					config.ServerHost,
					config.ServerPort,
					"***",
					config.ReconnectDelay)
				logger.Info("Reconnecting in %ds...", config.ReconnectDelay)

				select {
				case <-time.After(time.Duration(config.ReconnectDelay) * time.Second):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

func runClientWithStatsManager(ctx context.Context, config *base_core.Config, statsManager *base_core.StatsManager) error {
	logger := base_core.GetLogger()

	memoryMonitor := base_core.NewMemoryMonitor()
	if memoryMonitor != nil {
		statsManager.SetMemoryHook(memoryMonitor.PrintMemoryStats)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := base_tunnel.ConnectAndTunnel(ctx, config, statsManager); err != nil {
				logger.Error("Connection error: %v, reconnecting in %ds...", err, config.ReconnectDelay)

				select {
				case <-time.After(time.Duration(config.ReconnectDelay) * time.Second):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

func sendManagementCommand(cmd int, username string) {
	transport := daemon.NewTransport()
	params := map[string]string{"username": username}
	timestamp := time.Now().Unix()

	respStr, err := transport.SendCommand(cmd, params, timestamp)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	var resp daemon.Response
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Error)
		return
	}

	switch cmd {
	case daemon.CMD_LIST:
		printProcessList(resp.Data)
	case daemon.CMD_LOG:
		printLogInfo(resp.Data)
	default:
		fmt.Printf("Success: %v\n", resp.Data)
	}
}

func formatDuration(d time.Duration) string {
	seconds := int(d.Seconds())

	if seconds >= 365*24*3600 {
		years := seconds / (365 * 24 * 3600)
		return fmt.Sprintf("%dy", years)
	}
	if seconds >= 30*24*3600 {
		months := seconds / (30 * 24 * 3600)
		return fmt.Sprintf("%dm", months)
	}
	if seconds >= 7*24*3600 {
		weeks := seconds / (7 * 24 * 3600)
		return fmt.Sprintf("%dw", weeks)
	}
	if seconds >= 24*3600 {
		days := seconds / (24 * 3600)
		return fmt.Sprintf("%dd", days)
	}
	if seconds >= 3600 {
		hours := seconds / 3600
		return fmt.Sprintf("%dh", hours)
	}
	if seconds >= 60 {
		minutes := seconds / 60
		return fmt.Sprintf("%dm", minutes)
	}
	return "1m"
}

func printProcessList(data interface{}) {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		fmt.Println("No processes running")
		return
	}

	daemonPID := int(dataMap["daemon_pid"].(float64))
	daemonVersion, _ := dataMap["daemon_version"].(string)
	if daemonVersion == "" {
		daemonVersion = "unknown"
	}
	processes, ok := dataMap["processes"].([]interface{})
	if !ok || len(processes) == 0 {
		fmt.Printf("Daemon Version: %s (PID: %d)\n", daemonVersion, daemonPID)
		fmt.Println("No processes running")
		return
	}

	sort.Slice(processes, func(i, j int) bool {
		processI, okI := processes[i].(map[string]interface{})
		processJ, okJ := processes[j].(map[string]interface{})
		if !okI || !okJ {
			return false
		}

		timeIVal, okI := processI["start_time"]
		timeJVal, okJ := processJ["start_time"]
		if !okI || !okJ || timeIVal == nil || timeJVal == nil {
			return false
		}

		timeI, errI := time.Parse(time.RFC3339, timeIVal.(string))
		timeJ, errJ := time.Parse(time.RFC3339, timeJVal.(string))
		if errI != nil || errJ != nil {
			return false
		}

		return timeI.Before(timeJ)
	})

	maxLocalServerWidth := 20
	maxUserWidth := 10
	maxRemoteServerWidth := 20
	maxStatusWidth := 8

	for _, p := range processes {
		processMap, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		username, _ := processMap["username"].(string)
		serverHost, _ := processMap["server_host"].(string)
		serverPort := int(processMap["server_port"].(float64))
		localHost, _ := processMap["local_host"].(string)
		localPort := int(processMap["local_port"].(float64))
		status, _ := processMap["status"].(string)

		localServerStr := fmt.Sprintf("%s:%d", localHost, localPort)
		remoteServerStr := fmt.Sprintf("%s:%d", serverHost, serverPort)

		if len(localServerStr) > maxLocalServerWidth {
			maxLocalServerWidth = len(localServerStr)
		}
		if maxLocalServerWidth > 40 {
			maxLocalServerWidth = 40
		}
		if len(username) > maxUserWidth {
			maxUserWidth = len(username)
		}
		if maxUserWidth > 20 {
			maxUserWidth = 20
		}
		if len(remoteServerStr) > maxRemoteServerWidth {
			maxRemoteServerWidth = len(remoteServerStr)
		}
		if maxRemoteServerWidth > 30 {
			maxRemoteServerWidth = 30
		}
		if len(status) > maxStatusWidth {
			maxStatusWidth = len(status)
		}
	}

	fmt.Printf("Daemon Version: %s (PID: %d)\n\n", daemonVersion, daemonPID)
	fmt.Printf("%-4s %-*s %-*s %-*s %-*s\n",
		"#", maxLocalServerWidth, "local-server",
		maxUserWidth, "user",
		maxRemoteServerWidth, "remote-server",
		maxStatusWidth, "status")

	totalWidth := 4 + maxLocalServerWidth + maxUserWidth + maxRemoteServerWidth + maxStatusWidth
	fmt.Println(strings.Repeat("-", totalWidth))

	for i, p := range processes {
		processMap, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		username, _ := processMap["username"].(string)
		serverHost, _ := processMap["server_host"].(string)
		serverPort := int(processMap["server_port"].(float64))
		localHost, _ := processMap["local_host"].(string)
		localPort := int(processMap["local_port"].(float64))
		status, _ := processMap["status"].(string)

		localServerStr := fmt.Sprintf("%s:%d", localHost, localPort)
		remoteServerStr := fmt.Sprintf("%s:%d", serverHost, serverPort)

		fmt.Printf("%-4d %-*s %-*s %-*s %-*s\n",
			i+1,
			maxLocalServerWidth, localServerStr,
			maxUserWidth, username,
			maxRemoteServerWidth, remoteServerStr,
			maxStatusWidth, status)
	}
}

func printLogInfo(data interface{}) {
	logData, ok := data.(map[string]interface{})
	if !ok {
		return
	}

	id, _ := logData["id"].(string)
	status := logData["status"].(string)
	note := logData["note"].(string)

	fmt.Printf("ID: %s\n", id)
	fmt.Printf("Status: %s\n", status)
	fmt.Printf("Note: %s\n", note)
}

func sendStartCommand(config *base_core.Config) {
	transport := daemon.NewTransport()
	params := map[string]string{
		"username":        config.Username,
		"server_host":     config.ServerHost,
		"server_port":     strconv.Itoa(config.ServerPort),
		"local_host":      config.LocalHost,
		"local_port":      strconv.Itoa(config.LocalPort),
		"remote_port":     strconv.Itoa(config.RemotePort),
		"password":        config.Password,
		"ssh_key":         config.SSHKeyPath,
		"reconnect_delay": strconv.Itoa(config.ReconnectDelay),
	}

	respStr, err := transport.SendCommand(daemon.CMD_START, params, time.Now().Unix())
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	var resp daemon.Response
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Error)
		return
	}

	fmt.Printf("Process started successfully: %v\n", resp.Data)
}

func sendGetCommand(username string) {
	transport := daemon.NewTransport()
	params := map[string]string{"username": username}

	respStr, err := transport.SendCommand(daemon.CMD_GET_COMMAND, params, time.Now().Unix())
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	var resp daemon.Response
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Error)
		return
	}

	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		fmt.Println("Invalid response data")
		return
	}

	if commands, ok := dataMap["commands"].([]interface{}); ok {
		printAllCommandsInfo(commands)
	} else {
		printCommandInfo(resp.Data)
	}
}

func printCommandInfo(data interface{}) {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		fmt.Println("Invalid response data")
		return
	}

	cmd, ok := dataMap["command"].(string)
	if !ok {
		fmt.Println("No command found")
		return
	}

	id, _ := dataMap["id"].(string)
	usr, _ := dataMap["username"].(string)

	fmt.Printf("Username: %s\n", usr)
	fmt.Printf("ID: %s\n", id)
	fmt.Printf("Command: %s\n", cmd)
}

func printAllCommandsInfo(commands []interface{}) {
	fmt.Printf("Found %d process(es):\n\n", len(commands))

	for i, cmd := range commands {
		cmdMap, ok := cmd.(map[string]interface{})
		if !ok {
			continue
		}

		username, _ := cmdMap["username"].(string)
		id, _ := cmdMap["id"].(string)
		server, _ := cmdMap["server"].(string)
		command, _ := cmdMap["command"].(string)

		fmt.Printf("%d. Username: %s\n", i+1, username)
		fmt.Printf("   ID: %s\n", id)
		fmt.Printf("   Server: %s\n", server)
		fmt.Printf("   Command: %s\n", command)
		fmt.Println()
	}
}

func restartAllProcesses() {
	transport := daemon.NewTransport()
	timestamp := time.Now().Unix()

	respStr, err := transport.SendCommand(daemon.CMD_LIST, nil, timestamp)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	var resp daemon.Response
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	if !resp.Success {
		fmt.Printf("Error: %v\n", resp.Data)
		return
	}

	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		fmt.Println("No processes running")
		return
	}

	processes, ok := dataMap["processes"].([]interface{})
	if !ok || len(processes) == 0 {
		fmt.Println("No processes to restart")
		return
	}

	fmt.Printf("Found %d process(es), restarting...\n\n", len(processes))

	successCount := 0
	failCount := 0

	for i, p := range processes {
		processMap, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		username, _ := processMap["username"].(string)
		id, _ := processMap["id"].(string)
		serverHost, _ := processMap["server_host"].(string)
		serverPort := int(processMap["server_port"].(float64))

		target := id
		fmt.Printf("[%d/%d] Restarting %s (%s:%d)... ", i+1, len(processes), username, serverHost, serverPort)

		params := map[string]string{"username": target}
		restartRespStr, err := transport.SendCommand(daemon.CMD_RESTART, params, timestamp)
		if err != nil {
			fmt.Printf("Failed: %v\n", err)
			failCount++
			continue
		}

		var restartResp daemon.Response
		if err := json.Unmarshal([]byte(restartRespStr), &restartResp); err != nil {
			fmt.Printf("Failed: %v\n", err)
			failCount++
			continue
		}

		if restartResp.Success {
			fmt.Printf("Success\n")
			successCount++
		} else {
			fmt.Printf("Failed: %s\n", restartResp.Error)
			failCount++
		}
	}

	fmt.Printf("\nRestart complete: %d succeeded, %d failed\n", successCount, failCount)
}

func handleTakeover(force bool) {
	socketPath := base_core.GetDaemonSocketPath()
	if !base_core.IsDaemonRunning() {
		log.Fatalf("No running nssh daemon found (socket: %s)", socketPath)
	}

	transport := daemon.NewTransport()
	timestamp := time.Now().Unix()

	respStr, err := transport.SendCommand(daemon.CMD_TAKEOVER, nil, timestamp)
	if err != nil {
		log.Fatal("Failed to get daemon info: ", err)
	}

	var resp daemon.Response
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		log.Fatal("Failed to parse response: ", err)
	}

	if !resp.Success {
		log.Fatal("Takeover failed: ", resp.Error)
	}

	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		log.Fatal("Invalid response format")
	}

	oldVersion, _ := dataMap["version"].(string)
	oldPID := int(dataMap["pid"].(float64))
	workers, ok := dataMap["workers"].([]interface{})
	if !ok {
		workers = []interface{}{}
	}



	workerList := make([]map[string]interface{}, 0)
	for _, w := range workers {
		wm, ok := w.(map[string]interface{})
		if !ok {
			continue
		}
		workerList = append(workerList, wm)
	}

	sort.Slice(workerList, func(i, j int) bool {
		timeI, _ := workerList[i]["start_time"].(string)
		timeJ, _ := workerList[j]["start_time"].(string)
		return timeI < timeJ
	})

	if !force {
		fmt.Println("========================================")
		fmt.Println("         nssh Takeover Confirmation")
		fmt.Println("========================================")
		fmt.Printf("Target version: nssh v%s (current)\n", Version)
		fmt.Printf("Source version: nssh v%s (PID: %d)\n", oldVersion, oldPID)
		fmt.Printf("SSH tunnels to migrate: %d\n\n", len(workerList))

		fmt.Println("List:")
		for i, w := range workerList {
			username, _ := w["username"].(string)
			serverHost, _ := w["server_host"].(string)
			serverPort, _ := w["server_port"].(float64)
			localHost, _ := w["local_host"].(string)
			localPort, _ := w["local_port"].(float64)
			remotePort, _ := w["remote_port"].(float64)
			fmt.Printf("  %d. -R %d:%s:%d %s@%s -p %d --passwd ****\n", i+1, int(remotePort), localHost, int(localPort), username, serverHost, int(serverPort))
		}
		fmt.Println("")
		fmt.Print("Confirm takeover? [y/N] (default N): ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			fmt.Println("Cancelled")
			return
		}
	}

	fmt.Println("Migrating tunnels...")

	if proc, err := os.FindProcess(oldPID); err == nil {
		proc.Kill()
	}

	time.Sleep(500 * time.Millisecond)

	// 使用动态获取的 socket 路径
	os.Remove(base_core.GetDaemonSocketPath())

	if !base_core.IsDaemonRunning() {
		fmt.Print("Starting daemon...")
		startDaemonBackground()
		time.Sleep(2 * time.Second)
	}

	successCount := 0
	for i, w := range workerList {
		username, _ := w["username"].(string)
		serverHost, _ := w["server_host"].(string)
		serverPort, _ := w["server_port"].(float64)
		localHost, _ := w["local_host"].(string)
		localPort, _ := w["local_port"].(float64)
		remotePort, _ := w["remote_port"].(float64)
		password, _ := w["password"].(string)

		config := &base_core.Config{
			Username:    username,
			ServerHost:  serverHost,
			ServerPort:  int(serverPort),
			LocalHost:   localHost,
			LocalPort:   int(localPort),
			RemotePort:  int(remotePort),
			Password:    password,
		}

		sendStartCommand(config)
		fmt.Printf("  [%d/%d] -R %d:%s:%d %s@%s:%d\n", i+1, len(workerList), int(remotePort), localHost, int(localPort), username, serverHost, int(serverPort))
		successCount++
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Printf("\n[✓] Takeover successful: %d/%d tunnels migrated\n", successCount, len(workerList))
}

