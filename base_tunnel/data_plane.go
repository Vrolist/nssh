package base_tunnel

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/Vrolist/nssh/base_core"
)

// QUIC 数据面（L4-b）：每条外部连接独占一条 QUIC stream。
//
// 阶段1（单 stream）把所有 SSH channel 与数据都塞进同一条 QUIC stream，大流量下载会
// 阻塞 channel open 等控制报文（队头阻塞）。客户端在 SSH 连接上打开一条持久控制通道
// nwy-data-plane@nssh；服务端收到外部连接后发 DATA_OPEN(conn_id)，客户端按 conn_id
// 新开一条 QUIC stream 并把本地服务的数据搬上去，与 SSH 控制报文彻底分离。
//
// 兼容：老服务端不认识该 channel type 会直接拒绝 → 本文件不启用，行为等同阶段1。
// 关闭开关：环境变量 NSSH_DATA_PLANE=off。

const (
	// DataPlaneChannelType 数据面控制通道 v1（与节点侧 nwy_node_core_server_go 约定）
	DataPlaneChannelType = "nwy-data-plane@nssh"
	// DataPlaneChannelTypeV2 数据面控制通道 v2：控制帧到达后先建流、再拨本地服务，
	// 用数据流首字节回报拨号结果——把 2s 本地拨号从「建流关键路径」上摘掉。
	DataPlaneChannelTypeV2 = "nwy-data-plane@nssh/v2"

	// dataPlaneLocalDialTimeout 拨本地服务超时：必须小于服务端 dataPlaneOpenTimeout。
	// 若 ≥ 服务端预算，本地服务卡死时服务端会先按超时 502（误导为客户端失联），
	// 客户端随后回的真实原因已无人接收——拨号失败应赶在服务端超时前回告。
	// 本机/局域网 TCP 拨号正常 <100ms，2s 已足够覆盖慢 NAS 冷盘场景。
	dataPlaneLocalDialTimeout  = 2 * time.Second
	dataPlaneStreamOpenTimeout = 5 * time.Second
	maxDataFrameFieldLen       = 65535

	dataFrameTypeOpen  = byte(1)
	dataFrameTypeError = byte(2)
	// dataFrameTypeBindPlane 控制帧：声明 bulk 数据面 plane_id（后续用独立 QUIC 连接承载数据流）
	dataFrameTypeBindPlane = byte(3)

	// v2 数据流首个字节：1=本地服务已就绪；0=失败，后跟 uint16 长度 + 原因
	dataStatusOK   = byte(0x01)
	dataStatusFail = byte(0x00)
)

// planeBindMagic bulk QUIC 连接首条 stream 的前导魔数：\x00NWP + plane_id(8B)，
// 与节点侧 dispatchQUICStream 约定一致。
var planeBindMagic = []byte{0x00, 'N', 'W', 'P'}

var dataStreamMagic = []byte{0x00, 'N', 'W', 'Y'}

// dataStreamConn 数据面连接抽象：SSH channel 与 QUIC 数据流都满足该接口。
type dataStreamConn interface {
	io.ReadWriteCloser
	CloseWrite() error
}

// dataPlaneEnabled 读取灰度开关：NSSH_DATA_PLANE=off/0/false/disabled 时关闭，默认启用。
func dataPlaneEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NSSH_DATA_PLANE"))) {
	case "off", "0", "false", "disabled":
		return false
	default:
		return true
	}
}

// dataPlanePreferredVersion 控制通道版本偏好：
//   - NSSH_DATA_PLANE_VERSION=v1/1 → 只尝试 v1（灰度回退用）
//   - 其它（含未设置）→ 优先 v2，服务端拒绝时自动回落 v1
func dataPlanePreferredVersion() int {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NSSH_DATA_PLANE_VERSION"))) {
	case "v1", "1":
		return 1
	default:
		return 2
	}
}

