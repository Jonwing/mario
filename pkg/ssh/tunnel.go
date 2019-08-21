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
	StatusConnected
	StatusReconnecting
	StatusLost
	StatusClosed
)

var (
	errInvalidLocalAddr = errors.New("invalid local listening address")
	errNegativePort = errors.New("port can not be negative")
	errAnonymous = errors.New("user not specified")
	errMissedPort = errors.New("remote port not specified")
	errConnectionLost = errors.New("connection lost")
)

type TunnelStatus int
type tunnelHandler func(*Tunnel)

// Connector a Connector represents a pair of tunneled connections
type Connector struct {
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

	listener net.Listener

	sshConfig *sh.ClientConfig

	sshClient *sh.Client

	connectors []*Connector

	OnStatus tunnelHandler

	Status TunnelStatus

	// err stores the latest error of this tunnel if there is one
	err error
}

func (t *Tunnel) KeepAlive() {
	if t.Status == StatusClosed {
		return
	}
	if t.sshClient == nil {
		t.err = errConnectionLost
		t.UpdateStatus(StatusLost)
		t.Reconnect()
		return
	}
	_, _, err := t.sshClient.SendRequest("keepalive@openssh.com", true, nil)
	if err != nil {
		t.UpdateStatus(StatusLost)
		t.Reconnect()
	}
}

func (t *Tunnel) Error() error {
	return t.err
}

func (c *Connector) Forward() error {
	go c.localToRemote()
	_, err := io.Copy(c.remoteConn, c.localConn)
	c.remoteConn.Close()
	c.localConn.Close()
	return err
}

func (c *Connector) localToRemote() {
	_, err := io.Copy(c.localConn, c.remoteConn)
	if err != nil {
		c.remoteConn.Close()
		c.localConn.Close()
	}
}

func (c *Connector) Close() {
	c.remoteConn.Close()
	c.localConn.Close()
}

func (t *Tunnel) String() string {
	return t.Local + "->" + t.SSHUri + "->" + t.ForwardTo
}

func (t *Tunnel) Up() error {
	if t.sshClient != nil || t.listener != nil {
		t.err = errors.New("tunnel is already up")
		t.UpdateStatus(StatusConnectFailed)
		return t.err
	}
	listener, err := net.Listen("tcp", t.Local)
	if err != nil {
		t.err = err
		t.UpdateStatus(StatusConnectFailed)
		return err
	}
	t.listener = listener
	defer listener.Close()
	t.UpdateStatus(StatusConnecting)

	t.sshClient, err = sh.Dial("tcp", t.SSHUri, t.sshConfig)
	if err != nil {
		t.err = err
		t.UpdateStatus(StatusConnectFailed)
		return err
	}
	t.UpdateStatus(StatusConnected)
	for {
		conn, err := listener.Accept()
		if err != nil {
			t.err = err
			t.UpdateStatus(StatusConnectFailed)
			return err
		}
		// TODO: don't dial in current goroutine, in case of timeout blocking
		remoteConn, err := t.sshClient.Dial("tcp", t.ForwardTo)
		if err != nil {
			continue
		}
		cnt := NewConnector(conn, remoteConn)
		t.connectors = append(t.connectors, cnt)
		go cnt.Forward()
	}
}

func (t *Tunnel) Down() {
	for _, c := range t.connectors {
		c.Close()
	}
	if t.listener != nil {
		t.listener.Close()
	}
	if t.sshClient != nil {
		t.sshClient.Close()
	}
	t.UpdateStatus(StatusClosed)
}

func (t *Tunnel) Reconnect() {
	t.Down()
	// to avoid write op on the &t, create a new Tunnel to connect
	newT := Tunnel{
		Local:  t.Local,
		SSHUri:     t.SSHUri,
		ForwardTo:  t.ForwardTo,
		sshConfig:  t.sshConfig,
		connectors: make([]*Connector, 0),
		OnStatus:   t.OnStatus,
		Status:     StatusReconnecting,
	}
	*t = newT
	t.Up()
}


func (t *Tunnel) UpdateStatus(st TunnelStatus) {
	t.Status = st
	if t.OnStatus != nil {
		t.OnStatus(t)
	}
}

func (t *Tunnel) User() string {
	return t.sshConfig.User
}


func NewConnector(local, remote net.Conn) *Connector {
	cnt := &Connector{
		localConn:  local,
		remoteConn: remote,
	}
	return cnt
}


// NewTunnel create a new Tunnel forwarding packages from 127.0.0.1:<localPort> to <remote> which is in the
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
		Local:  local,
		SSHUri:     serverParts[1],
		ForwardTo:  remote,
		sshConfig:  sshConfig,
		connectors: make([]*Connector, 0),
		OnStatus: onStatus,
		Status: StatusNew,
	}
	return tn, nil
}
