package sdk_go

type Config struct {
	Username       string
	Password       string
	ServerHost     string
	ServerPort     int
	LocalHost      string
	LocalPort      int
	RemotePort     int
	ReconnectDelay int
}

func NewConfig(username, password, serverHost string, serverPort, localPort int) *Config {
	return &Config{
		Username:       username,
		Password:       password,
		ServerHost:     serverHost,
		ServerPort:     serverPort,
		LocalHost:      "localhost",
		LocalPort:      localPort,
		RemotePort:     0,
		ReconnectDelay: 60,
	}
}

func (c *Config) SetLocalHost(host string) *Config {
	c.LocalHost = host
	return c
}

func (c *Config) SetRemotePort(port int) *Config {
	c.RemotePort = port
	return c
}

func (c *Config) SetReconnectDelay(delay int) *Config {
	c.ReconnectDelay = delay
	return c
}

func NewSimpleConfig(username, password, serverHost string, serverPort int,
	localHost string, localPort, remotePort int) *Config {

	return &Config{
		Username:       username,
		Password:       password,
		ServerHost:     serverHost,
		ServerPort:     serverPort,
		LocalHost:      localHost,
		LocalPort:      localPort,
		RemotePort:     remotePort,
		ReconnectDelay: 60,
	}
}
