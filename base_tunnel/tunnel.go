package base_tunnel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Vrolist/nssh/base_core"
)

// ErrForwardRejected 服务端明确拒绝端口声明（端口被其他会话占用 / 策略不允许 / UDP 未开启）。
// 断开重连后立即被拒 = 旧会话还占着端口，通常秒级~90s 内释放；
// daemon 据此用短退避（3s/5s/10s）快速探测端口释放，而不是固定等 30s。
var ErrForwardRejected = errors.New("forward request rejected by server")

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
	compress     bool // 是否已与服务端协商启用应用层压缩（client->server）
}

// 【worker】ChannelHandler 结构体内存占用分析：
// - string (localHost): ~16-32 bytes
// - int (localPort): 8 bytes
// - *StatsManager: 指针8B，共享实例约700B-1.3KB
// 总计: 约 30-50 bytes/实例 (不含共享的StatsManager)
// 生命周期: 每次SSH连接时创建，连接断开后释放

// tcpForwardClient setupTCPForward 所需的最小 SSH 客户端能力集合。
// 抽成接口便于单测：真实实现为 *ssh.Client，测试用假实现验证服务端拒绝
// tcpip-forward（ok=false）时必须断线，而不是把连接当成已建立。
type tcpForwardClient interface {
	HandleChannelOpen(channelType string) <-chan ssh.NewChannel
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
	Close() error
}

const (
	// forwardRequestTimeout tcpip-forward 请求超时：服务端不回复时（半开/卡死）必须
	// 及时失败进入重连，否则重连线程会永久卡在建立阶段。
	forwardRequestTimeout = 15 * time.Second
	// sshHandshakeTimeout SSH 握手超时：ssh.NewClientConn 不读 ClientConfig.Timeout，
	// 只有 ssh.Dial 才用，必须靠 transport deadline 兜底（否则握手可无限挂起）。
	sshHandshakeTimeout = 15 * time.Second
)

// sendForwardRequestWithTimeout 带超时的 wantReply 请求。
// 超时后调用方会 Close 连接，卡住的 SendRequest 随之解除。
func sendForwardRequestWithTimeout(client tcpForwardClient, name string, payload []byte, timeout time.Duration) (bool, error) {
	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		ok, _, err := client.SendRequest(name, true, payload)
		done <- result{ok: ok, err: err}
	}()
	select {
	case r := <-done:
		return r.ok, r.err
	case <-time.After(timeout):
		return false, fmt.Errorf("request %q timeout after %v", name, timeout)
	}
}

// declareTCPForward 注册 forwarded-tcpip channel 接收并发送 tcpip-forward 端口声明。
// 服务端明确拒绝（ok=false，例如关闭了 AllowRemoteForward/端口策略不允许）时，
// 连接虽然存活但没有任何转发能力——必须断线重连，否则客户端会长期"假在线"。
func declareTCPForward(client tcpForwardClient, remotePort int) (<-chan ssh.NewChannel, error) {
	logger := base_core.GetLogger()

	requests := client.HandleChannelOpen("forwarded-tcpip")
	if requests == nil {
		// 注册失败属罕见路径：记录错误并断开连接（等待上层重连）
		logger.Error("Failed to register for forwarded-tcpip channel requests")
		client.Close()
		return nil, fmt.Errorf("failed to register for forwarded-tcpip channel requests")
	}

	ok, err := sendForwardRequestWithTimeout(client, "tcpip-forward", ssh.Marshal(&struct {
		Address string
		Port    uint32
	}{
		Address: "0.0.0.0",
		Port:    uint32(remotePort),
	}), forwardRequestTimeout)
	if err != nil {
		logger.Error("Failed to send tcpip-forward request: %v", err)
		client.Close()
		return nil, fmt.Errorf("failed to send tcpip-forward request: %w", err)
	}
	if !ok {
		logger.Error("Server rejected tcpip-forward request for remote port %d", remotePort)
		client.Close()
		// 用哨兵错误包装：daemon 重连调度据此走 bind 被拒的短退避（P2），而非固定 30s
		return nil, fmt.Errorf("%w: tcpip-forward remote port %d", ErrForwardRejected, remotePort)
	}
	return requests, nil
}

