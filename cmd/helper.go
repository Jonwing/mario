package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"os/user"
	"path"
)

func GetUserHome() string {
	u, err := user.Current()
	if err != nil {
		return path.Dir(".")
	}
	return u.HomeDir
}

func PrivateKey(path string) (io.Reader, error) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return bytes.NewBuffer(file), nil
}

type tConfigs struct {
	// TunnelTimeout timeout for a tunnel in seconds
	TunnelTimeout int `json:"tunnel_timeout,omitempty"`
	// Tunnels list of tunnel config
	Tunnels []*tConfig `json:"tunnels"`
}

type tConfig struct {
	Name string `json:"name"`

	Local string `json:"local"`

	SshServer string `json:"ssh_server"`

	MapTo string `json:"map_to"`

	PrivateKey string `json:"private_key,omitempty"`

	DontConnect bool `json:"do_not_connect,omitempty"`
}

func LoadJsonConfig(path string) (*tConfigs, error) {
	newCfg := &tConfigs{Tunnels: make([]*tConfig, 0)}
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(content, newCfg)
	if err != nil {
		return nil, err
	}
	return newCfg, nil
}
