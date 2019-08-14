package internal

import (
	"errors"
	"fmt"
	"github.com/Jonwing/mario/pkg/ssh"
	"github.com/gdamore/tcell"
	"github.com/rivo/tview"
	"github.com/sirupsen/logrus"
	"io"
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
	Layout *tview.Application

	tnView *tview.List

	tunnelRecv chan *TunnelInfo

	logView *tview.TextView

	inputView *tview.InputField

	// tunnels holds information of all tunnels in an id-ascending order
	tunnels []*TunnelInfo

	mario *Mario

	input chan string
}

func (d *Dashboard) Show() error {
	if d.mario == nil {
		return errors.New("no mario, probably run in a wrong way")
	}
	tn, err := d.mario.Monitor()
	if err != nil {
		return err
	}
	go func() {
		for t := range tn {

			if err := t.Error(); err != nil && t.GetStatus() != status[ssh.StatusClosed] {
				errStr := fmt.Sprintf("[Error] Tunnel <%d> (%s) raised an error: %s\n", t.GetID(), t.GetName(), t.Error())
				d.logView.Write([]byte(errStr))
			}
			d.tunnelRecv <- t
		}
	}()
	go d.updateTunnelInfo()
	return d.Layout.Run()
}

func DefaultDashboard(pk string, log logger) *Dashboard {
	d := &Dashboard{
		Layout:    tview.NewApplication(),
		tunnels: make([]*TunnelInfo, 0),
		tunnelRecv: make(chan *TunnelInfo, 16),
		tnView:    tview.NewList(),
		logView:   tview.NewTextView(),
		inputView: tview.NewInputField().SetLabel("> "),
		input:     make(chan string),
		mario:	   NewMario(pk, 15*time.Second, log),
	}

	// total flex
	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	subFlex := tview.NewFlex()
	d.tnView.SetBorder(true).SetTitle(" Tunnels ")

	d.logView.SetBorder(true).SetTitle(" Logs ")
	d.logView.SetChangedFunc(func() {
		d.Layout.Draw()
	})
	subFlex = subFlex.AddItem(
		d.tnView, 0, 1, false).AddItem(
		d.logView, 0, 1, false)

	// inputView is responsible for user input
	d.inputView.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			text := d.inputView.GetText()
			d.inputView.SetText("")
			d.input <- text
		}
	})
	flex = flex.AddItem(
		subFlex, 0, 9, false).AddItem(
		d.inputView, 0, 1, true)

	d.Layout = tview.NewApplication().SetRoot(flex, true).SetFocus(flex)
	return d
}


func (d *Dashboard) updateTunnelInfo() {
	for tn := range d.tunnelRecv {
		idx := sort.Search(len(d.tunnels), func(i int) bool {
			return d.tunnels[i].GetID() >= tn.GetID()
		})
		if idx < len(d.tunnels) && d.tunnels[idx].GetID() == tn.GetID() {
			logrus.Debugf("tn %d %s", tn.GetID(), tn.GetStatus())
			d.tnView.SetItemText(idx, tn.GetStatus(), d.formatTunnel(tn))
		} else {
			// TODO: sorting every time a TunnelInfo is added is expensive
			d.tunnels = append(d.tunnels, tn)
			tnSorter(byID).sort(d.tunnels)
			d.tnView.Clear()
			for _, tn := range d.tunnels {
				d.tnView.AddItem(tn.GetStatus(), d.formatTunnel(tn), ' ', nil)
			}
		}
		// TODO: How to use d.tnView.Draw ?
		d.Layout.Draw()
	}
}

func (d *Dashboard) Update(tn *TunnelInfo) {
	d.tunnelRecv <- tn
}


func (d *Dashboard) GetLogView() io.Writer {
	return d.logView
}

func (d *Dashboard) NewTunnel(name string, localPort int, server, remote string, pk string) error {
	tn, err := d.mario.Establish(name, localPort, server, remote, pk)
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


func (d *Dashboard) formatTunnel(tn *TunnelInfo) string {
	return strconv.Itoa(tn.GetID()) + "    " + tn.GetName() + "    " + tn.Represent()
}

func (d *Dashboard) SetInputAutoComplete(handler func (string) []string) {
	d.inputView.SetAutocompleteFunc(handler)
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