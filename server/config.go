package server

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// Config represents the structure of the TOML file
type L5proxyConfig struct {
	Server      ServerTomlConfig     `toml:"server"`
	Accounts    []*AccountTomlConfig `toml:"account"`
	UdpPortMaps []*PortMapTomlConfig `toml:"udpmap"`
	TcpPortMaps []*PortMapTomlConfig `toml:"tcpmap"`
}

type ServerTomlConfig struct {
	Address       string `toml:"address"`
	WebsocketPath string `toml:"websocket"`
	Daemon        bool   `toml:"daemon"`
	LogLevel      string `toml:"loglevel"`
}

type AccountTomlConfig struct {
	UUID      string `toml:"uuid"`
	RateLimit int    `toml:"ratelimit"`
	MaxTunnel int    `toml:"maxtunnel"`
	RelayURL  string `toml:"relay"`
}

type PortMapTomlConfig struct {
	Endpoint    string                 `toml:"endpoint"`
	AddressMaps []AddressMapTomlConfig `toml:"addressmap"`
}

type AddressMapTomlConfig struct {
	External string `toml:"external"`
	Internal string `toml:"internal"`
}

func ParseConfig(filePath string) (*L5proxyConfig, error) {
	if len(filePath) == 0 {
		return nil, fmt.Errorf("config file path can not empty")
	}

	var config L5proxyConfig

	// Read and decode the TOML file
	if _, err := toml.DecodeFile(filePath, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
