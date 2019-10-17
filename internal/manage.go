package internal

import (
	"bytes"
	"errors"
	"github.com/Jonwing/mario/pkg/ssh"
	"github.com/sirupsen/logrus"
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
	ssh.StatusNew:          "new",
	ssh.StatusConnecting:   "connecting",
	ssh.StatusConnected:    "connected",
	ssh.StatusClosed:       "closed",
	ssh.StatusReconnecting: "reconnecting",
	ssh.StatusError:        "error",
}

type act int

type tnAction struct {
	act act
	tn  *TunnelInfo
	err chan error
}

func newAction(tn *TunnelInfo, action act, errBack chan error) *tnAction {
	a := &tnAction{
		act: action,
		tn:  tn,
		err: errBack,
	}
	return a
}

type TunnelInfo struct {
	t          *ssh.Tunnel
	id         int
	name       string
	privateKey string
	mario      *Mario
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

func (t *TunnelInfo) GetLocal() string {
	return t.t.Local
}

func (t *TunnelInfo) GetServer() string {
	return t.t.User() + "@" + t.t.SSHUri
}

func (t *TunnelInfo) GetRemote() string {
	return t.t.ForwardTo
}

func (t *TunnelInfo) GetStatus() string {
	st, ok := status[t.t.Status()]
	if t.t.Error() != nil {
		return "error"
	}
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

func (t *TunnelInfo) Close(waitDone chan error) {
	t.mario.Close(t, waitDone)
}

func (t *TunnelInfo) Up(waitDone chan error) {
	t.mario.Up(t, waitDone)
}

func (t *TunnelInfo) Connections() []*ssh.Connector {
	return t.t.GetConnectors()
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

func (m *Mario) Establish(name string, local, server, remote string, pk string) (*TunnelInfo, error) {
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

	tn, err := ssh.NewTunnel(local, server, remote, key, m.handleTunnel, m.CheckAliveInterval)
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

	m.wm.Lock()
	m.wrappers[tn] = tw
	m.wm.Unlock()
	go tn.Up()
	return tw, nil
}

func (m *Mario) Up(tn *TunnelInfo, waitDone chan error) {
	if tn == nil {
		waitDone <- errors.New("nil tn")
		return
	}
	if tn.t.Status() == ssh.StatusConnected {
		waitDone <- nil
		return
	}
	at := newAction(tn, actReconnect, waitDone)
	m.actions <- at
}

func (m *Mario) Close(tn *TunnelInfo, waitDone chan error) {
	if tn == nil {
		waitDone <- errors.New("nil tn")
		return
	}
	at := newAction(tn, actClose, waitDone)
	m.actions <- at
}

func (m *Mario) ApplyAll(action act, waitDone bool) {
	m.wm.RLock()
	waiting := make(chan error, len(m.wrappers))
	for tn := range m.wrappers {
		if action == actReconnect {
			tn.Reconnect(waiting)
		} else {
			tn.Down(waiting)
		}
	}
	m.wm.RUnlock()
	if !waitDone {
		return
	}
	count := 0
	for range waiting {
		count++
		if count == len(m.wrappers) {
			break
		}
	}
}

func (m *Mario) Monitor() (<-chan *TunnelInfo, error) {
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
					action.tn.t.Down(action.err)
				case actReconnect:
					action.tn.t.Reconnect(action.err)
				}
			case raw := <-m.updatedTunnels:
				m.wm.RLock()
				wrapped, ok := m.wrappers[raw]
				m.wm.RUnlock()
				if !ok {
					wrapped = m.wrap(raw)
					wrapped.name = "unknown"
					m.wm.Lock()
					m.wrappers[wrapped.t] = wrapped
					m.wm.Unlock()
				}
				m.publishWrapper <- wrapped
			case <-m.stop:
				break
			}
		}
	}()
	return m.publishWrapper, nil
}

func (m *Mario) Stop() {
	logrus.Debugln("Mario stop")
	m.stop <- struct{}{}
	m.wm.RLock()
	waiting := make(chan error, len(m.wrappers))
	for tn := range m.wrappers {
		tn.Down(waiting)
	}
	m.wm.RUnlock()
	count := 0
	for range waiting {
		count++
		if count == len(m.wrappers) {
			break
		}
	}
}

func NewMario(pkPath string, heartbeat time.Duration, logger logger) *Mario {
	if heartbeat < 30*time.Second {
		heartbeat = 30 * time.Second
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
		actions:            make(chan *tnAction, 16),
		updatedTunnels:     make(chan *ssh.Tunnel, 32),
		publishWrapper:     make(chan *TunnelInfo, 32),
		logger:             logger,
		wrappers:           make(map[*ssh.Tunnel]*TunnelInfo),
		wm:                 sync.RWMutex{},
		stop:               make(chan struct{}),
	}
	return m
}