// encodeDataStatusOK v2 数据流状态：本地服务已就绪
func encodeDataStatusOK() []byte { return []byte{dataStatusOK} }
// encodeDataStatusFail v2 数据流状态：本地服务不可用（reason 随状态字节回告）
func encodeDataStatusFail(reason string) []byte {
	msg := []byte(reason)
	if len(msg) > maxDataFrameFieldLen {
		msg = msg[:maxDataFrameFieldLen]
	}
	buf := make([]byte, 3+len(msg))
	buf[0] = dataStatusFail
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(msg)))
	copy(buf[3:], msg)
	return buf
}

// encodePlaneBindFrame 控制帧：type=3 + plane_id(8B)
func encodePlaneBindFrame(planeID uint64) []byte {
	buf := make([]byte, 9)
	buf[0] = dataFrameTypeBindPlane
	binary.BigEndian.PutUint64(buf[1:], planeID)
	return buf
}

// dataPlaneIsolationEnabled 灰度开关（默认关闭）：
// NSSH_DATA_PLANE_ISOLATION=on/1/true 时，数据流走独立 QUIC 连接（bulk），
// 与 SSH 控制报文（含 DATA_OPEN 帧）分连接隔离，避免大流量挤占连接级流控窗口。
func dataPlaneIsolationEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NSSH_DATA_PLANE_ISOLATION"))) {
	case "on", "1", "true", "yes", "enabled":
		return true
	default:
		return false
	}
}

// dataOpenFrame 解析后的 DATA_OPEN 帧
type dataOpenFrame struct {
	connID     uint64
	port       uint32
	originHost string
	originPort uint32
}

// readDataOpenFrame 从控制通道读取一帧；非 DATA_OPEN 帧返回 (nil, nil) 由调用方忽略。
func readDataOpenFrame(r io.Reader) (*dataOpenFrame, error) {
	hdr := make([]byte, 9)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	typ := hdr[0]
	connID := binary.BigEndian.Uint64(hdr[1:9])

	switch typ {
	case dataFrameTypeOpen:
		meta := make([]byte, 6)
		if _, err := io.ReadFull(r, meta); err != nil {
			return nil, err
		}
		port := binary.BigEndian.Uint32(meta[0:4])
		hostLen := int(binary.BigEndian.Uint16(meta[4:6]))
		host := make([]byte, hostLen)
		if _, err := io.ReadFull(r, host); err != nil {
			return nil, err
		}
		portBuf := make([]byte, 4)
		if _, err := io.ReadFull(r, portBuf); err != nil {
			return nil, err
		}
		return &dataOpenFrame{
			connID:     connID,
			port:       port,
			originHost: string(host),
			originPort: binary.BigEndian.Uint32(portBuf),
		}, nil
	default:
		// 未知帧类型：长度不可知，无法安全跳过 → 按协议错误断开
		return nil, fmt.Errorf("unknown data plane frame type %d", typ)
	}
}

// encodeDataErrorFrame 编码 DATA_ERROR（本地服务不可用等）
func encodeDataErrorFrame(connID uint64, reason string) []byte {
	msg := []byte(reason)
	if len(msg) > maxDataFrameFieldLen {
		msg = msg[:maxDataFrameFieldLen]
	}
	buf := make([]byte, 1+8+2+len(msg))
	buf[0] = dataFrameTypeError
	binary.BigEndian.PutUint64(buf[1:9], connID)
	binary.BigEndian.PutUint16(buf[9:11], uint16(len(msg)))
	copy(buf[11:], msg)
	return buf
}

// tunnelDataPlane 客户端数据面：控制通道 + 所属 QUIC 连接 + 本地转发目标。
type tunnelDataPlane struct {
	control    io.ReadWriteCloser
	qconn      quic.Connection
	handler    *ChannelHandler
	localAddr  string
	version    int
	serverAddr string

	bulkMu   sync.RWMutex
	bulkConn quic.Connection

	writeMu   sync.Mutex
	closeOnce sync.Once
}

func newTunnelDataPlane(control io.ReadWriteCloser, qconn quic.Connection, handler *ChannelHandler, version ...int) *tunnelDataPlane {
	v := 1
	if len(version) > 0 && version[0] > 0 {
		v = version[0]
	}
	return &tunnelDataPlane{
		control:   control,
		qconn:     qconn,
		handler:   handler,
		localAddr: fmt.Sprintf("%s:%d", handler.localHost, handler.localPort),
		version:   v,
	}
}

