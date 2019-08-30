package ssh

import (
	"bytes"
	"io/ioutil"
	"net"
	"testing"
	"time"
)

var (
	local          = "127.0.0.1:12379"
	sshServer      = ""
	remoteServer   = "127.0.0.1:2379"
	privateKeyPath = ""
)

func TestNewTunnel(t *testing.T) {
	keyFile, err := ioutil.ReadFile(privateKeyPath)
	if err != nil {
		t.Errorf("open private key failed, err: %s", err.Error())
		return
	}

	_, err = NewTunnel(local, sshServer, remoteServer, bytes.NewBuffer(keyFile), nil)
	if err != nil {
		t.Errorf("can not init a tunnel, error: %s", err.Error())
		return
	}
}


func TestTunnel_Up(t *testing.T) {
	keyFile, err := ioutil.ReadFile(privateKeyPath)
	if err != nil {
		t.Errorf("open private key failed, err: %s", err.Error())
		return
	}

	tn, _ := NewTunnel(
		local, sshServer, remoteServer, bytes.NewBuffer(keyFile), nil)
	go func() {
		tn.Up()
	}()
	defer tn.Down()
	time.Sleep(time.Second)
	conn, err := net.Dial("tcp", local)
	if err != nil {
		t.Errorf("can not connect to a tunnel, err: %s", err.Error())
		return
	}
	_, err = conn.Write([]byte("hello world"))
	if err != nil {
		t.Errorf("can not write to tunnel, error: %s", err.Error())
	}
}

