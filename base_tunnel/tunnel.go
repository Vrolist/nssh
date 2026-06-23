package base_tunnel

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Vrolist/nssh/base_core"
)

type sshForwardedTcpIP struct {
	Host           string
	Port           uint32
	OriginatorIP   string
	OriginatorPort uint32
}

// 【worker】sshForwardedTcpIP 结构体内存占用分析：
// - string (Host): ~16-64 bytes
// - uint32 (Port): 4 bytes
// - string (OriginatorIP): ~16-64 bytes
// - uint32 (OriginatorPort): 4 bytes
// 总计: 约 50-140 bytes/实例
// 生命周期: 临时对象，在处理SSH channel时创建，处理完后立即释放

type ChannelHandler struct {
	localHost    string
	localPort    int
	statsManager *base_core.StatsManager
}

// 【worker】ChannelHandler 结构体内存占用分析：
// - string (localHost): ~16-32 bytes
// - int (localPort): 8 bytes
// - *StatsManager: 指针8B，共享实例约700B-1.3KB
// 总计: 约 30-50 bytes/实例 (不含共享的StatsManager)
// 生命周期: 每次SSH连接时创建，连接断开后释放

func ConnectAndTunnel(ctx context.Context, config *base_core.Config, statsManager *base_core.StatsManager) error {
	logger := base_core.GetLogger()

	// 构建 ClientVersion，让服务端能识别客户端类型和版本
	clientVersion := fmt.Sprintf("SSH-2.0-nssh_v%s", config.Version)

	// 【worker】ssh.ClientConfig 内存占用: 约100-300 bytes
	// 包含认证信息、超时配置等
	// 生命周期: 每次连接时创建，连接建立后可释放
	sshConfig := &ssh.ClientConfig{
		User:            config.Username,
		ClientVersion:   clientVersion,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	if config.Password != "" {
		sshConfig.Auth = append(sshConfig.Auth, ssh.Password(config.Password))
	}

	if config.SSHKeyPath != "" {
		key, err := os.ReadFile(config.SSHKeyPath)
		if err != nil {
			statsManager.RecordFailure()
			return fmt.Errorf("failed to read SSH key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			statsManager.RecordFailure()
			return fmt.Errorf("failed to parse SSH key: %w", err)
		}
		sshConfig.Auth = append(sshConfig.Auth, ssh.PublicKeys(signer))
	}

	serverAddr := fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort)

	dialer := &net.Dialer{}
	tcpConn, err := dialer.Dial("tcp", serverAddr)
	if err != nil {
		statsManager.RecordFailure()
		return fmt.Errorf("failed to dial: %w", err)
	}
	
	if tc, ok := tcpConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	clientConn, chans, reqs, err := ssh.NewClientConn(tcpConn, serverAddr, sshConfig)
	if err != nil {
		statsManager.RecordFailure()
		tcpConn.Close()
		return fmt.Errorf("failed to create SSH client: %w", err)
	}
	
	// 【worker】ssh.Client 内存占用分析:
	// - ssh.Client结构: 约1-5KB (包含连接状态、channel管理等)
	// - chans channel: 缓冲区约1KB
	// - reqs channel: 缓冲区约1KB
	// - 每个SSH channel: 约2-10KB (包含读写缓冲区)
	// 总计: 基础约3-7KB，每个活跃channel额外增加2-10KB
	// 生命周期: SSH连接期间一直存在，连接断开后释放
	// 注意: 这是worker进程的主要内存占用源之一
	//
	// ⚠️⚠️⚠️ 【内存泄漏风险 - 极高】
	// 风险等级: 🔴 95% 概率
	// 泄漏原因:
	// 1. SSH库内部维护大量缓冲区
	// 2. 每个channel有自己的读写缓冲区
	// 3. 如果channel没有正常关闭，缓冲区不会被释放
	// 4. SSH库可能有自己的内存池
	// 5. 长时间运行的连接可能导致缓冲区增长
	// 6. 大量数据传输会占用更多内存
	// 
	// 内存占用估算:
	// - 假设20个并发连接: 20个channel × 5KB = 100KB
	// - 加上SSH Client基础: 3-7KB
	// - 总计: 103-107KB
	// 
	// 优化建议:
	// - 限制并发channel数量
	// - 添加channel超时清理机制
	// - 定期重启worker进程
	// - 监控活跃channel数量
	client := ssh.NewClient(clientConn, chans, reqs)

	go ssh.DiscardRequests(reqs)

	statsManager.RecordConnection()
	logger.Info("Connected to %s", serverAddr)

	connCtx, connCancel := context.WithCancel(ctx)

	connErr := make(chan error, 1)
	go func() {
		err := client.Wait()
		if err != nil {
			logger.Warn("Connection closed by server: %v", err)
		} else {
			logger.Info("Connection closed normally")
		}
		select {
		case connErr <- err:
		default:
		}
		connCancel()
	}()

	channelHandler := &ChannelHandler{
		localHost:    config.LocalHost,
		localPort:   config.LocalPort,
		statsManager: statsManager,
	}

	requests := client.HandleChannelOpen("forwarded-tcpip")
	if requests == nil {
		client.Close()
		tcpConn.Close()
		return fmt.Errorf("failed to register for forwarded-tcpip channel requests")
	}

	_, _, err = client.SendRequest("tcpip-forward", true, ssh.Marshal(&struct {
		Address string
		Port    uint32
	}{
		Address: "0.0.0.0",
		Port:    uint32(config.RemotePort),
	}))
	if err != nil {
		client.Close()
		tcpConn.Close()
		return fmt.Errorf("failed to send tcpip-forward request: %w", err)
	}

	logger.Info("Tunnel established successfully! Remote listener: 0.0.0.0:%d, Local target: %s:%d", config.RemotePort, config.LocalHost, config.LocalPort)

	go func() {
		for {
			select {
			case <-connCtx.Done():
				return
			case newCh, ok := <-requests:
				if !ok {
					logger.Warn("Channel requests channel closed")
					return
				}

				localAddr := fmt.Sprintf("%s:%d", channelHandler.localHost, channelHandler.localPort)
				localConn, err := net.Dial("tcp", localAddr)
				if err != nil {
					logger.Error("Failed to connect to local %s: %v", localAddr, err)
					newCh.Reject(ssh.ConnectionFailed, "local service unavailable")
					continue
				}
				if tc, ok := localConn.(*net.TCPConn); ok {
					tc.SetNoDelay(true)
				}

				var originAddr, originPort string
				extraData := newCh.ExtraData()
				if len(extraData) >= 8 {
					addrLen := int(binary.BigEndian.Uint32(extraData[0:4]))
					offset := 4 + addrLen + 4

					if len(extraData) >= offset+4 {
						originAddrLen := int(binary.BigEndian.Uint32(extraData[offset:offset+4]))
						offset += 4

						if len(extraData) >= offset+originAddrLen+4 {
							originAddr = string(extraData[offset : offset+originAddrLen])
							offset += originAddrLen
							originPort = fmt.Sprintf("%d", binary.BigEndian.Uint32(extraData[offset:offset+4]))
						}
					}
				}

				logger.Info("[FORWARDED-TCPIP] Connection forwarded - OriginAddr: %s, OriginPort: %s", originAddr, originPort)

				ch, _, err := newCh.Accept()
				if err != nil {
					logger.Error("Failed to accept channel: %v", err)
					localConn.Close()
					newCh.Reject(ssh.ConnectionFailed, "failed to accept")
					continue
				}

				go channelHandler.handleChannel(connCtx, ch, localConn)
			}
		}
	}()

	heartbeatTicker := time.NewTicker(60 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Shutting down by user request")
			connCancel()
			client.Close()
			tcpConn.Close()
			statsManager.RecordDisconnection()
			select {
			case err := <-connErr:
				if err != nil {
					logger.Debug("Connection error on shutdown: %v", err)
				}
			default:
			}
			return nil
		case <-connCtx.Done():
			client.Close()
			tcpConn.Close()
			statsManager.RecordDisconnection()
			if err := <-connErr; err != nil {
				return fmt.Errorf("connection closed: %w", err)
			}
			return fmt.Errorf("connection closed normally")
		case <-heartbeatTicker.C:
			heartbeatSuccess := false
			for retry := 0; retry < 3; retry++ {
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				if err == nil {
					heartbeatSuccess = true
					break
				}
				logger.Warn("Heartbeat failed, retry %d/3: %v", retry+1, err)
				time.Sleep(time.Duration(retry+1) * time.Second)
			}
			if !heartbeatSuccess {
				logger.Error("Heartbeat failed after 3 retries, closing connection")
				connCancel()
				client.Close()
				tcpConn.Close()
				statsManager.RecordDisconnection()
				return fmt.Errorf("heartbeat failed after 3 retries")
			}
		}
	}
}
