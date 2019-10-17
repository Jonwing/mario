package cmd

import (
	"fmt"
	"github.com/Jonwing/mario/pkg/ssh"
	"github.com/c-bata/go-prompt"
	json "github.com/json-iterator/go"
	"github.com/olekukonko/tablewriter"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
)

type completeFunc func(cmd promptCommand, args []string, current string) []prompt.Suggest

type promptCommand interface {
	// returns the name of this command
	Name() string

	// GetCmd returns the actual cobra.Command
	GetCmd() *cobra.Command

	// Complete is responsible for returning autocomplete suggestions for given
	// input, args is a slice of words which is input separated by space, `current`
	// is the word to be completed.
	Complete(args []string, current string) []prompt.Suggest

	// Children returns children commands of this command, if any
	Children() []promptCommand

	// add child command to this command
	AddChildren(cmd ...promptCommand)

	// clear command flag values. The command might be used multiple times, so that if there
	// are flags, they should be reset to default state to avoid accessing flags of last run unexpectedly
	ClearFlags()
}

func (i *interactiveCmd) Name() string {
	return "tunnel"
}

func (i *interactiveCmd) GetCmd() *cobra.Command {
	return i.command
}

func (i *interactiveCmd) Complete(args []string, current string) []prompt.Suggest {
	children := make([]prompt.Suggest, len(i.children))
	for idx, c := range i.children {
		suggest := prompt.Suggest{
			Text:        c.Name(),
			Description: c.GetCmd().Short,
		}
		children[idx] = suggest
	}
	return prompt.FilterHasPrefix(children, current, true)
}

func (i *interactiveCmd) Children() []promptCommand {
	return i.children
}

func (i *interactiveCmd) AddChildren(cmd ...promptCommand) {
	for _, c := range cmd {
		i.children = append(i.children, c)
		i.command.AddCommand(c.GetCmd())
	}
}

func (i *interactiveCmd) ClearFlags() {
	h := i.command.Flags().Lookup("help")
	if h == nil {
		return
	}
	err := h.Value.Set("false")
	if err != nil {
		fmt.Printf("error clearing help flag: %s\n", err.Error())
	}
}

func (i *interactiveCmd) runCommand(txt string) {
	txt = strings.TrimSpace(txt)
	if txt == "" {
		return
	}
	txt = spacePtn.ReplaceAllString(txt, " ")
	args := strings.Split(txt, " ")
	err := i.RunCommand(args)
	if err != nil {
		logrus.Errorln("command error: ", err.Error())
	}
	for _, cmd := range i.children {
		cmd.ClearFlags()
	}
}

func (i *interactiveCmd) complete(d prompt.Document) (suggest []prompt.Suggest) {
	txt := d.TextBeforeCursor()
	w := d.GetWordBeforeCursor()
	if txt == "" {
		return
	}

	args := strings.Split(spacePtn.ReplaceAllString(txt, " "), " ")

	// If PIPE is in text before the cursor, returns empty suggestions.
	for i := range args {
		if args[i] == "|" {
			return
		}
	}

	current := promptCommand(i)
	for i := range args {
		c := getChildCommand(current, args[i])
		if c != nil {
			current = c
		}
	}
	return current.Complete(args, w)
}

// command common hierarchical command with suggestion prompting,
// combines cobra.Command and go-prompt
type command struct {
	root *interactiveCmd

	name string

	cmd *cobra.Command

	completer completeFunc

	children []promptCommand
}

// setRoot all commands are child commands of the root
func (c *command) setRoot(root *interactiveCmd) {
	c.root = root
}

func (c *command) Name() string {
	return c.name
}

func (c *command) GetCmd() *cobra.Command {
	return c.cmd
}

func (c *command) Complete(args []string, current string) []prompt.Suggest {
	if c.completer == nil {
		return nil
	}
	return c.completer(c, args, current)
}

func (c *command) Children() []promptCommand {
	return c.children
}

func (c *command) AddChildren(cmd ...promptCommand) {
	c.children = append(c.children, cmd...)
}

