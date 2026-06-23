package daemon

import (
	"context"
	"time"

	"github.com/Vrolist/nssh/base_core"
)

const (
	CMD_START          = 0x01
	CMD_STOP           = 0x02
	CMD_RESTART        = 0x04
	CMD_LIST           = 0x05
	CMD_LOG            = 0x06
	CMD_STOP_ALL       = 0x08
	CMD_REGISTER       = 0x0A
	CMD_GET_COMMAND    = 0x0C
	CMD_GET_DAEMON_PID = 0x0D
	CMD_GET_VERSION    = 0x0E
	CMD_TAKEOVER       = 0x20
)

type WorkerInfo struct {
	ID              string              `json:"id"`
	CancelFunc      context.CancelFunc `json:"-"`
	ErrorChan       chan error         `json:"-"`
	DoneChan        chan struct{}      `json:"-"`
	Username        string              `json:"username"`
	ServerHost      string              `json:"server_host"`
	ServerPort      int                 `json:"server_port"`
	LocalHost       string              `json:"local_host"`
	LocalPort       int                 `json:"local_port"`
	RemotePort      int                 `json:"remote_port"`
	Password        string              `json:"password"`
	SSHKeyPath      string              `json:"ssh_key_path"`
	StartTime       time.Time           `json:"start_time"`
	Status          string              `json:"status"`
	LastRegisterTime time.Time          `json:"last_register_time"`
	StatusChangeTime time.Time          `json:"status_change_time"`
	StatsManager    *base_core.StatsManager `json:"-"`
	LastConnectTime time.Time           `json:"last_connect_time"`
	ReconnectNeeded bool                 `json:"-"`
	Config          *base_core.Config   `json:"-"`
	OfflineCount    int                 `json:"offline_count"`
}
