package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Vrolist/nssh/base_core"
	"github.com/Vrolist/nssh/base_tunnel"
)

// setupSignals 设置信号处理，在 platform_unix.go 和 platform_windows.go 中实现
var setupSignals = func() chan os.Signal {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	return sigChan
}

type Daemon struct {
	workers       map[string]*WorkerInfo
	cmdChan       chan func(map[string]*WorkerInfo)
	stopChan      chan struct{}
	logger        *base_core.CustomLogger
	transport     Transport
	idleStartTime time.Time
	systemInfo    SystemInfo
	version       string
	statsManager  *base_core.StatsManager
}

// 【daemon】Daemon 结构体内存占用分析：
// - map[string]*WorkerInfo: 每个worker约200-600B，map overhead约48B/entry
//   假设10个worker: 约3-8KB
// - chan: 缓冲区100，每个函数指针8B，约800B
// - *CustomLogger: 指针8B，logger实例约1-2KB（包含缓冲区）
// - Transport: 接口，实际实例约100-500B
// - time.Time: 24B
// - SystemInfo: 约100-200B
// 总计: 约5-15KB（取决于worker数量）
// 生命周期: daemon进程启动后一直存在

func NewDaemon(version string) *Daemon {
	d := &Daemon{
		workers:  make(map[string]*WorkerInfo),
		cmdChan:  make(chan func(map[string]*WorkerInfo), 20),
		stopChan: make(chan struct{}),
		logger:   base_core.GetLogger(),
		version:  version,
	}
	go d.processLoop()
	return d
}

func validateConfig(config *base_core.Config) error {
	if config.ServerHost == "" {
		return fmt.Errorf("ServerHost cannot be empty")
	}
	if ip := net.ParseIP(config.ServerHost); ip != nil {
		if ip.To4() == nil {
			return fmt.Errorf("ServerHost must be IPv4 address, not IPv6")
		}
	} else {
		if !base_core.IsValidDomain(config.ServerHost) {
			return fmt.Errorf("Invalid ServerHost: must be IPv4 or domain name")
		}
	}

	if config.ServerPort <= 0 || config.ServerPort > 65535 {
		return fmt.Errorf("ServerPort must be between 1 and 65535")
	}
	if config.LocalPort <= 0 || config.LocalPort > 65535 {
		return fmt.Errorf("LocalPort must be between 1 and 65535")
	}
	if config.RemotePort <= 0 || config.RemotePort > 65535 {
		return fmt.Errorf("RemotePort must be between 1 and 65535")
	}

	if config.LocalHost == "" {
		return fmt.Errorf("LocalHost cannot be empty")
	}
	if config.LocalHost != "localhost" {
		if ip := net.ParseIP(config.LocalHost); ip == nil || ip.To4() == nil {
			return fmt.Errorf("LocalHost must be 'localhost' or IPv4 address")
		}
	}

	return nil
}

func generateProcessKey(username, serverHost string, serverPort int) string {
	return fmt.Sprintf("%s@%s:%d", username, serverHost, serverPort)
}

type TargetSpec struct {
	Username  string
	Server    string
	ServerPort int
	Mode      string
}

func parseTargetSpec(target string) TargetSpec {
	spec := TargetSpec{Mode: "single"}
	if strings.Contains(target, "@") {
		parts := strings.SplitN(target, "@", 2)
		spec.Username = parts[0]
		if len(parts) > 1 {
			if parts[1] == "all" {
				spec.Mode = "all"
			} else if strings.Contains(parts[1], ":") {
				hostPort := strings.SplitN(parts[1], ":", 2)
				spec.Server = hostPort[0]
				if port, err := strconv.Atoi(hostPort[1]); err == nil {
					spec.ServerPort = port
				}
			} else {
				spec.Server = parts[1]
			}
		}
	} else {
		spec.Username = target
	}
	return spec
}

