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

// Establish setups a new channel, if `noConnect` is true, only initiate a new tunnel.
// args
// 	name: 		name of a tunnel
// 	local:		local listening address
// 	server: 	ssh server address
// 	remote: 	address of remote peer of the tunnel
// 	pk: 		private key path
// 	noConnect: 	don't connect now
func (m *Mario) Establish(name string, local, server, remote string, pk string, noConnect bool) (*TunnelInfo, error) {
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
	if !noConnect {
		go tn.Up()
	}
	return tw, nil
}

func (m *Mario) Up(tn *TunnelInfo, waitDone chan error) {
	if tn == nil {
		waitDone <- errors.New("nil tn")
		return
	}
	if tn.t.Status()&ssh.StatusConnected == ssh.StatusConnected {
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
	var method func(*ssh.Tunnel, chan error)
	if action == actReconnect {
		method = func(t *ssh.Tunnel, w chan error) {
			t.Reconnect(w)
		}
	} else {
		method = func(t *ssh.Tunnel, w chan error) {
			t.Down(w)
		}
	}

	for tn := range m.wrappers {
		method(tn, waiting)
	}

	m.wm.RUnlock()
	if !waitDone {
		return
	}
	m.waitTimeout(2*time.Second, waiting, len(m.wrappers))
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
				m.wm.Lock()
				wrapped, ok := m.wrappers[raw]
				if !ok {
					wrapped = m.wrap(raw)
					wrapped.name = "unknown"
					m.wrappers[wrapped.t] = wrapped
				}
				m.wm.Unlock()
				m.publishWrapper <- wrapped
			case <-m.stop:
				break
			}
		}
	}()
	return m.publishWrapper, nil
}

// waitTimeout waits on a channel up to `count` errors until timeout
func (m *Mario) waitTimeout(timeout time.Duration, waiting <-chan error, count int) (es []error) {
	if count <= 0 {
		return
	}
	tm := time.NewTimer(timeout)
	var sum int
	for {
		select {
		case err := <-waiting:
			sum++
			es = append(es, err)
			if sum >= count {
				return
			}
		case <-tm.C:
			return
		}
	}
}

func (m *Mario) Stop() {
	logrus.Debugln("Mario stop")
	m.stop <- struct{}{}
	m.wm.RLock()
	defer m.wm.RUnlock()
	waiting := make(chan error, len(m.wrappers))
	for tn := range m.wrappers {
		if tn.Status() == ssh.StatusNew {
			waiting <- nil
		}
		tn.Down(waiting)
	}
	m.waitTimeout(2*time.Second, waiting, len(m.wrappers))
}

func NewMario(pkPath string, heartbeat time.Duration, logger logger) *Mario {
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
		actions:            make(chan *tnAction, 1),
		updatedTunnels:     make(chan *ssh.Tunnel, 1),
		publishWrapper:     make(chan *TunnelInfo, 1),
		logger:             logger,
		wrappers:           make(map[*ssh.Tunnel]*TunnelInfo),
		wm:                 sync.RWMutex{},
		stop:               make(chan struct{}),
	}
	return m
}
