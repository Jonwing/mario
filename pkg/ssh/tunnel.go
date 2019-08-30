package ssh

import (
	"bytes"
	"errors"
	sh "golang.org/x/crypto/ssh"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	StatusNew = TunnelStatus(iota)
	StatusConnecting
	StatusConnectFailed
	StatusListeningErr
	StatusConnected
	StatusReconnecting
	StatusLost
	StatusClosed
	StatusWorkError
)

var (
	errInvalidLocalAddr = errors.New("invalid local listening address")
	errNegativePort     = errors.New("port can not be negative")
	errAnonymous        = errors.New("user not specified")
	errMissedPort       = errors.New("remote port not specified")
	errConnectionLost   = errors.New("connection lost")
	errShutdown         = errors.New("shutdown")
	errRemove			= errors.New("removed")
)

type TunnelStatus int
type tunnelHandler func(*Tunnel)

// connector a connector represents a pair of tunneled connections
type connector struct {
	tunnel *Tunnel
	localConn net.Conn
	remoteConn net.Conn
}


type Tunnel struct {
	// Local the listen address for local tcp server
	Local string

	// SSHUri The ssh server's uri in form of "user@hostname:port", if port is absent,
	// the default ssh port 22 will be used
	SSHUri string

	// ForwardTo The remote server's uri you want your LocalPort to map to, is in form of
	// "hostname:port"
	ForwardTo string

	works chan func() error

	listener net.Listener

	sshConfig *sh.ClientConfig

	sshClient *sh.Client

	connectors []*connector

	OnStatus tunnelHandler

	Status TunnelStatus

	// err stores the latest error of this tunnel if there is one
	err error
}

func (t *Tunnel) KeepAlive() {
	t.works <- func() error {
		if t.Status == StatusClosed {
			return nil
		}
		if t.sshClient == nil {
			t.setStatusError(StatusLost, nil)
			return t.forceConnect()
		}
		_, _, err := t.sshClient.SendRequest("keepalive@openssh.com", true, nil)
		if err != nil {
			t.setStatusError(StatusLost, err)
			return t.forceConnect()
		}
		return nil
	}
}

func (t *Tunnel) Error() error {
	return t.err
}

func (c *connector) Forward() error {
	go c.localToRemote()
	_, err := io.Copy(c.remoteConn, c.localConn)
	c.remoteConn.Close()
	c.localConn.Close()
	return err
}

func (c *connector) localToRemote() {
	_, err := io.Copy(c.localConn, c.remoteConn)
	if err != nil {
		c.remoteConn.Close()
		c.localConn.Close()
	}
}

func (c *connector) Close() {
	c.remoteConn.Close()
	c.localConn.Close()
}

func (t *Tunnel) String() string {
	return t.Local + "->" + t.SSHUri + "->" + t.ForwardTo
}

func (t *Tunnel) forceConnect() error {
	if t.listener != nil {
		t.listener.Close()
		// t.listener = nil
	}

	if t.sshClient != nil {
		t.sshClient.Close()
		// t.sshClient = nil
	}
	t.setStatusError(StatusConnecting, nil)
	var err error
	t.sshClient, err = sh.Dial("tcp", t.SSHUri, t.sshConfig)
	if err != nil {
		return err
	}

	t.listener, err = net.Listen("tcp", t.Local)
	if err != nil {
		return err
	}
	t.setStatusError(StatusConnected, nil)
	go t.listenLocal()
	return nil
}

func (t *Tunnel) Up() {
	err := t.forceConnect()
	if err != nil {
		t.setStatusError(StatusConnectFailed, err)
		return
	}

	for work := range t.works {
		err := work()
		if err != nil {
			if err.Error() == errRemove.Error() {
				t.UpdateStatus(StatusClosed, nil)
				return
			}
			t.setStatusError(StatusWorkError, err)
		}
	}
}

func (t *Tunnel) listenLocal() {
	defer t.sshClient.Close()
	defer t.listener.Close()
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			t.UpdateStatus(StatusListeningErr, err)
			return
		}
		// TODO: don't dial in current goroutine, in case of timeout blocking
		t.works <- func() error {
			remoteConn, err := t.sshClient.Dial("tcp", t.ForwardTo)
			if err != nil {
				return nil
			}
			cnt := newConnector(conn, remoteConn)
			t.connectors = append(t.connectors, cnt)
			go cnt.Forward()
			return nil
		}
	}
}

func (t *Tunnel) Down() {
	t.works <- func() error {
		for _, cnt := range t.connectors {
			cnt.Close()
		}
		t.listener.Close()
		t.UpdateStatus(StatusClosed, nil)
		return errShutdown
	}
}

// the difference between Down() and Destroy() is that Destroy() exits the running
// goroutine so that all subsequent works will failed, which making this tunnel unavailable
func (t *Tunnel) Destroy() {
	t.works <- func() error {
		for _, cnt := range t.connectors {
			cnt.Close()
		}
		t.listener.Close()
		return errRemove
	}
}

func (t *Tunnel) Reconnect() {
	t.works <- t.forceConnect
}


func (t *Tunnel) UpdateStatus(st TunnelStatus, err error) {
	t.works <- func() error {
		t.setStatusError(st, err)
		return nil
	}
}

func (t *Tunnel) setStatusError(st TunnelStatus, err error) {
	if err != nil {
		t.err = err
	}
	t.Status = st
	if t.OnStatus != nil {
		t.OnStatus(t)
	}
}

func (t *Tunnel) User() string {
	return t.sshConfig.User
}


func newConnector(local, remote net.Conn) *connector {
	cnt := &connector{
		localConn:  local,
		remoteConn: remote,
	}
	return cnt
}


// NewTunnel create a new Tunnel forwarding packages from <local> to <remote> which is in the
// network of ssh server <server>. 'server' is in form of 'user@host:port', if port is absent,
// the default ssh port 22 is used. 'remote' is in form of 'host:port', 'pk' should contain the private key
// of this tunnel.
func NewTunnel(local string, server string, remote string, pk io.Reader, onStatus tunnelHandler) (tn *Tunnel, err error) {
	locals := strings.Split(local, ":")
	if len(locals) < 2 {
		return nil, errInvalidLocalAddr
	}

	if _, err := strconv.Atoi(locals[1]); err != nil {
		return nil, err
	}

	serverParts := strings.Split(server, "@")
	if len(serverParts) < 2 {
		return nil, errAnonymous
	}

	remoteParts := strings.Split(remote, ":")
	if len(remoteParts) < 2 {
		return nil, errMissedPort
	}

	key := new(bytes.Buffer)
	_, err = key.ReadFrom(pk)
	if err != nil {
		return nil, err
	}

	signer, err := sh.ParsePrivateKey(key.Bytes())
	if err != nil {
		return nil, err
	}

	sshConfig := &sh.ClientConfig{
		User: serverParts[0],
		Auth: []sh.AuthMethod{sh.PublicKeys(signer)},
		HostKeyCallback: func(hostname string, remote net.Addr, key sh.PublicKey) error {
			// Always accept key.
			return nil
		},
		Timeout: 15*time.Second,
	}

	tn = &Tunnel{
		Local:      local,
		SSHUri:     serverParts[1],
		ForwardTo:  remote,
		sshConfig:  sshConfig,
		connectors: make([]*connector, 0),
		OnStatus:   onStatus,
		Status:     StatusNew,
		works:      make(chan func() error, 32),
	}
	return tn, nil
}
