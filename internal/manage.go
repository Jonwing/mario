package internal

import (
	"bytes"
	"github.com/Jonwing/mario/pkg/ssh"
	"io/ioutil"
	"os/user"
	"path"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	actOpen = act(iota)
	actClose
	actReconnect
)


var status = map[ssh.TunnelStatus]string{
	ssh.StatusNew: "new",
	ssh.StatusConnecting: "connecting",
	ssh.StatusConnected: "connected",
	ssh.StatusConnectFailed: "connect failed",
	ssh.StatusClosed: "closed",
	ssh.StatusReconnecting: "reconnecting",
	ssh.StatusLost: "lost",
}

type act int

type tnAction struct {
	act act
	tn *TunnelInfo
}


type TunnelInfo struct {
	t *ssh.Tunnel
	id int
	name string
	privateKey string
	mario *Mario
}

func (t *TunnelInfo) GetID() int {
	return t.id
}

func (t *TunnelInfo) GetName() string {
	return t.name
}

func (t *TunnelInfo) GetPrivateKeyPath() string {
	return t.privateKey
}

func (t *TunnelInfo) GetLocalPort() int {
	return t.t.LocalPort
}

func (t *TunnelInfo) GetServer() string {
	return t.t.User() + "@" + t.t.SSHUri
}

func (t *TunnelInfo) GetRemote() string {
	return t.t.ForwardTo
}

func (t *TunnelInfo) GetStatus() string {
	st, ok := status[t.t.Status]
	if !ok {
		return "unknown"
	}
	return st
}

func (t *TunnelInfo) Represent() string {
	return t.t.String()
}

func (t *TunnelInfo) Error() error {
	return t.t.Error()
}

func (t *TunnelInfo) Close() error {
	return t.mario.Close(t)
}

func (t *TunnelInfo) Up() error {
	return t.mario.Up(t)
}


type logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

type Mario struct {
	tunnelCount int32

	CheckAliveInterval time.Duration

	// the global private key file path
	KeyPath string

	keyBuf []byte

	actions chan *tnAction

	// this channel is used to broadcast tunnel status
	publishWrapper chan *TunnelInfo

	// updatedTunnels receives tunnels whose status have been changed
	updatedTunnels chan *ssh.Tunnel

	logger logger

	wrappers map[*ssh.Tunnel]*TunnelInfo

	wm sync.RWMutex

	stop chan struct{}
}

func (m *Mario) handleTunnel(t *ssh.Tunnel) {
	m.updatedTunnels <- t
}

func (m *Mario) wrap(t *ssh.Tunnel) *TunnelInfo {
	id := atomic.AddInt32(&m.tunnelCount, 1)
	return &TunnelInfo{id: int(id), t: t, name: strconv.Itoa(int(id)), mario: m}
}

func (m *Mario) Establish(name string, localPort int, server, remote string, pk string) (*TunnelInfo, error){
	var key *bytes.Buffer
	if pk == "" {
		if m.keyBuf == nil {
			keyFile, err := ioutil.ReadFile(m.KeyPath)
			if err != nil {
				return nil, err
			}
			m.keyBuf = keyFile
		}
		key = bytes.NewBuffer(m.keyBuf)
	} else {
		keyBytes, err := ioutil.ReadFile(pk)
		if err != nil {
			return nil, err
		}
		key = bytes.NewBuffer(keyBytes)
	}

	tn, err := ssh.NewTunnel(localPort, server, remote, key, m.handleTunnel)
	if err != nil {
		return nil, err
	}

	tw := m.wrap(tn)
	if name != "" {
		tw.name = name
	}

	if pk != "" {
		tw.privateKey = pk
	}

	m.actions <- &tnAction{act: actOpen, tn:  tw}
	go tn.Up()
	return tw, nil
}

func (m *Mario) Up(tn *TunnelInfo) (err error) {
	if tn.t.Status != ssh.StatusLost && tn.t.Status != ssh.StatusClosed {
		return
	}
	m.actions <- &tnAction{tn:tn, act:actReconnect}
	return nil
}

func (m *Mario) Close(tn *TunnelInfo) (err error) {
	m.actions <- &tnAction{
		act: actClose,
		tn:  tn,
	}
	return nil
}

func (m *Mario) Monitor() (<-chan *TunnelInfo, error) {
	ticker := time.NewTicker(m.CheckAliveInterval)
	keyFile, err := ioutil.ReadFile(m.KeyPath)
	if err != nil {
		return nil, err
	}
	m.keyBuf = keyFile
	go func() {
		for {
			select {
			case action := <-m.actions:
				switch action.act {
				case actOpen:
					m.wrappers[action.tn.t] = action.tn
				case actClose:
					action.tn.t.Down()
				case actReconnect:
					go action.tn.t.Reconnect()
				}
			case raw := <-m.updatedTunnels:
				wrapped, ok := m.wrappers[raw]
				if !ok {
					wrapped = m.wrap(raw)
					wrapped.name = "unknown"
					m.wrappers[wrapped.t] = wrapped
				}
				m.publishWrapper <- wrapped
			case <-ticker.C:
				for t := range m.wrappers {
					go t.KeepAlive()
				}
				case <-m.stop:
					break
			}
		}
	}()
	return m.publishWrapper, nil
}


func NewMario(pkPath string, heartbeat time.Duration, logger logger) *Mario {
	if heartbeat < 15*time.Second {
		heartbeat = 15*time.Second
	}
	if pkPath == "" {
		u, err := user.Current()
		if err == nil {
			pkPath = path.Join(u.HomeDir, ".ssh/id_rsa")
		}
	}
	m := &Mario{
		tunnelCount:        0,
		CheckAliveInterval: heartbeat,
		KeyPath:            pkPath,
		actions: 			make(chan *tnAction, 16),
		updatedTunnels:     make(chan *ssh.Tunnel, 32),
		publishWrapper:		make(chan *TunnelInfo, 32),
		logger:             logger,
		wrappers:           make(map[*ssh.Tunnel]*TunnelInfo),
		wm:                 sync.RWMutex{},
		stop:               make(chan struct{}),
	}
	return m
}