// dataConnection 当前承载数据流的 QUIC 连接：
// 隔离模式绑定成功时用 bulk 连接，否则沿用 SSH 所在连接（行为与旧版一致）。
func (dp *tunnelDataPlane) dataConnection() quic.Connection {
	dp.bulkMu.RLock()
	defer dp.bulkMu.RUnlock()
	if dp.bulkConn != nil {
		return dp.bulkConn
	}
	return dp.qconn
}

// setupIsolation 建立独立 bulk QUIC 连接（P3）：
// 先通过控制帧声明 plane_id，再另起一条 QUIC 连接发送 planeBindMagic+plane_id 绑定。
// 任一步失败都静默回退（数据流继续走 SSH 所在连接），不影响隧道可用性。
func (dp *tunnelDataPlane) setupIsolation(ctx context.Context) {
	logger := base_core.GetLogger()
	if dp.serverAddr == "" {
		return
	}
	var idBuf [8]byte
	if _, err := cryptorand.Read(idBuf[:]); err != nil {
		logger.Debug("Data plane isolation: generate plane id failed: %v", err)
		return
	}
	planeID := binary.BigEndian.Uint64(idBuf[:])
	if planeID == 0 {
		return
	}
	dp.writeMu.Lock()
	_, err := dp.control.Write(encodePlaneBindFrame(planeID))
	dp.writeMu.Unlock()
	if err != nil {
		logger.Debug("Data plane isolation: send bind frame failed: %v", err)
		return
	}

	conn, err := dialQUICConnection(ctx, dp.serverAddr, QUICDialTimeout)
	if err != nil {
		logger.Debug("Data plane isolation: dial bulk QUIC failed: %v", err)
		return
	}
	openCtx, cancel := context.WithTimeout(ctx, dataPlaneStreamOpenTimeout)
	stream, err := conn.OpenStreamSync(openCtx)
	cancel()
	if err != nil {
		_ = conn.CloseWithError(0, "open bind stream failed")
		logger.Debug("Data plane isolation: open bind stream failed: %v", err)
		return
	}
	header := make([]byte, len(planeBindMagic)+8)
	copy(header, planeBindMagic)
	binary.BigEndian.PutUint64(header[len(planeBindMagic):], planeID)
	if _, err := stream.Write(header); err != nil {
		_ = conn.CloseWithError(0, "write bind header failed")
		logger.Debug("Data plane isolation: write bind header failed: %v", err)
		return
	}

	dp.bulkMu.Lock()
	dp.bulkConn = conn
	dp.bulkMu.Unlock()
	logger.Info("Data plane isolation enabled (bulk plane=%d)", planeID)

	// 隧道结束时关闭 bulk 连接（绑定流不主动关闭，作为连接存活锚点）
	go func() {
		<-ctx.Done()
		_ = conn.CloseWithError(0, "tunnel closed")
	}()
}

// run 读取控制帧（DATA_OPEN）→ 为每条外部连接新开 QUIC stream。
func (dp *tunnelDataPlane) run(ctx context.Context) {
	logger := base_core.GetLogger()
	if dataPlaneIsolationEnabled() {
		go dp.setupIsolation(ctx)
	}
	for {
		f, err := readDataOpenFrame(dp.control)
		if err != nil {
			logger.Debug("Data plane control channel closed: %v", err)
			dp.close()
			return
		}
		if f == nil {
			continue
		}
		if dp.version >= 2 {
			go dp.openDataStreamV2(ctx, f)
		} else {
			go dp.openDataStream(ctx, f)
		}
	}
}