func (c *command) ClearFlags() {
	h := c.cmd.Flags().Lookup("help")
	if h == nil {
		return
	}
	err := h.Value.Set("false")
	if err != nil {
		fmt.Printf("error clearing help flag: %s\n", err.Error())
	}
}

// listCommand list the current state of all tunnels to output
type listCommand struct {
	command

	table *tablewriter.Table
}

func (l *listCommand) Run(cmd *cobra.Command, args []string) {
	l.table.ClearRows()
	tns := l.root.dashboard.GetTunnels()
	rows := make([][]string, len(tns))
	for i, tn := range tns {
		var errStr string
		if tn.Error() != nil {
			errStr = tn.Error().Error()
		}
		rows[i] = []string{strconv.Itoa(tn.GetID()), tn.GetName(), tn.GetStatus(), tn.Represent(), errStr}
	}
	l.table.AppendBulk(rows)
	l.table.Render()
}

func NewListCommand(root *interactiveCmd) *listCommand {
	l := &listCommand{
		command: command{
			root: root,
			name: "list",
			cmd: &cobra.Command{
				Use:   "list",
				Short: "list all tunnels",
			},
			completer: nil,
			children:  make([]promptCommand, 0),
		},
		table: tablewriter.NewWriter(os.Stdout),
	}
	l.table.SetHeader([]string{"id", "name", "status", "link", "remark"})
	l.table.SetRowLine(false)
	return l
}

// openCommand is responsible for establishing a new SSH tunnel
// usage:
// 		open --link "your ssh tunnel address" --name t1 --key ~/.ssh/other_rsa
// 		open --local :1080 --server user@server.com --remote 127.0.0.1:1080
type openCommand struct {
	command

	// link(--link\-l) the link that represents a ssh tunnel
	link string

	// local listening address
	local string

	// server ssh server address
	server string

	// remote endpoint of the ssh tunnel
	remote string

	// name of this tunnel
	tunnelName string

	// pk private key path
	pk string
}

func (o *openCommand) ClearFlags() {
	o.command.ClearFlags()
	o.link = ""
	o.local = ""
	o.server = ""
	o.remote = ""
	o.tunnelName = ""
	o.pk = ""
}

func (o *openCommand) Complete(args []string, word string) []prompt.Suggest {
	if !strings.HasPrefix(word, "--") {
		return nil
	}
	// open command has no args, just flags
	suggests := make([]prompt.Suggest, 0)
	fs := o.cmd.Flags()
	fs.VisitAll(flagHasPrefix(word, &suggests))
	return suggests
}

func (o *openCommand) Run(cmd *cobra.Command, args []string) {
	if o.link != "" {
		// this should split the link into [mapping, server] slice
		parts := strings.SplitN(o.link, "@", 2)
		if len(parts) != 2 {
			logrus.Errorln("wrong link: ", o.link)
			return
		}
		// this should split mapping into [local host, local port, remote] slice
		mapping := strings.SplitN(parts[0], ":", 3)
		if len(mapping) != 3 {
			logrus.Errorln("wrong link: ", o.link)
			return
		}

		_, err := strconv.Atoi(mapping[1])
		if err != nil {
			logrus.Errorln("port must be a number: ", mapping[1])
			return
		}
		o.local = strings.Join(mapping[:2], ":")
		o.remote = mapping[2]

		o.server = parts[1]
	} else {
		if o.server == "" || o.remote == "" {
			logrus.Errorln("[Error]Should specify server by -s and remote by -r")
			return
		}
	}

	err := o.root.dashboard.NewTunnel(o.tunnelName, o.local, o.server, o.remote, o.pk, false)
	if err != nil {
		logrus.WithError(err).Errorf(
			"Open tunnel failed. local: %d, server: %s, remote: %s", o.local, o.server, o.remote)
	}
}

// closeOrUpCommand is responsible for close or reopen a ssh tunnel
// usage:
// 		close
// 		close <tunnel_id>
// 		close --name tunnel_name
// 		up
// 		up <tunnel_id>
// 		up --name tunnel_name
type closeOrUpCommand struct {
	command

	tunnelName string

	listCmd *listCommand
}

func (c *closeOrUpCommand) ClearFlags() {
	c.command.ClearFlags()
	c.tunnelName = ""
}