// setupTCPForward TCP 反向转发：注册 channel + 端口声明 + 转发循环
// （每个新 channel 独立 goroutine 处理，消除队头阻塞）。
// 返回错误表示隧道未建立成功，调用方必须关闭连接并进入重连流程。
func setupTCPForward(client tcpForwardClient, channelHandler *ChannelHandler, connCtx context.Context, config *base_core.Config) error {
	requests, err := declareTCPForward(client, config.RemotePort)
	if err != nil {
		return err
	}

	logger := base_core.GetLogger()
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

				// 每个新 channel 用独立 goroutine 处理，消除队头阻塞：
				// 本地服务瞬时卡顿时，一次拨号挂住不应阻塞后续所有转发请求
				go func(newCh ssh.NewChannel) {
					localAddr := fmt.Sprintf("%s:%d", channelHandler.localHost, channelHandler.localPort)
					// 带超时拨号：本地服务无响应时快速 reject，避免无限等待拖垮上游
					localConn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
					if err != nil {
						logger.Error("Failed to connect to local %s: %v", localAddr, err)
						newCh.Reject(ssh.ConnectionFailed, "local service unavailable")
						return
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
							originAddrLen := int(binary.BigEndian.Uint32(extraData[offset : offset+4]))
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
						return
					}

					channelHandler.handleChannel(connCtx, ch, localConn)
				}(newCh)
			}
		}
	}()
	return nil
}

