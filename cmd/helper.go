package cmd

import (
	"bytes"
	"github.com/sirupsen/logrus"
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

type log struct {}

func (l *log) Debugf(format string, args ...interface{}) {
	logrus.Debugf(format, args...)
}

func (l *log) Infof(format string, args ...interface{}) {
	logrus.Infof(format, args...)
}

func (l *log) Errorf(format string, args ...interface{}) {
	logrus.Errorf(format, args...)
}


type tConfigs struct {
	Tunnels []*tConfig	`json:"tunnels"`
}

type tConfig struct {
	Name string	`json:"name"`

	Local string `json:"local"`

	SshServer string	`json:"ssh_server"`

	MapTo string	`json:"map_to"`

	PrivateKey string	`json:"private_key,omitempty"`
}