func (c *closeOrUpCommand) Complete(args []string, word string) []prompt.Suggest {
	// if  starts with -- ,  returns the flags
	suggests := make([]prompt.Suggest, 0)
	if strings.HasPrefix(word, "--") {
		c.cmd.Flags().VisitAll(flagHasPrefix(word, &suggests))
		return suggests
	}
	// if the last part of the command is "--name" or "-n", return the tunnel list
	if len(args) > 2 && (args[len(args)-2] == "--name" || args[len(args)-2] == "-n") {
		for _, tn := range c.root.dashboard.GetTunnels() {
			suggests = append(suggests, prompt.Suggest{
				Text:        tn.GetName(),
				Description: "ID: " + strconv.Itoa(tn.GetID()) + "(" + tn.GetStatus() + ")",
			})
		}
		return prompt.FilterHasPrefix(suggests, word, true)
	}

	for _, tn := range c.root.dashboard.GetTunnels() {
		suggests = append(suggests, prompt.Suggest{
			Text:        strconv.Itoa(tn.GetID()),
			Description: tn.GetName() + "(" + tn.GetStatus() + ")",
		})
	}
	return prompt.FilterHasPrefix(suggests, word, true)
}

func (c *closeOrUpCommand) Run(cmd *cobra.Command, args []string) {
	var method func(interface{}, bool) error
	var err error
	if c.name == "close" {
		method = c.root.dashboard.CloseTunnel
	} else {
		method = c.root.dashboard.UpTunnel
	}
	if len(args) == 0 && c.tunnelName == "" {
		_ = method(-1, true)
		c.listCmd.Run(nil, nil)
		return
	}
	if len(args) > 0 {
		for _, str := range args {
			id, err := strconv.Atoi(str)
			if err != nil {
				logrus.Errorln("id should be a number", args[0])
				return
			}
			err = method(id, true)
		}
		// close tunnel with id
	} else {
		err = method(c.tunnelName, true)
	}

	if err != nil {
		logrus.Errorln(c.name, "failed: ", err.Error())
	}
	c.listCmd.Run(nil, nil)
}

// saveCommand saves all the ssh tunnels that mario holds to disk for next time usage
type saveCommand struct {
	command

	// output path of the export file
	output string
}

func (s *saveCommand) ClearFlags() {
	s.output = ""
	s.command.ClearFlags()
}

func (s *saveCommand) Complete(args []string, word string) []prompt.Suggest {
	if !strings.HasPrefix(word, "--") {
		return nil
	}
	// open command has no args, just flags
	suggests := make([]prompt.Suggest, 0)
	fs := s.cmd.Flags()
	fs.VisitAll(flagHasPrefix(word, &suggests))
	return suggests
}

func (s *saveCommand) Run(cmd *cobra.Command, args []string) {
	tns := s.root.dashboard.GetTunnels()
	configs := make([]*tConfig, 0)
	for _, tn := range tns {
		cfg := new(tConfig)
		cfg.Name = tn.GetName()
		cfg.Local = tn.GetLocal()
		cfg.PrivateKey = tn.GetPrivateKeyPath()
		cfg.MapTo = tn.GetRemote()
		cfg.SshServer = tn.GetServer()
		configs = append(configs, cfg)
	}

	tnConfig := &tConfigs{
		Tunnels:       configs,
		TunnelTimeout: int(s.root.dashboard.Mario.CheckAliveInterval.Seconds()),
	}
	if s.output == "" {
		s.output = path.Join(GetUserHome(), "tunnels.json")
	}

	marshaled, err := json.MarshalIndent(tnConfig, "", "    ")
	if err != nil {
		logrus.WithError(err).Errorln("save tunnels failed.")
	}

	err = ioutil.WriteFile(s.output, marshaled, 0644)
	if err != nil {
		logrus.Errorln("can not write file to disk because of: ", err)
	}
}

type viewCommand struct {
	command

	tunnelName string

	table *tablewriter.Table
}

func (c *viewCommand) ClearFlags() {
	c.command.ClearFlags()
	c.tunnelName = ""
}

