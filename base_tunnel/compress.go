package base_tunnel

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
	"time"

	"golang.org/x/crypto/ssh"
)

// ==================== 隧道应用层压缩（C 方案，响应方向 client→server） ====================
//
// 压缩「隧道客户端 → 节点服务端」这一段（源站响应经家宽上行回传的方向），
// 外部用户与网关完全无感：服务端解压后得到原始 HTTP 字节流。
//
// SSH 传输层本身不可压缩（x/crypto/ssh 不支持 compression，服务端 fork 也只协商 none），
// 因此在 SSH channel 之上做应用层分块压缩：
//   - 连接建立后发送全局请求 nwy-compress@nwy 协商；
//   - 对端同意后，local→SSH 方向的每次写入按块压缩成帧；
//   - 视频/图片等已压缩内容压不动时自动回退原始帧，服务端按帧标志透明处理。
//
// 帧格式（5 字节头 + 负载）：
//   [0:4] payload 长度（大端 uint32）
//   [4]   flags（bit0=1 表示负载为 raw deflate）
//   [5:]  payload
const (
	// compressRequestName 压缩能力协商请求名（与服务端约定一致）
	compressRequestName = "nwy-compress@nwy"

	// compressFrameHeaderSize 帧头长度：4 字节长度 + 1 字节标志
	compressFrameHeaderSize = 5

	// compressBlockSize 单块最大原始字节数（块内独立压缩，块间无字典依赖）
	compressBlockSize = 64 * 1024

	// compressMinBlockSize 小于该值的块直接走原始帧（省 CPU，避免帧头比例过高）
	compressMinBlockSize = 256

	// compressFrameFlagCompressed 帧标志位：置位表示负载是 raw deflate
	compressFrameFlagCompressed byte = 0x01

	// compressBadBlockLimit 连续压不下去的块数达到该值后永久回退原始透传
	compressBadBlockLimit = 2
)

// negotiateCompress 向服务端发起压缩协商，带超时保护。
// 返回 (enabled, ok)：
//   - enabled=true：对端同意，后续按帧发送
//   - enabled=false, ok=true：老服务端不认识该请求（回复 false），保持原始字节
//   - ok=false：请求超时/连接异常，无法确定对端状态，调用方应断开重连，
//     避免「服务端已启用、客户端按原始字节发送」造成数据错乱
func negotiateCompress(client *ssh.Client, timeout time.Duration) (bool, bool) {
	done := make(chan bool, 1)
	go func() {
		ok, _, err := client.SendRequest(compressRequestName, true, nil)
		if err != nil {
			done <- false
			return
		}
		done <- ok
	}()
	select {
	case ok := <-done:
		return ok, true
	case <-time.After(timeout):
		return false, false
	}
}

// compressWriter 把写入的原始字节按块压缩成帧后写入 SSH channel。
// 每个 channel 独立实例；视频等已压缩内容自适应回退为原始帧。
type compressWriter struct {
	dst io.Writer

	deflate   bytes.Buffer
	fw        *flate.Writer
	out       []byte
	rawStreak int
	rawOnly   bool
}

func newCompressWriter(dst io.Writer) *compressWriter {
	w := &compressWriter{dst: dst}
	fw, err := flate.NewWriter(&w.deflate, 5)
	if err != nil {
		// 固定级别不会失败，兜底为原始透传
		w.rawOnly = true
		return w
	}
	w.fw = fw
	return w
}

func (w *compressWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > compressBlockSize {
			n = compressBlockSize
		}
		if err := w.writeBlock(p[:n]); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (w *compressWriter) writeBlock(block []byte) error {
	if !w.rawOnly && w.fw != nil && len(block) >= compressMinBlockSize {
		w.deflate.Reset()
		w.fw.Reset(&w.deflate)
		if _, err := w.fw.Write(block); err != nil {
			return err
		}
		if err := w.fw.Close(); err != nil {
			return err
		}
		compressed := w.deflate.Bytes()
		if len(compressed) < len(block) {
			w.rawStreak = 0
			return w.writeCompressedFrame(compressed)
		}
		// 压不下去：连续多块后永久回退原始透传（保护视频/已压缩内容）
		w.rawStreak++
		if w.rawStreak >= compressBadBlockLimit {
			w.rawOnly = true
		}
	}
	return w.writeRawFrame(block)
}

func (w *compressWriter) writeCompressedFrame(payload []byte) error {
	total := compressFrameHeaderSize + len(payload)
	if cap(w.out) < total {
		w.out = make([]byte, total)
	} else {
		w.out = w.out[:total]
	}
	binary.BigEndian.PutUint32(w.out[0:4], uint32(len(payload)))
	w.out[4] = compressFrameFlagCompressed
	copy(w.out[compressFrameHeaderSize:], payload)
	return writeFull(w.dst, w.out)
}

func (w *compressWriter) writeRawFrame(payload []byte) error {
	var header [compressFrameHeaderSize]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(len(payload)))
	header[4] = 0
	if err := writeFull(w.dst, header[:]); err != nil {
		return err
	}
	return writeFull(w.dst, payload)
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