// sendKeepaliveWithTimeout 发送 keepalive 并带超时保护。
// TCP 半开（网络静默断开，无 FIN/RST）时 SendRequest 会阻塞在内核重传上长达数分钟，
// 包一层超时可以让客户端尽快判定断线并进入重连流程，缩短隧道不可用窗口。
func sendKeepaliveWithTimeout(client *ssh.Client, timeout time.Duration) error {
	reqDone := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		reqDone <- err
	}()
	select {
	case err := <-reqDone:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("keepalive timeout after %v", timeout)
	}
}

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

	// 传输层建立（阶段1新增）：transport=auto|quic|tcp
	// - auto（默认）：QUIC 探测成功走 QUIC（UDP 单 stream），失败/未启用自动回退 TCP——老服务端/受限网络零影响
	// - tcp：现状路径，一行不动
	// SSH 协议层完全一致：两个传输都只是给 ssh.NewClientConn 提供一条 net.Conn
	transportConn, transportMode, err := dialTransport(ctx, config, serverAddr)
	if err != nil {
		statsManager.RecordFailure()
		return fmt.Errorf("failed to dial (%s): %w", transportMode, err)
	}

	// SSH 握手加 deadline：ClientConfig.Timeout 对 NewClientConn 不生效，
	// 中间层「接受 TCP 但不转发」时握手会无限挂起，必须靠 deadline 失败重连。
	_ = transportConn.SetDeadline(time.Now().Add(sshHandshakeTimeout))
	clientConn, chans, reqs, err := ssh.NewClientConn(transportConn, serverAddr, sshConfig)
	if err != nil {
		statsManager.RecordFailure()
		transportConn.Close()
		return fmt.Errorf("failed to create SSH client: %w", err)
	}
	_ = transportConn.SetDeadline(time.Time{})

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
	logger.Info("Connected to %s via %s", serverAddr, transportMode)

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
		localPort:    config.LocalPort,
		statsManager: statsManager,
	}

	// 应用层压缩协商（C 方案，响应方向 client→server）：
	// 老服务端不认识该请求会回复 false，协商失败保持原始字节，行为零变化。
	if config.Compress {
		enabled, ok := negotiateCompress(client, 5*time.Second)
		if !ok {
			// 无法确定对端是否已启用压缩：断开重连，避免两端帧格式不一致
			client.Close()
			statsManager.RecordFailure()
			return fmt.Errorf("tunnel compression negotiation timeout")
		}
		channelHandler.compress = enabled
		if channelHandler.compress {
			logger.Info("Tunnel compression negotiated (client->server)")
		} else {
			logger.Debug("Tunnel compression not available, using raw transport")
		}
	}

	// 数据面（L4-b）：QUIC 传输且未显式关闭时，在 SSH 连接上建立 nwy-data-plane
	// 控制通道。老服务端不认识该 channel type 会直接拒绝 → 保留阶段1（单 stream 复用）。
	if transportMode == "quic" && dataPlaneEnabled() {
		if qc, ok := transportConn.(*quicStreamConn); ok {
			if ch, reqs, err := client.OpenChannel(DataPlaneChannelType, nil); err == nil {
				go ssh.DiscardRequests(reqs)
				dp := newTunnelDataPlane(ch, qc.conn, channelHandler)
				go dp.run(connCtx)
				logger.Info("QUIC data plane established (per-connection streams)")
			} else {
				logger.Debug("QUIC data plane unavailable (%v), using single-stream transport", err)
			}
		}
	}

	// UDP DATAGRAM（阶段5）：仅 QUIC 传输且两端协商支持 DATAGRAM 时启用。
	// 任一条不满足（TCP / 对端未启用 / 握手未协商成功）→ nil，UDP 数据面回落 channel 基线。
	var udpDatagram datagramConn
	if transportMode == "quic" {
		if dc, ok := transportConn.(datagramConn); ok && dc.SupportsDatagrams() {
			udpDatagram = dc
		}
	}

	// 转发协议分支（阶段4 扩展 --proto both）：
	// - udp：仅 UDP 业务穿透（QUIC datagram 优先，否则 channel 基线）
	// - both：单连接双栈——先 TCP 声明（现状路径原样），再 UDP 声明+数据面；
	//   同 remote_port TCP/UDP 不冲突可共存，一条 worker 承载整条隧道（daemon 键规则不变）
	// - tcp（默认）：现状路径，一行不动
	switch config.Proto {
	case "udp":
		udpSess, err := setupUDPForward(client, config, udpDatagram)
		if err != nil {
			client.Close()
			transportConn.Close()
			statsManager.RecordFailure()
			return fmt.Errorf("failed to setup UDP forward: %w", err)
		}
		go runUDPProxy(connCtx, udpSess, config)
	case "both":
		// TCP 声明：被服务端拒绝（ok=false）或发送失败 → 整条隧道失败，断开重连
		if err := setupTCPForward(client, channelHandler, connCtx, config); err != nil {
			client.Close()
			transportConn.Close()
			statsManager.RecordFailure()
			return fmt.Errorf("failed to setup TCP forward (dual-stack): %w", err)
		}
		// UDP 声明：失败视为整条隧道失败，返回错误触发重连循环
		udpSess, err := setupUDPForward(client, config, udpDatagram)
		if err != nil {
			client.Close()
			transportConn.Close()
			statsManager.RecordFailure()
			return fmt.Errorf("failed to setup UDP forward (dual-stack): %w", err)
		}
		go runUDPProxy(connCtx, udpSess, config)
		logger.Info("Dual-stack tunnel established! Remote listener: 0.0.0.0:%d (tcp+udp), Local target: %s:%d", config.RemotePort, config.LocalHost, config.LocalPort)
	default:
		if err := setupTCPForward(client, channelHandler, connCtx, config); err != nil {
			client.Close()
			transportConn.Close()
			statsManager.RecordFailure()
			return fmt.Errorf("failed to setup TCP forward: %w", err)
		}
	}

	// 转发声明全部成功 = 隧道真正建立。daemon 据此把 worker 置为 online；
	// 禁止在连接/心跳阶段提前置 online，否则服务端拒绝转发或半开时会"假在线"。
	if config.OnTunnelEstablished != nil {
		config.OnTunnelEstablished()
	}


	heartbeatTicker := time.NewTicker(60 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Shutting down by user request")
			connCancel()
			client.Close()
			transportConn.Close()
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
			transportConn.Close()
			statsManager.RecordDisconnection()
			if err := <-connErr; err != nil {
				return fmt.Errorf("connection closed: %w", err)
			}
			return fmt.Errorf("connection closed normally")
		case <-heartbeatTicker.C:
			heartbeatSuccess := false
			for retry := 0; retry < 3; retry++ {
				err := sendKeepaliveWithTimeout(client, 10*time.Second)
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
				transportConn.Close()
				statsManager.RecordDisconnection()
				return fmt.Errorf("heartbeat failed after 3 retries")
			}
		}
	}
}