func (c *viewCommand) Complete(args []string, word string) []prompt.Suggest {
	// if  starts with -- ,  returns the flags
	suggests := make([]prompt.Suggest, 0)
	if strings.HasPrefix(word, "--") {
		c.cmd.Flags().VisitAll(flagHasPrefix(word, &suggests))
		return suggests
	}
	// if the last part of the command is "--name" or "-n", return the tunnel list
	if len(args) > 2 && (args[len(args)-2] == "--name" || args[len(args)-2] == "-n") {
		for _, tn := range c.root.dashboard.GetTunnels() {
			suggests = append(suggests, prompt.Suggest{
				Text:        tn.GetName(),
				Description: "ID: " + strconv.Itoa(tn.GetID()) + "(" + tn.GetStatus() + ")",
			})
		}
		return prompt.FilterHasPrefix(suggests, word, true)
	}

	for _, tn := range c.root.dashboard.GetTunnels() {
		suggests = append(suggests, prompt.Suggest{
			Text:        strconv.Itoa(tn.GetID()),
			Description: tn.GetName() + "(" + tn.GetStatus() + ")",
		})
	}
	return prompt.FilterHasPrefix(suggests, word, true)
}

func (c *viewCommand) Run(cmd *cobra.Command, args []string) {
	if len(args) == 0 && c.tunnelName == "" {
		logrus.Errorln("specify tunnel id or tunnel name")
		return
	}
	var cs []*ssh.Connector
	if len(args) > 0 {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			logrus.Errorln("id should be a number", args[0])
			return
		}
		// close tunnel with id
		cs = c.root.dashboard.GetTunnelConnections(id)
	} else {
		cs = c.root.dashboard.GetTunnelConnections(c.tunnelName)
	}

	if len(cs) == 0 {
		return
	}

	c.table.ClearRows()
	rows := make([][]string, len(cs))
	for i, cnt := range cs {
		rows[i] = []string{strconv.FormatUint(cnt.ID(), 10), cnt.String()}
	}
	c.table.AppendBulk(rows)
	c.table.Render()
}

func NewCommand(name, short, long string, completer completeFunc, runner func(*cobra.Command, []string)) *command {
	return &command{
		root: nil,
		name: name,
		cmd: &cobra.Command{
			Use:   name,
			Short: short,
			Long:  long,
		},
		completer: completer,
		children:  make([]promptCommand, 0),
	}
}

func builtinCommand(root *interactiveCmd, name, short, long string, completer completeFunc, runner func(*cobra.Command, []string)) *command {
	return &command{
		root: root,
		name: name,
		cmd: &cobra.Command{
			Use:   name,
			Short: short,
			Long:  long,
			Run:   runner,
		},
		completer: completer,
		children:  make([]promptCommand, 0),
	}
}

