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

	history *history

	tnView *TableView

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

func (d *Dashboard) Quit() {
	d.mario.Stop()
	logrus.Infoln("Bye.")
	time.Sleep(2*time.Second)
	d.Layout.Stop()
}

func DefaultDashboard(pk string, log logger) *Dashboard {
	d := &Dashboard{
		Layout:    tview.NewApplication(),
		tunnels: make([]*TunnelInfo, 0),
		tunnelRecv: make(chan *TunnelInfo, 16),
		history:   NewHistory(),
		logView:   tview.NewTextView(),
		inputView: tview.NewInputField().SetLabel("> "),
		input:     make(chan string),
		mario:	   NewMario(pk, 15*time.Second, log),
	}

	tb, _ := SimpleTableView([]string{"id", "name", "status", "link"}, []int{1, 2, 2, 5})
	d.tnView = tb

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
		switch key {
		case tcell.KeyEnter:
			text := d.inputView.GetText()
			d.inputView.SetText("")
			d.input <- text
		case tcell.KeyUp:
			last := d.history.Prev()
			if last == "" {
				return
			}
			d.inputView.SetText(last)
		case tcell.KeyDown:
			next := d.history.Next()
			if next == "" {
				return
			}
			d.inputView.SetText(next)
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
			d.tnView.UpdateRow(idx, 2, tn.GetStatus())
		} else {
			d.tunnels = append(d.tunnels, tn)
			if len(d.tunnels) <= 1 || tn.GetID() <= d.tunnels[len(d.tunnels)-1].GetID() {
				tnSorter(byID).sort(d.tunnels)
				err := d.tnView.InsertRow(idx, []string{ strconv.Itoa(tn.GetID()), tn.GetName(), tn.GetStatus(), tn.Represent()})
				// d.tnView.Clear()
				// for _, tn := range d.tunnels {
				// 	err := d.tnView.AddRows([]string{ strconv.Itoa(tn.GetID()), tn.GetName(), tn.GetStatus(), tn.Represent()})
				if err != nil {
					logrus.Errorf("can not display tunnel %s because of %s", tn.Represent(), err.Error())
				}
				// }
			}
		}
		d.Layout.Draw()
	}
}

func (d *Dashboard) Update(tn *TunnelInfo) {
	d.tunnelRecv <- tn
}


func (d *Dashboard) GetLogView() io.Writer {
	return d.logView
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

func (d *Dashboard) Page(dir int)  {
	if dir < 0 {
		d.tnView.PrevPage()
	} else {
		d.tnView.NextPage()
	}
	d.Layout.Draw()
}

// debug purpose
func (d *Dashboard) GetInput() <-chan string {
	return d.input
}

func (d *Dashboard) MakeHistory(input string) {
	d.history.Append(input)
}
