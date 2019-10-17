package ssh

import (
	"bytes"
	"errors"
	"github.com/google/btree"
	"github.com/sirupsen/logrus"
	sh "golang.org/x/crypto/ssh"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// tunnel is new
	StatusNew = TunnelStatus(1 << iota)
	// connecting to remote
	StatusConnecting
	// connect successfully and listening
	StatusConnected
	// trying to reconnect
	StatusReconnecting
	// indicate that the tunnel is no listening on the given port
	StatusClosed
	// indicate that there is an error
	StatusError = TunnelStatus(1 << 16)
	// the tunnel has been shutdown and removed
	StatusRemoved = TunnelStatus(1 << 17)
)

var (
	errInvalidLocalAddr = errors.New("invalid local listening address")
	errAnonymous        = errors.New("user not specified")
	errMissedPort       = errors.New("remote port not specified")
	errRemoteLost       = errors.New("remote connection lost")
)

type TunnelStatus int
type tunnelHandler func(*Tunnel)

// Connector a Connector represents a pair of tunneled connections
type Connector struct {
	counter    uint64
	openedAt   time.Time
	tunnel     *Tunnel
	localConn  net.Conn
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

func (c *Connector) breakDown() {
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

	// connectors connections this tunnel is serving
	connectors *btree.BTree

	// OnStatus when tunnel's state is changed, this function will be called
	OnStatus tunnelHandler

	status TunnelStatus

	// cCount records connections this tunnel a currently serving
	cCount uint64

	// healthCheckInterval is the interval to check whether ssh connection is alive
	// it's also the timeout of a ssh client
	healthCheckInterval time.Duration

	// err stores the latest error of this tunnel
	err error
}

func (t *Tunnel) Status() TunnelStatus {
	return t.status
}

func (t *Tunnel) Error() error {
	// the status should be more accurate than t.err
	// t.err might be the legacy of last error
	if t.status&StatusError == StatusError {
		return t.err
	}
	return nil
}

func (t *Tunnel) String() string {
	return t.Local + " -> " + t.SSHUri + " -> " + t.ForwardTo
}

func (t *Tunnel) forceConnect() error {
	if t.sshClient != nil {
		t.sshClient.Close()
	}
	t.setStatusError(StatusConnecting, nil)
	var err error

	client, err := sh.Dial("tcp", t.SSHUri, t.sshConfig)
	if err != nil {
		return err
	}
	t.sshClient = client

	if t.listener == nil || t.closed() {
		listener, err := net.Listen("tcp", t.Local)
		if err != nil {
			return err
		}
		t.listener = listener
		go t.listenLocal()
	}

	t.setStatusError(StatusConnected, nil)
	return nil
}

func (t *Tunnel) Up() {
	if t.listener != nil {
		return
	}
	err := t.forceConnect()
	if err != nil {
		t.setStatusError(StatusError, err)
		return
	}
	ticker := time.NewTicker(t.healthCheckInterval)
	for {
		select {
		case work := <-t.works:
			err := work()
			if err != nil {
				t.setStatusError(StatusError, err)
			}
			if t.status&StatusRemoved == StatusRemoved {
				logrus.Debugln("worker get rest......", t.String())
				return
			}
		case <-ticker.C:
			if t.status&StatusRemoved == StatusRemoved {
				logrus.Debugln("exit heath checker", t.String())
				return
			}
			if t.closed() && t.Error() == nil {
				continue
			}
			logrus.Debugln("keep ", t.String(), "alive")
			if t.sshClient == nil {
				t.setStatusError(StatusError, errRemoteLost)
			} else {
				_, _, err := t.sshClient.SendRequest("keepalive@openssh.com", true, nil)
				if err == nil {
					continue
				}
				t.setStatusError(StatusError, err)
			}
			_ = t.forceConnect()
		}
	}
}

func (t *Tunnel) listenLocal() {
	defer t.listener.Close()
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			t.works <- func() error {
				if t.closed() {
					return nil
				}
				t.setStatusError(StatusClosed, err)
				return nil
			}
			return
		}
		t.works <- func() error {
			remoteConn, err := t.sshClient.Dial("tcp", t.ForwardTo)
			if err != nil {
				return nil
			}
			cnt := t.newConnector(conn, remoteConn)
			go cnt.forward()
			return nil
		}
	}
}

func (t *Tunnel) Down(waitDone bool) {
	bufSize := 1
	if waitDone {
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
		t.setStatusError(StatusClosed, nil)
		t.listener.Close()
		ok <- struct{}{}
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
		t.setStatusError(StatusRemoved, nil)
		t.listener.Close()
		ok <- struct{}{}
		return nil
	}
	<-ok
}

func (t *Tunnel) Reconnect(waitDone bool) {
	if !waitDone {
		t.works <- t.forceConnect
		return
	}
	ok := make(chan struct{})
	t.works <- func() error {
		_ = t.forceConnect()
		ok <- struct{}{}
		return nil
	}
	<-ok
}

func (t *Tunnel) UpdateStatus(st TunnelStatus, err error) {
	t.works <- func() error {
		t.setStatusError(st, err)
		return nil
	}
}

func (t *Tunnel) setStatusError(st TunnelStatus, err error) {
	if err != nil {
		st |= StatusError
		t.err = err
	}

	t.status = st
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
		tunnel:     t,
		localConn:  local,
		remoteConn: remote,
		openedAt:   time.Now(),
		counter:    t.cCount,
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

func (t *Tunnel) closed() bool {
	return t.status&StatusClosed == StatusClosed
}

// NewTunnel create a new Tunnel forwarding packages from <local> to <remote> which is in the
// network of ssh server <server>. 'server' is in form of 'user@host:port', if port is absent,
// the default ssh port 22 is used. 'remote' is in form of 'host:port',
// 'pk' should contain the private key of this tunnel.
func NewTunnel(local string, server string, remote string, pk io.Reader, onStatus tunnelHandler, sshTimeout time.Duration) (tn *Tunnel, err error) {
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
		Timeout: sshTimeout,
	}

	tn = &Tunnel{
		Local:               local,
		SSHUri:              serverParts[1],
		ForwardTo:           remote,
		sshConfig:           sshConfig,
		connectors:          btree.New(2),
		OnStatus:            onStatus,
		status:              StatusNew,
		works:               make(chan func() error, 32),
		healthCheckInterval: sshTimeout,
	}
	return tn, nil
}
