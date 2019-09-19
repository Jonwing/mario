package internal

import (
	"errors"
	"fmt"
	"github.com/Jonwing/mario/pkg/ssh"
	"github.com/sirupsen/logrus"
	"sort"
	"strconv"
	"time"
)

type sortTnBy struct {
	tns []*TunnelInfo
	by func(i, j *TunnelInfo) bool
}

func (t *sortTnBy) Len() int {
	return len(t.tns)
}

func (t *sortTnBy) Swap(i, j int) {
	t.tns[i], t.tns[j] = t.tns[j], t.tns[i]
}

func (t *sortTnBy) Less(i, j int) bool {
	return t.by(t.tns[i], t.tns[j])
}

type tnSorter func(i, j *TunnelInfo) bool

func (s tnSorter) sort(tn []*TunnelInfo) {
	st := &sortTnBy{
		tns: tn,
		by:  s,
	}
	sort.Sort(st)
}

func byID(i, j *TunnelInfo) bool {
	return i.GetID() < j.GetID()
}

func byName(i, j *TunnelInfo) bool {
	return i.GetName() < j.GetName()
}

type Dashboard struct {
	tunnelRecv chan *TunnelInfo

	// tunnels holds information of all tunnels in an id-ascending order
	tunnels []*TunnelInfo

	mario *Mario

	input chan string
}

func (d *Dashboard) Work() error {
	if d.mario == nil {
		return errors.New("no mario, probably run in a wrong way")
	}
	tn, err := d.mario.Monitor()
	if err != nil {
		return err
	}
	go func() {
		for t := range tn {
			d.tunnelRecv <- t
		}
	}()
	go d.updateTunnelInfo()
	return nil
}

func (d *Dashboard) Quit() {
	d.mario.Stop()
	fmt.Println("Bye.")
}

func DefaultDashboard(pk string, log logger) *Dashboard {
	d := &Dashboard{
		tunnels: make([]*TunnelInfo, 0),
		tunnelRecv: make(chan *TunnelInfo, 16),
		input:     make(chan string),
		mario:	   NewMario(pk, 15*time.Second, log),
	}

	return d
}


func (d *Dashboard) updateTunnelInfo() {
	for tn := range d.tunnelRecv {
		idx := sort.Search(len(d.tunnels), func(i int) bool {
			return d.tunnels[i].GetID() >= tn.GetID()
		})
		if idx >= len(d.tunnels) || d.tunnels[idx].GetID() != tn.GetID() {
			d.tunnels = append(d.tunnels, tn)
			if len(d.tunnels) <= 1 || tn.GetID() <= d.tunnels[len(d.tunnels)-1].GetID() {
				tnSorter(byID).sort(d.tunnels)
			}
		}
		logrus.Debugf("tunnel <%d><%s> status changed to: %s", tn.GetID(), tn.GetName(), tn.GetStatus())
	}
}

func (d *Dashboard) Update(tn *TunnelInfo) {
	d.tunnelRecv <- tn
}


func (d *Dashboard) NewTunnel(name string, local, server, remote string, pk string) error {
	tn, err := d.mario.Establish(name, local, server, remote, pk)
	if err != nil {
		return err
	}
	d.tunnelRecv <- tn
	return nil
}

func (d *Dashboard) getTunnel(idOrName interface{}) (tn *TunnelInfo) {
	switch idOrName.(type) {
	case int:
		v := idOrName.(int)
		idx := sort.Search(len(d.tunnels), func(i int) bool {
			return d.tunnels[i].GetID() >= v
		})
		if idx < len(d.tunnels) && d.tunnels[idx].GetID() == v {
			return d.tunnels[idx]
		}
	case string:
		name := idOrName.(string)
		for _, tn := range d.tunnels {
			if tn.GetName() == name {
				return tn
			}
		}
	}
	return
}

func (d *Dashboard) CloseTunnel(idOrName interface{}) (err error) {
	tn := d.getTunnel(idOrName)
	if tn == nil {
		return errors.New(fmt.Sprintf("tunnel with id or name %s not found", idOrName))
	}
	return tn.Close()
}

func (d *Dashboard) UpTunnel(idOrName interface{}) (err error) {
	tn := d.getTunnel(idOrName)
	if tn == nil {
		return errors.New(fmt.Sprintf("tunnel with id or name %s not found", idOrName))
	}
	return tn.Up()
}

func (d *Dashboard) GetTunnelConnections(idOrName interface{}) []*ssh.Connector {
	tn := d.getTunnel(idOrName)
	if tn == nil {
		return nil
	}
	return tn.Connections()
}


func (d *Dashboard) formatTunnel(tn *TunnelInfo) string {
	return strconv.Itoa(tn.GetID()) + "    " + tn.GetName() + "    " + tn.Represent()
}


func (d *Dashboard) GetTunnels() []*TunnelInfo {
	if len(d.tunnels) == 0 {
		return nil
	}
	tns := make([]*TunnelInfo, len(d.tunnels))
	for i, tn := range d.tunnels {
		tns[i] = tn
	}
	return tns
}


// debug purpose
func (d *Dashboard) GetInput() <-chan string {
	return d.input
}