// openDataStreamV2 v2：先建 QUIC 数据流并写 magic+conn_id，再拨本地服务，
// 用首字节 0x01/0x00+reason 回报拨号结果。
//
// 为什么改顺序：v1 在拨本地服务（最坏 2s）之后才建流，服务端只能干等到超时；
// 并发首屏时多个 2s 拨号叠加成同一屏障（线上表现为同页资源同时慢 7s）。
// v2 让建流与拨号并行可感知：服务端拿到流后最多等状态字节，失败也能立刻拿到原因。
func (dp *tunnelDataPlane) openDataStreamV2(ctx context.Context, f *dataOpenFrame) {
	logger := base_core.GetLogger()

	streamCtx, cancel := context.WithTimeout(ctx, dataPlaneStreamOpenTimeout)
	stream, err := dp.dataConnection().OpenStreamSync(streamCtx)
	cancel()
	if err != nil {
		logger.Error("Data plane v2: open data stream failed: %v", err)
		dp.sendError(f.connID, "open data stream failed: "+err.Error())
		return
	}

	header := make([]byte, len(dataStreamMagic)+8)
	copy(header, dataStreamMagic)
	binary.BigEndian.PutUint64(header[len(dataStreamMagic):], f.connID)
	if _, err := stream.Write(header); err != nil {
		_ = stream.Close()
		dp.sendError(f.connID, "write data stream header failed: "+err.Error())
		return
	}

	localConn, err := net.DialTimeout("tcp", dp.localAddr, dataPlaneLocalDialTimeout)
	if err != nil {
		logger.Error("Data plane v2: failed to connect local %s: %v", dp.localAddr, err)
		_, _ = stream.Write(encodeDataStatusFail("local service unavailable: " + err.Error()))
		_ = stream.Close()
		dp.sendError(f.connID, "local service unavailable: "+err.Error())
		return
	}
	if tc, ok := localConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	if _, err := stream.Write(encodeDataStatusOK()); err != nil {
		// 状态字节写不出去说明服务端已放弃该流，直接回收本地连接
		_ = stream.Close()
		localConn.Close()
		dp.sendError(f.connID, "write data stream status failed: "+err.Error())
		return
	}

	dp.handler.handleChannel(ctx, &quicDataConn{Stream: stream}, localConn)
}

// openDataStream 拨本地服务 + 新开 QUIC stream + 双向搬运。
func (dp *tunnelDataPlane) openDataStream(ctx context.Context, f *dataOpenFrame) {
	logger := base_core.GetLogger()

	localConn, err := net.DialTimeout("tcp", dp.localAddr, dataPlaneLocalDialTimeout)
	if err != nil {
		logger.Error("Data plane: failed to connect local %s: %v", dp.localAddr, err)
		dp.sendError(f.connID, "local service unavailable: "+err.Error())
		return
	}
	if tc, ok := localConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	streamCtx, cancel := context.WithTimeout(ctx, dataPlaneStreamOpenTimeout)
	stream, err := dp.dataConnection().OpenStreamSync(streamCtx)
	cancel()
	if err != nil {
		localConn.Close()
		dp.sendError(f.connID, "open data stream failed: "+err.Error())
		return
	}

	header := make([]byte, len(dataStreamMagic)+8)
	copy(header, dataStreamMagic)
	binary.BigEndian.PutUint64(header[len(dataStreamMagic):], f.connID)
	if _, err := stream.Write(header); err != nil {
		_ = stream.Close()
		localConn.Close()
		dp.sendError(f.connID, "write data stream header failed: "+err.Error())
		return
	}

	dp.handler.handleChannel(ctx, &quicDataConn{Stream: stream}, localConn)
}

// sendError 回告服务端本次数据流建立失败
func (dp *tunnelDataPlane) sendError(connID uint64, reason string) {
	frame := encodeDataErrorFrame(connID, reason)
	dp.writeMu.Lock()
	defer dp.writeMu.Unlock()
	if _, err := dp.control.Write(frame); err != nil {
		dp.close()
	}
}

func (dp *tunnelDataPlane) close() {
	dp.closeOnce.Do(func() {
		_ = dp.control.Close()
	})
}

// quicDataConn 把客户端 QUIC 数据流适配为 dataStreamConn。
type quicDataConn struct {
	quic.Stream
}

// CloseWrite 半关闭写方向（发送 FIN），读方向继续
func (c *quicDataConn) CloseWrite() error {
	return c.Stream.Close()
}

// Close 关闭双向
func (c *quicDataConn) Close() error {
	_ = c.Stream.Close()
	c.Stream.CancelRead(0)
	return nil
}
