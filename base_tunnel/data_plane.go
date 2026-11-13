package base_tunnel

import (
	"context"
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
	// DataPlaneChannelType 数据面控制通道类型（与节点侧 nwy_node_core_server_go 约定）
	DataPlaneChannelType = "nwy-data-plane@nssh"

	// dataPlaneLocalDialTimeout 拨本地服务超时：必须小于服务端 dataPlaneOpenTimeout（3s）。
	// 若 ≥ 3s，本地服务卡死时服务端会先按 data_stream_timeout 502（误导为客户端失联），
	// 客户端随后回的真实原因 DATA_ERROR 已无人接收——拨号失败应赶在服务端超时前回告。
	// 本机/局域网 TCP 拨号正常 <100ms，2s 已足够覆盖慢 NAS 冷盘场景。
	dataPlaneLocalDialTimeout  = 2 * time.Second
	dataPlaneStreamOpenTimeout = 5 * time.Second
	maxDataFrameFieldLen       = 65535

	dataFrameTypeOpen  = byte(1)
	dataFrameTypeError = byte(2)
)

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
	control   io.ReadWriteCloser
	qconn     quic.Connection
	handler   *ChannelHandler
	localAddr string

	writeMu   sync.Mutex
	closeOnce sync.Once
}

func newTunnelDataPlane(control io.ReadWriteCloser, qconn quic.Connection, handler *ChannelHandler) *tunnelDataPlane {
	return &tunnelDataPlane{
		control:   control,
		qconn:     qconn,
		handler:   handler,
		localAddr: fmt.Sprintf("%s:%d", handler.localHost, handler.localPort),
	}
}

// run 读取控制帧（DATA_OPEN）→ 为每条外部连接新开 QUIC stream。
func (dp *tunnelDataPlane) run(ctx context.Context) {
	logger := base_core.GetLogger()
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
		go dp.openDataStream(ctx, f)
	}
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
	stream, err := dp.qconn.OpenStreamSync(streamCtx)
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