func getChildCommand(cmd promptCommand, name string) promptCommand {
	for _, c := range cmd.Children() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func (i *interactiveCmd) buildCommands() {
	listCmd := NewListCommand(i)
	listCmd.cmd.Run = listCmd.Run

	openCmd := &openCommand{
		command: command{
			root: i,
			name: "open",
			cmd: &cobra.Command{
				Use:   "open",
				Short: "establish a tunnel",
			},
			children: make([]promptCommand, 0),
		},
	}
	openCmd.cmd.Run = openCmd.Run
	openCmd.cmd.Flags().StringVarP(
		&openCmd.tunnelName, "name", "n", "", "name of this tunnel")
	openCmd.cmd.Flags().StringVarP(
		&openCmd.link, "link", "l", "",
		"tunnel info, format: <local>:<remote>@<user>@<ssh_server>. e.g. :1080:192.168.1.2:1080@user@host.com:22 ")
	openCmd.cmd.Flags().StringVar(&openCmd.local, "local",
		":8080", "local address of the tunnel to listen")
	openCmd.cmd.Flags().StringVarP(&openCmd.server, "server", "s", "",
		"ssh server address of this tunnel, e.g. user@host.com:22, "+
			"if local not specified, the default local 22 will be used.")
	openCmd.cmd.Flags().StringVarP(&openCmd.remote, "remote", "r", "",
		"remote address of the tunnel. e.g. 192.168.1.2:1080")
	openCmd.cmd.Flags().StringVarP(&openCmd.pk, "key", "k", "",
		"ssh private key file path, if not provided, the global key path will be used")

	closeCmd := &closeOrUpCommand{
		command: command{
			root: i,
			name: "close",
			cmd: &cobra.Command{
				Use:   "close",
				Short: "close a tunnel",
			},
			children: make([]promptCommand, 0),
		},
		listCmd: listCmd,
	}
	closeCmd.cmd.Run = closeCmd.Run
	closeCmd.cmd.Flags().StringVarP(&closeCmd.tunnelName, "name", "n", "", "specify tunnel name")

	upCmd := &closeOrUpCommand{
		command: command{
			root: i,
			name: "up",
			cmd: &cobra.Command{
				Use:   "up",
				Short: "refresh a tunnel",
			},
			children: make([]promptCommand, 0),
		},
		listCmd: listCmd,
	}
	upCmd.cmd.Run = upCmd.Run
	upCmd.cmd.Flags().StringVarP(
		&upCmd.tunnelName, "name", "n", "", "specify tunnel name")

	saveCmd := &saveCommand{
		command: command{
			root: i,
			name: "save",
			cmd: &cobra.Command{
				Use:   "save",
				Short: "save tunnels to disk",
			},
			children: make([]promptCommand, 0),
		},
	}
	saveCmd.cmd.Run = saveCmd.Run
	saveCmd.cmd.Flags().StringVarP(&saveCmd.output, "output", "o", "",
		"output file path to save tunnels information")

	helpCmd := &command{
		root: i,
		name: "help",
		cmd: &cobra.Command{
			Use:   "help",
			Short: "print usage",
			Run: func(cmd *cobra.Command, args []string) {
				_ = i.command.Usage()
			},
			Hidden: true,
		},
		children: make([]promptCommand, 0),
	}

	exit := &command{
		root: i,
		name: "exit",
		cmd: &cobra.Command{
			Use:   "exit",
			Short: "exit mario",
			Run: func(cmd *cobra.Command, args []string) {
				i.dashboard.Quit()
				i.exitParser.Exit()
			},
		},
		completer: nil,
		children:  make([]promptCommand, 0),
	}

	viewCmd := &viewCommand{
		command: command{
			root: i,
			name: "view",
			cmd: &cobra.Command{
				Use:   "view",
				Short: "list connections of a tunnel",
			},
			children: make([]promptCommand, 0),
		},
		table: tablewriter.NewWriter(os.Stdout),
	}
	viewCmd.table.SetHeader([]string{"id", "detail"})
	viewCmd.table.SetRowLine(false)
	viewCmd.cmd.Run = viewCmd.Run
	viewCmd.cmd.Flags().StringVarP(&viewCmd.tunnelName, "name", "n", "", "specify tunnel name")

	i.AddChildren(listCmd, openCmd, closeCmd, upCmd, saveCmd, helpCmd, viewCmd, exit)
}

func flagHasPrefix(w string, filterTo *[]prompt.Suggest) func(flag *pflag.Flag) {
	return func(flag *pflag.Flag) {
		if len(w) <= 2 || strings.HasPrefix(flag.Name, w[2:]) {
			*filterTo = append(*filterTo, prompt.Suggest{
				Text:        "--" + flag.Name,
				Description: flag.Usage,
			})
		}
	}
}

// ExitParser modifies the original parser to provide an ability to exit directly(return
// from `Run` method of a Prompt), while the original way is to press Control+D.
type ExitParser struct {
	*prompt.PosixParser

	exit uint32
}

func (e *ExitParser) Read() ([]byte, error) {
	exited := atomic.LoadUint32(&e.exit)
	if exited > 0 {
		return []byte{0x04}, nil
	}
	return e.PosixParser.Read()
}

func (e *ExitParser) Exit() {
	atomic.StoreUint32(&e.exit, 1)
}

func NewExitParser() *ExitParser {
	return &ExitParser{
		PosixParser: prompt.NewStandardInputParser(),
		exit:        0,
	}
}