func (d *Daemon) handleProcessAction(params map[string]string, action string) string {
	target := params["username"]
	spec := parseTargetSpec(target)

	if spec.Mode == "all" {
		return d.handleAllProcessesAction(action, spec.Username, spec.Server, spec.ServerPort)
	}

	result := make(chan []*WorkerInfo, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		processes := make([]*WorkerInfo, 0)
		for _, p := range m {
			matchUsername := p.Username == spec.Username
			matchServer := spec.Server == "" || (p.ServerHost == spec.Server && (spec.ServerPort == 0 || p.ServerPort == spec.ServerPort))
			if matchUsername && matchServer {
				processes = append(processes, p)
			}
		}
		result <- processes
	}
	processes := <-result

	if len(processes) == 0 {
		if spec.Server != "" {
			resp := Response{Success: false, Error: fmt.Sprintf("worker not found: %s@%s:%d", spec.Username, spec.Server, spec.ServerPort)}
			b, _ := json.Marshal(resp)
			return string(b) + "\n"
		}
		resp := Response{Success: false, Error: fmt.Sprintf("worker not found: %s", spec.Username)}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	if len(processes) > 1 {
		if spec.Server != "" {
			process := processes[0]
			key := generateProcessKey(process.Username, process.ServerHost, process.ServerPort)
			return d.performAction(action, process, key)
		}
		errorMsg := fmt.Sprintf("Multiple workers found for username '%s':\n", spec.Username)
		for i, p := range processes {
			errorMsg += fmt.Sprintf("  %d. %s - %s:%d\n", i+1, p.Username, p.ServerHost, p.ServerPort)
		}
		errorMsg += "Please specify using:\n"
		errorMsg += "  - username@all  (all workers)\n"
		errorMsg += "  - username@server:port  (specific worker)"
		resp := Response{Success: false, Error: errorMsg}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	process := processes[0]
	key := generateProcessKey(process.Username, process.ServerHost, process.ServerPort)
	return d.performAction(action, process, key)
}

func (d *Daemon) handleAllProcessesAction(action, username string, server string, serverPort int) string {
	type ProcessWithKey struct {
		info *WorkerInfo
		key  string
	}
	result := make(chan []ProcessWithKey, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		processes := make([]ProcessWithKey, 0)
		for key, p := range m {
			matchUsername := p.Username == username
			matchServer := server == "" || (p.ServerHost == server && (serverPort == 0 || p.ServerPort == serverPort))
			if matchUsername && matchServer {
				processes = append(processes, ProcessWithKey{info: p, key: key})
			}
		}
		result <- processes
	}
	processes := <-result

	if len(processes) == 0 {
		if server != "" {
			resp := Response{Success: false, Error: fmt.Sprintf("no processes found for username: %s@%s:%d", username, server, serverPort)}
			b, _ := json.Marshal(resp)
			return string(b) + "\n"
		}
		resp := Response{Success: false, Error: fmt.Sprintf("no processes found for username: %s", username)}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	var results []map[string]interface{}
	for _, item := range processes {
		resp := d.performAction(action, item.info, item.key)
		var r Response
		json.Unmarshal([]byte(resp), &r)
		if r.Success {
			results = append(results, map[string]interface{}{
				"username": item.info.Username,
				"id":       item.info.ID,
				"server":   fmt.Sprintf("%s:%d", item.info.ServerHost, item.info.ServerPort),
			})
		}
	}

	actionName := action
	if action == "stop" {
		actionName = "stopped"
	} else if action == "kill" {
		actionName = "killed"
	} else if action == "restart" {
		actionName = "restarted"
	}

	resp := Response{Success: true, Data: map[string]interface{}{
		"message": fmt.Sprintf("%s %d workers", actionName, len(results)),
		"count":   len(results),
		"results": results,
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) stopWorker(worker *WorkerInfo, key string) {
	if worker.CancelFunc != nil {
		worker.CancelFunc()
	}
	if worker.DoneChan != nil {
		<-worker.DoneChan
	}
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		delete(m, key)
		if len(m) == 0 {
			d.idleStartTime = time.Now()
		}
	}
}

func (d *Daemon) performAction(action string, worker *WorkerInfo, key string) string {
	switch action {
	case "stop":
		d.stopWorker(worker, key)
		resp := Response{Success: true, Data: map[string]interface{}{"username": worker.Username, "id": worker.ID, "status": "stopped"}}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"

	case "kill":
		d.stopWorker(worker, key)
		resp := Response{Success: true, Data: map[string]interface{}{"username": worker.Username, "id": worker.ID, "status": "killed"}}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"

	case "restart":
		d.stopWorker(worker, key)
		time.Sleep(1 * time.Second)

		config := worker.Config
		if config == nil {
			config = &base_core.Config{
				Username:       worker.Username,
				ServerHost:     worker.ServerHost,
				ServerPort:     worker.ServerPort,
				LocalHost:      worker.LocalHost,
				LocalPort:      worker.LocalPort,
				RemotePort:     worker.RemotePort,
				Password:       worker.Password,
				SSHKeyPath:     worker.SSHKeyPath,
				ReconnectDelay: 30,
			}
		}

		err := d.startWorker(config)
		if err != nil {
			resp := Response{Success: false, Error: err.Error()}
			b, _ := json.Marshal(resp)
			return string(b) + "\n"
		}

		resp := Response{Success: true, Data: map[string]interface{}{"username": worker.Username, "id": key, "status": "restarted"}}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"

	default:
		resp := Response{Success: false, Error: "unknown action"}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}
}

func (d *Daemon) processLoop() {
	for {
		select {
		case cmd, ok := <-d.cmdChan:
			if !ok {
				return
			}
			cmd(d.workers)
		case <-d.stopChan:
			return
		}
	}
}

func (d *Daemon) Run() {
	sigChan := setupSignals()

	d.logger.Info("Daemon started, PID: %d", os.Getpid())

	systemInfo, err := GetSystemInfo()
	if err != nil {
		d.logger.Error("Failed to get system info: %v", err)
	} else {
		d.systemInfo = systemInfo
		d.logger.Info("System Info: kernel=%s, distro=%s, arch=%s, os=%s, hostname=%s",
			systemInfo.KernelVersion, systemInfo.Distro, systemInfo.Arch, systemInfo.OS, systemInfo.Hostname)
	}

	d.transport = NewTransport()
	if err := d.transport.StartServer(d.handleTransportCommand); err != nil {
		d.logger.Fatal("Failed to start transport server: %v", err)
	}

	writePIDFile()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		reportCount := 0
		d.reportMonitorData()
		for {
			select {
			case <-ticker.C:
				d.cmdChan <- func(m map[string]*WorkerInfo) {
						for key, p := range m {
							if p.Status == "offline" && p.ReconnectNeeded && p.Config != nil {
								d.logger.Info("Worker %s offline, triggering reconnect...", p.Username)
								p.ReconnectNeeded = false
								go d.startWorker(p.Config)
								continue
							}

							if p.LastRegisterTime.IsZero() {
								continue
							}
							elapsed := time.Since(p.LastRegisterTime)
							if elapsed > 1*time.Minute {
								d.logger.Warn("Worker %s no heartbeat for %v, marking as offline", key, elapsed)
								p.Status = "offline"
								p.StatusChangeTime = time.Now()
								p.ReconnectNeeded = true

								p.OfflineCount++
								d.logger.Info("Worker %s offline count: %d", p.Username, p.OfflineCount)

								maxOfflineCount := 14400
								if p.Config != nil && p.Config.MaxOfflineCount > 0 {
									maxOfflineCount = p.Config.MaxOfflineCount
								}
								if p.OfflineCount >= maxOfflineCount {
									d.logger.Warn("Worker %s offline count reached %d, deleting...", p.Username, p.OfflineCount)
									d.stopWorker(p, key)
								}
								continue
							}

							if p.Status == "online" && p.OfflineCount > 0 {
								d.logger.Info("Worker %s back online, reset offline count from %d", p.Username, p.OfflineCount)
								p.OfflineCount = 0
							}
						}

					if len(m) == 0 {
						if d.idleStartTime.IsZero() {
							d.idleStartTime = time.Now()
						} else if time.Since(d.idleStartTime) > 10*time.Minute {
							d.logger.Info("No clients for 10 minutes, shutting down daemon...")
							d.shutdown()
							removePIDFile()
							os.Exit(0)
						}
					} else {
						d.idleStartTime = time.Time{}
					}
				}

				reportCount++
				if reportCount >= 10 {
					d.reportMonitorData()
					reportCount = 0
				}
			case <-d.stopChan:
				removePIDFile()
				return
			}
		}
	}()

	for sig := range sigChan {
		switch sig {
		case syscall.SIGTERM, syscall.SIGINT:
			d.shutdown()
			d.transport.Stop()
			removePIDFile()
			os.Exit(0)
		}
	}
}

func (d *Daemon) handleTransportCommand(cmd int, params map[string]string, key string, timestamp int64) string {
	switch cmd {
	case CMD_START:
		return d.handleStartTransport(params)
	case CMD_STOP:
		return d.handleStopTransport(params)
	case CMD_STOP_ALL:
		return d.handleStopAllTransport()
	case CMD_RESTART:
		return d.handleRestartTransport(params)
	case CMD_LIST:
		return d.handleListTransport()
	case CMD_LOG:
		return d.handleLogTransport(params)
	case CMD_REGISTER:
		return d.handleRegisterTransport(params)
	case CMD_GET_COMMAND:
		return d.handleGetCommandTransport(params)
	case CMD_GET_DAEMON_PID:
		return d.handleGetDaemonPIDTransport()
	case CMD_GET_VERSION:
		return d.handleGetVersionTransport()
	case CMD_TAKEOVER:
		return d.handleTakeover(params, key, timestamp)
	default:
		resp := Response{Success: false, Error: "unknown command"}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}
}

func (d *Daemon) handleStartTransport(params map[string]string) string {
	reconnectDelay := parseInt(params["reconnect_delay"])
	if reconnectDelay <= 0 {
		reconnectDelay = 30
	}

	config := &base_core.Config{
		Username:       params["username"],
		ServerHost:     params["server_host"],
		ServerPort:     parseInt(params["server_port"]),
		LocalHost:      params["local_host"],
		LocalPort:      parseInt(params["local_port"]),
		RemotePort:     parseInt(params["remote_port"]),
		Password:       params["password"],
		SSHKeyPath:     params["ssh_key"],
		ReconnectDelay: reconnectDelay,
	}

	if err := validateConfig(config); err != nil {
		resp := Response{Success: false, Error: err.Error()}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	d.logger.Info("Starting client with parsed parameters: username=%s, server_host=%s, server_port=%d, local_host=%s, local_port=%d, remote_port=%d, password=%s, ssh_key=%s",
		config.Username, config.ServerHost, config.ServerPort, config.LocalHost, config.LocalPort, config.RemotePort, config.Password, config.SSHKeyPath)

	processKey := generateProcessKey(config.Username, config.ServerHost, config.ServerPort)

	result := make(chan *WorkerInfo, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		oldProcess := m[processKey]
		if oldProcess != nil && oldProcess.CancelFunc != nil {
			oldProcess.CancelFunc()
		}
		result <- m[processKey]
	}
	oldProcess := <-result

	err := d.startWorker(config)
	if err != nil {
		resp := Response{Success: false, Error: err.Error()}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	restarted := oldProcess != nil
	resp := Response{Success: true, Data: map[string]interface{}{"id": processKey, "username": config.Username, "restarted": restarted}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleStopTransport(params map[string]string) string {
	return d.handleProcessAction(params, "stop")
}

func (d *Daemon) handleStopAllTransport() string {
	type ProcessWithKey struct {
		info *WorkerInfo
		key  string
	}
	result := make(chan []ProcessWithKey, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		processes := make([]ProcessWithKey, 0, len(m))
		for key, p := range m {
			processes = append(processes, ProcessWithKey{info: p, key: key})
		}
		result <- processes
	}
	processes := <-result

	if len(processes) == 0 {
		resp := Response{Success: false, Error: "no processes to stop"}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	for _, item := range processes {
		if item.info.CancelFunc != nil {
			item.info.CancelFunc()
		}
	}

	d.cmdChan <- func(m map[string]*WorkerInfo) {
		for _, item := range processes {
			delete(m, item.key)
		}
		if len(m) == 0 {
			d.idleStartTime = time.Now()
		}
	}

	resp := Response{Success: true, Data: map[string]interface{}{
		"message":       fmt.Sprintf("stopped %d workers", len(processes)),
		"stopped_count": len(processes),
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleRegisterTransport(params map[string]string) string {
	username := params["username"]
	if username == "" {
		resp := Response{Success: false, Error: "missing required parameter: username"}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	password := params["password"]
	sshKey := params["ssh_key"]
	serverHost := params["server_host"]
	serverPort, _ := strconv.Atoi(params["server_port"])
	localHost := params["local_host"]
	localPort, _ := strconv.Atoi(params["local_port"])
	remotePort, _ := strconv.Atoi(params["remote_port"])
	status := params["status"]
	if status == "" {
		status = "offline"
	}
	startTimeStr := params["start_time"]
	statusChangeTimeStr := params["last_status_change_time"]

	processKey := generateProcessKey(username, serverHost, serverPort)

	isNewProcess := false
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		if process, exists := m[processKey]; exists {
			process.LastRegisterTime = time.Now()
			if startTimeStr != "" {
				if parsedTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
					process.StartTime = parsedTime
				}
			}
			if statusChangeTimeStr != "" {
				if parsedTime, err := time.Parse(time.RFC3339, statusChangeTimeStr); err == nil {
					if process.Status != status {
						process.Status = status
						process.StatusChangeTime = parsedTime
					}
				}
			} else {
				if process.Status != status {
					process.Status = status
					process.StatusChangeTime = time.Now()
				}
			}
			if password != "" {
				process.Password = password
			}
			if sshKey != "" {
				process.SSHKeyPath = sshKey
			}
		} else {
			isNewProcess = true
			m[processKey] = &WorkerInfo{
				ID:                 processKey,
				Username:            username,
				ServerHost:         serverHost,
				ServerPort:         serverPort,
				LocalHost:          localHost,
				LocalPort:          localPort,
				RemotePort:         remotePort,
				Password:           password,
				SSHKeyPath:         sshKey,
				StartTime:          parseTime(startTimeStr, time.Now()),
				Status:             status,
				LastRegisterTime:   time.Now(),
				StatusChangeTime:   parseTime(statusChangeTimeStr, time.Now()),
			}
		}
	}

	if isNewProcess {
		d.logger.Info("[CLIENT_REGISTER] New client registered - Username: %s, ID: %s, Server: %s:%d, Tunnel: %d:%s:%d, Status: %s, Password: %s, SSHKey: %s, StartTime: %s, StatusChangeTime: %s",
			username, processKey, serverHost, serverPort, remotePort, localHost, localPort, status, password, sshKey, startTimeStr, statusChangeTimeStr)
	} else {
		d.logger.Info("[CLIENT_REGISTER] Client heartbeat received - Username: %s, ID: %s, Server: %s:%d, Tunnel: %d:%s:%d, Status: %s, Password: %s, SSHKey: %s, StartTime: %s, StatusChangeTime: %s",
			username, processKey, serverHost, serverPort, remotePort, localHost, localPort, status, password, sshKey, startTimeStr, statusChangeTimeStr)
	}

	resp := Response{Success: true}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleRestartTransport(params map[string]string) string {
	return d.handleProcessAction(params, "restart")
}

func (d *Daemon) handleListTransport() string {
	result := make(chan []*WorkerInfo, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		processes := make([]*WorkerInfo, 0, len(m))
		for _, p := range m {
			processes = append(processes, p)
		}
		result <- processes
	}
	processes := <-result

	resp := Response{Success: true, Data: map[string]interface{}{
		"daemon_pid":    os.Getpid(),
		"daemon_version": d.version,
		"processes":     processes,
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleGetDaemonPIDTransport() string {
	resp := Response{Success: true, Data: map[string]interface{}{
		"daemon_pid": os.Getpid(),
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleGetVersionTransport() string {
	resp := Response{Success: true, Data: map[string]interface{}{
		"version": d.version,
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleLogTransport(params map[string]string) string {
	username := params["username"]

	result := make(chan *WorkerInfo, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		for _, process := range m {
			if process.Username == username {
				result <- process
				return
			}
		}
		result <- nil
	}
	process := <-result

	if process == nil {
		resp := Response{Success: false, Error: "worker not found"}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	resp := Response{Success: true, Data: map[string]interface{}{
		"id":      process.ID,
		"status":  process.Status,
		"note":    "Log viewing not available for goroutine workers",
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleGetCommandTransport(params map[string]string) string {
	target := params["username"]
	spec := parseTargetSpec(target)

	if spec.Mode == "all" {
		return d.formatAllCommandsResponse(spec.Username, spec.Server, spec.ServerPort)
	}

	result := make(chan []*WorkerInfo, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		processes := make([]*WorkerInfo, 0)
		for _, p := range m {
			matchUsername := p.Username == spec.Username
			matchServer := spec.Server == "" || (p.ServerHost == spec.Server && (spec.ServerPort == 0 || p.ServerPort == spec.ServerPort))
			if matchUsername && matchServer {
				processes = append(processes, p)
			}
		}
		result <- processes
	}
	processes := <-result

	if len(processes) == 0 {
		if spec.Server != "" {
			resp := Response{Success: false, Error: fmt.Sprintf("worker not found: %s@%s:%d", spec.Username, spec.Server, spec.ServerPort)}
			b, _ := json.Marshal(resp)
			return string(b) + "\n"
		}
		resp := Response{Success: false, Error: fmt.Sprintf("worker not found: %s", spec.Username)}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	if len(processes) > 1 {
		if spec.Server != "" {
			process := processes[0]
			return d.formatCommandResponse(process)
		}
		errorMsg := fmt.Sprintf("Multiple workers found for username '%s':\n", spec.Username)
		for i, p := range processes {
			errorMsg += fmt.Sprintf("  %d. %s - %s:%d\n", i+1, p.Username, p.ServerHost, p.ServerPort)
		}
		errorMsg += "Please specify using:\n"
		errorMsg += "  - username@all  (all workers)\n"
		errorMsg += "  - username@server:port  (specific worker)"
		resp := Response{Success: false, Error: errorMsg}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	process := processes[0]
	return d.formatCommandResponse(process)
}

func (d *Daemon) formatCommandResponse(process *WorkerInfo) string {
	cmd := fmt.Sprintf("nssh -R %d:%s:%d %s@%s -p %d --passwd %s --daemon",
		process.RemotePort, process.LocalHost, process.LocalPort,
		process.Username, process.ServerHost,
		process.ServerPort, process.Password)

	if process.SSHKeyPath != "" {
		cmd = fmt.Sprintf("nssh -R %d:%s:%d %s@%s -p %d --passwd %s -i %s --daemon",
			process.RemotePort, process.LocalHost, process.LocalPort,
			process.Username, process.ServerHost,
			process.ServerPort, process.Password, process.SSHKeyPath)
	}

	resp := Response{Success: true, Data: map[string]interface{}{
		"username": process.Username,
		"command":  cmd,
		"id":       process.ID,
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) formatAllCommandsResponse(username string, server string, serverPort int) string {
	type ProcessWithKey struct {
		info *WorkerInfo
		key  string
	}
	result := make(chan []ProcessWithKey, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		processes := make([]ProcessWithKey, 0)
		for key, p := range m {
			matchUsername := p.Username == username
			matchServer := server == "" || (p.ServerHost == server && (serverPort == 0 || p.ServerPort == serverPort))
			if matchUsername && matchServer {
				processes = append(processes, ProcessWithKey{info: p, key: key})
			}
		}
		result <- processes
	}
	processes := <-result

	if len(processes) == 0 {
		if server != "" {
			resp := Response{Success: false, Error: fmt.Sprintf("no processes found for username: %s@%s:%d", username, server, serverPort)}
			b, _ := json.Marshal(resp)
			return string(b) + "\n"
		}
		resp := Response{Success: false, Error: fmt.Sprintf("no processes found for username: %s", username)}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	var commands []map[string]interface{}
	for _, item := range processes {
		cmd := fmt.Sprintf("nssh -R %d:%s:%d %s@%s -p %d --passwd %s --daemon",
			item.info.RemotePort, item.info.LocalHost, item.info.LocalPort,
			item.info.Username, item.info.ServerHost,
			item.info.ServerPort, item.info.Password)

		if item.info.SSHKeyPath != "" {
			cmd = fmt.Sprintf("nssh -R %d:%s:%d %s@%s -p %d --passwd %s -i %s --daemon",
				item.info.RemotePort, item.info.LocalHost, item.info.LocalPort,
				item.info.Username, item.info.ServerHost,
				item.info.ServerPort, item.info.Password, item.info.SSHKeyPath)
		}

		commands = append(commands, map[string]interface{}{
			"username": item.info.Username,
			"id":       item.info.ID,
			"server":   fmt.Sprintf("%s:%d", item.info.ServerHost, item.info.ServerPort),
			"command":  cmd,
		})
	}

	resp := Response{Success: true, Data: map[string]interface{}{
		"message":  fmt.Sprintf("found %d workers", len(commands)),
		"count":    len(commands),
		"commands": commands,
	}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func parseInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func parseTime(timeStr string, defaultTime time.Time) time.Time {
	if timeStr == "" {
		return defaultTime
	}
	if parsedTime, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return parsedTime
	}
	return defaultTime
}

func sanitizeForFilename(s string) string {
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

func (d *Daemon) startWorker(config *base_core.Config) error {
	ctx, cancel := context.WithCancel(context.Background())

	workerKey := generateProcessKey(config.Username, config.ServerHost, config.ServerPort)

	d.cmdChan <- func(m map[string]*WorkerInfo) {
		if existing, exists := m[workerKey]; exists && existing.CancelFunc != nil {
			existing.CancelFunc()
		}

		worker := &WorkerInfo{
			ID:        workerKey,
			CancelFunc: cancel,
			ErrorChan:  make(chan error, 1),
			DoneChan:   make(chan struct{}),
			Username:   config.Username,
			ServerHost: config.ServerHost,
			ServerPort: config.ServerPort,
			LocalHost:  config.LocalHost,
			LocalPort:  config.LocalPort,
			RemotePort: config.RemotePort,
			Password:   config.Password,
			SSHKeyPath: config.SSHKeyPath,
			StartTime:   time.Now(),
			Status:     "offline",
			Config:     config,
		}
		m[workerKey] = worker

		d.idleStartTime = time.Time{}

		go d.runWorker(ctx, worker, config)
	}

	return nil
}

func (d *Daemon) runWorker(ctx context.Context, worker *WorkerInfo, config *base_core.Config) {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Error("Worker goroutine panicked: %v", r)
			worker.Status = "crashed"
			worker.StatusChangeTime = time.Now()
		}
		close(worker.DoneChan)
	}()

	worker.Status = "offline"
	worker.StatusChangeTime = time.Now()
	worker.LastRegisterTime = time.Now()

	statsManager := base_core.NewStatsManager(config, true)
	defer statsManager.Stop()

	for {
		heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)

		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatCtx.Done():
					return
				case <-ticker.C:
					worker.LastRegisterTime = time.Now()
					worker.Status = "online"
				}
			}
		}()

		err := base_tunnel.ConnectAndTunnel(ctx, config, statsManager)
		heartbeatCancel()

		select {
		case <-ctx.Done():
			worker.Status = "offline"
			worker.StatusChangeTime = time.Now()
			return
		default:
		}

		if err != nil {
			d.logger.Warn("Worker %s tunnel error: %v", worker.Username, err)
		} else {
			d.logger.Info("Worker %s connection closed normally", worker.Username)
		}

		worker.Status = "offline"
		worker.StatusChangeTime = time.Now()

		reconnectDelay := time.Duration(config.ReconnectDelay) * time.Second
		d.logger.Info("Worker %s waiting %v before reconnect...", worker.Username, reconnectDelay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
			d.logger.Info("Worker %s reconnecting...", worker.Username)
		}
	}
}

func (d *Daemon) reportMonitorData() {
	daemonPID := os.Getpid()
	daemonMem, err := GetProcessMemory(daemonPID)
	if err != nil {
		d.logger.Error("Failed to get daemon memory: %v, using default values", err)
		daemonMem = ProcessMemory{PID: daemonPID}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	result := make(chan []*WorkerInfo, 1)
	d.cmdChan <- func(workers map[string]*WorkerInfo) {
		processes := make([]*WorkerInfo, 0, len(workers))
		for _, p := range workers {
			processes = append(processes, p)
		}
		result <- processes
	}
	processes := <-result

	workersMemory := make([]WorkerMemory, 0, len(processes))
	onlineCount := 0
	offlineCount := 0
	now := time.Now()
	for _, p := range processes {
		status := p.Status
		if status == "online" {
			onlineCount++
		} else {
			offlineCount++
		}

		var statusStableDuration string
		if !p.StatusChangeTime.IsZero() {
			statusStableDuration = formatDuration(now.Sub(p.StatusChangeTime))
		}

		workersMemory = append(workersMemory, WorkerMemory{
			ID:                   p.ID,
			Status:               status,
			StatusStableDuration: statusStableDuration,
		})
	}

	report := &MonitorReport{
		AllocMemory:    int64(m.Alloc),
		DaemonMemory:   daemonMem,
		DaemonPID:      daemonPID,
		GoroutineCount: runtime.NumGoroutine(),
		OfflineWorkers: offlineCount,
		OnlineWorkers:  onlineCount,
		ReportType:     "daemon_monitor",
		SystemInfo:     d.systemInfo,
		Timestamp:      time.Now().Format(time.RFC3339),
		TotalWorkers:   len(processes),
		Version:        d.version,
		VmRSSMemory:    daemonMem.VmRSS,
		WorkersMemory:  workersMemory,
	}

	if err := PushMonitorReport(report); err != nil {
		d.logger.Error("Failed to push monitor report: %v", err)
	} else {
		d.logger.Info("Monitor report pushed: goroutines=%d, alloc=%dKB, vmrss=%dKB, total=%d, online=%d, offline=%d",
			runtime.NumGoroutine(), m.Alloc/1024, daemonMem.VmRSS, len(processes), onlineCount, offlineCount)
	}
}

func (d *Daemon) shutdown() {
	close(d.stopChan)
	close(d.cmdChan)
	d.logger.Info("Daemon shutdown complete, workers remain running independently")
}

func (d *Daemon) handleTakeover(params map[string]string, key string, timestamp int64) string {
	if !VerifyKeyWithTime(timestamp, key) {
		resp := Response{Success: false, Error: "authentication failed"}
		b, _ := json.Marshal(resp)
		return string(b) + "\n"
	}

	action := params["action"]
	if action == "stop" {
		return d.handleTakeoverStop()
	}
	return d.handleTakeoverExport()
}

func (d *Daemon) handleTakeoverExport() string {
	result := make(chan []map[string]interface{}, 1)
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		workers := make([]map[string]interface{}, 0, len(m))
		for _, w := range m {
			workers = append(workers, map[string]interface{}{
				"username":     w.Username,
				"server_host": w.ServerHost,
				"server_port": w.ServerPort,
				"local_host":  w.LocalHost,
				"local_port":  w.LocalPort,
				"remote_port": w.RemotePort,
				"password":    w.Password,
				"ssh_key":     w.SSHKeyPath,
				"start_time":  w.StartTime,
			})
		}
		result <- workers
	}
	workers := <-result

	resp := Response{
		Success: true,
		Data: map[string]interface{}{
			"version": d.version,
			"pid":     os.Getpid(),
			"workers": workers,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}

func (d *Daemon) handleTakeoverStop() string {
	d.cmdChan <- func(m map[string]*WorkerInfo) {
		for _, w := range m {
			w.ReconnectNeeded = false
			w.Status = "migrating"
		}
		time.Sleep(5 * time.Second)
		for _, w := range m {
			if w.CancelFunc != nil {
				w.CancelFunc()
			}
		}
		for k := range m {
			delete(m, k)
		}
	}

	resp := Response{Success: true, Data: map[string]string{"message": "stopped"}}
	b, _ := json.Marshal(resp)
	return string(b) + "\n"
}
