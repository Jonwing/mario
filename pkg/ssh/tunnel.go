package ssh

import (
	"bytes"
	"errors"
	"github.com/google/btree"
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

// Connector a Connector represents a pair of tunneled connections
type Connector struct {
	counter uint64
	openedAt time.Time
	tunnel *Tunnel
	localConn net.Conn
	remoteConn net.Conn
}

func (c *Connector) String() string {
	return c.localConn.RemoteAddr().String() + "->" + c.tunnel.String()
}

func (c *Connector) ID() uint64 {
	return c.counter
}

// this implements btree.Item interface so that we can put it into a btree
func (c *Connector) Less(item btree.Item) bool {
	other, ok := item.(*Connector)
	if !ok {
		return false
	}
	return c.counter < other.counter
}

func (c *Connector) OpenedAt() time.Time {
	return c.openedAt
}

// forwards packages between local connection and remote connection
func (c *Connector) forward() error {
	go c.localToRemote()
	_, err := io.Copy(c.localConn, c.remoteConn)
	c.Close()
	return err
}

func (c *Connector) localToRemote() {
	_, err := io.Copy(c.remoteConn, c.localConn)
	if err != nil {
		c.Close()
	}
}

func (c *Connector) Close() {
	c.breakDown()
	c.tunnel.closeConnector(c)
}

func (c *Connector)	breakDown() {
	c.remoteConn.Close()
	c.localConn.Close()
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

	connectors *btree.BTree

	OnStatus tunnelHandler

	Status TunnelStatus

	cCount uint64

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

func (t *Tunnel) String() string {
	return t.Local + " -> " + t.SSHUri + " -> " + t.ForwardTo
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
				t.setStatusError(StatusClosed, nil)
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
		t.works <- func() error {
			remoteConn, err := t.sshClient.Dial("tcp", t.ForwardTo)
			if err != nil {
				return nil
			}
			cnt := t.newConnector(conn, remoteConn)
			// t.connectors = append(t.connectors, cnt)
			go cnt.forward()
			return nil
		}
	}
}

func (t *Tunnel) Down(waitClose bool) {
	bufSize := 1
	if waitClose {
		bufSize = 0
	}
	ok := make(chan struct{}, bufSize)
	t.works <- func() error {
		t.connectors.Ascend(func(i btree.Item) bool {
			cnt := i.(*Connector)
			cnt.breakDown()
			return true
		})
		t.connectors.Clear(false)
		t.listener.Close()
		ok <- struct{}{}
		t.setStatusError(StatusClosed, nil)
		return nil
	}
	<-ok
}

// the difference between Down() and Destroy() is that Destroy() exits the running
// goroutine so that all subsequent works will failed, which making this tunnel unavailable
func (t *Tunnel) Destroy(waitClose bool) {
	bufSize := 1
	if waitClose {
		bufSize = 0
	}
	ok := make(chan struct{}, bufSize)
	t.works <- func() error {
		t.connectors.Ascend(func(i btree.Item) bool {
			cnt := i.(*Connector)
			cnt.breakDown()
			return true
		})
		t.connectors.Clear(false)
		t.listener.Close()
		ok <- struct{}{}
		return errRemove
	}
	<-ok
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
	// if the last status is closed, suppress the broadcasting of listening error
	if st == StatusListeningErr && t.Status == StatusClosed {
		return
	}
	t.err = err

	t.Status = st
	if t.OnStatus != nil {
		t.OnStatus(t)
	}
}

func (t *Tunnel) User() string {
	return t.sshConfig.User
}


func (t *Tunnel) newConnector(local, remote net.Conn) *Connector {
	t.cCount++
	cnt := &Connector{
		tunnel: t,
		localConn:  local,
		remoteConn: remote,
		openedAt: time.Now(),
		counter: t.cCount,
	}
	t.connectors.ReplaceOrInsert(cnt)
	return cnt
}

func (t *Tunnel) closeConnector(c *Connector) {
	t.works <- func() error {
		t.connectors.Delete(c)
		return nil
	}
}

func (t *Tunnel) GetConnectors() []*Connector {
	connChan := make(chan []*Connector)
	t.works <- func() error {
		cs := make([]*Connector, 0, t.connectors.Len())
		t.connectors.Ascend(func(i btree.Item) bool {
			cs = append(cs, i.(*Connector))
			return true
		})
		connChan <- cs
		return nil
	}
	return <-connChan
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
		connectors: btree.New(2),
		OnStatus:   onStatus,
		Status:     StatusNew,
		works:      make(chan func() error, 32),
	}
	return tn, nil
}
