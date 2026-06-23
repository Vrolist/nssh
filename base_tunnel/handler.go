package base_tunnel

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Vrolist/nssh/base_core"
)

const copyBufferSize = 128 * 1024

func (h *ChannelHandler) handleChannel(ctx context.Context, ch ssh.Channel, localConn net.Conn) {
	logger := base_core.GetLogger()
	defer localConn.Close()
	defer ch.Close()

	logger.Info("[HANDLER] Channel handler started")

	if tcpConn, ok := localConn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
		tcpConn.SetLinger(0)
		tcpConn.SetNoDelay(true)
		tcpConn.SetReadBuffer(256 * 1024)
		tcpConn.SetWriteBuffer(256 * 1024)
	}

	buf1 := make([]byte, copyBufferSize)
	buf2 := make([]byte, copyBufferSize)

	var wg sync.WaitGroup
	var once sync.Once
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, err := io.CopyBuffer(localConn, ch, buf1)
		if err != nil {
			logger.Debug("[HANDLER] SSH->local error: %v", err)
		}
		logger.Info("[HANDLER] SSH->local copy completed, bytes=%d", n)
		once.Do(func() {
			if tcpConn, ok := localConn.(*net.TCPConn); ok {
				tcpConn.CloseWrite()
			}
		})
	}()

	go func() {
		defer wg.Done()
		n, err := io.CopyBuffer(ch, localConn, buf2)
		if err != nil {
			logger.Debug("[HANDLER] local->SSH error: %v", err)
		}
		logger.Info("[HANDLER] local->SSH copy completed, bytes=%d", n)
		once.Do(func() {
			ch.CloseWrite()
		})
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("[HANDLER] Both directions completed")
	case <-ctx.Done():
		logger.Info("[HANDLER] Context cancelled, forcing close")
		localConn.Close()
		ch.Close()
		select {
		case <-done:
			logger.Info("[HANDLER] Goroutines cleaned up")
		case <-time.After(5 * time.Second):
			logger.Warn("[HANDLER] Timeout waiting for goroutines to finish")
		}
	}
}
