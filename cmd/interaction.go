package cmd

import (
	"errors"
	"github.com/Jonwing/mario/internal"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"io/ioutil"
	"path"
	"strconv"
	"strings"
)

type action int

const (
	actionOpenTunnel = action(iota)
	actionCloseTunnel
	actionReconnect
	actionSave
	actionHelp
)

var (
	actions = map[string]action{
		"open": actionOpenTunnel,
		"close": actionCloseTunnel,
		"up": 	actionReconnect,
		"save": actionSave,
		"help": actionHelp,
	}
	names = []string{"close", "help", "next", "open", "prev", "save", "tunnel", "up"}
)

type iArgs struct {
	// name the tunnel name
	name string

	// link shortcut to specify tunnel config. e.g. 8080:192.168.1.2:8080@user@one_host.com:22
	// it this flag is set, the port, server, remote flag will be ignored
	link string

	// the tunnel local port
	port int

	// the ssh server address, include user. e.g. user@one_host.com:22
	server string

	// the other endpoint of the tunnel. e.g. 192.168.1.2:8080
	remote string

	// private key file path, if not provided, use the global config
	privateKeyPath string

	// the file path to save tunnel infos [while you run `tunnel save`]
	configOut string
}

type interactiveCmd struct {
	iArgs
	
	command *cobra.Command

	dashboard *internal.Dashboard
}

func NewInteractiveCommand(dashboard *internal.Dashboard) *interactiveCmd {
	it := &interactiveCmd{dashboard:dashboard}
	it.command = &cobra.Command{
		Use: " [action] [flags]",
		Short: "manage tunnels",
		Long: "open, close and save tunnels",
		RunE: it.execute,
	}

	openCmd := &cobra.Command{
		Use: "open [flags]",
		Short: "Establish tunnel",
		RunE: it.openTunnel,
	}

	closeCmd := &cobra.Command{
		Use: "close [tunnel_id] [flags]",
		Short: "Close tunnel",
		Long: "Close tunnel by [tunnel_id], can also use the --name(-n) to specify tunnel name",
		RunE: it.closeTunnel,
	}

	upCmd := &cobra.Command{
		Use: "up [tunnel_id]",
		Short: "Reconnect disconnected tunnel",
		Long: "Reconnect disconnected tunnel",
		RunE: it.reconnectTunnel,
	}

	saveCmd := &cobra.Command{
		Use: "save [flags]",
		Short: "Save your tunnels config to file system.",
		Long: "provide --output(-o) to specify path to save, if not, user home directory will be used.",
		RunE: it.saveTunnels,
	}

	nxtPageCmd := &cobra.Command{
		Use: "next",
		Short: "turn to next page",
		Run: it.nextPage,
	}

	prevPageCmd := &cobra.Command{
		Use: "prev",
		Short: "turn to previous page",
		Run: it.prevPage,
	}

	it.command.AddCommand(openCmd, closeCmd, saveCmd, upCmd, nxtPageCmd, prevPageCmd)

	it.command.PersistentFlags().StringVarP(&it.name, "name", "n", "", "specify tunnel name")
	openCmd.Flags().StringVarP(
		&it.link, "link", "l", "",
		"composed format of the tunnel info. e.g. 1080:192.168.1.2:1080@user@host.com:22, " +
		"this establishes a tunnel from local port 1080 to remote 1080 port of 192.168.1.2 " +
		"in the network of ssh server user@host.com, ssh port 22")
	openCmd.Flags().IntVarP(&it.port, "port", "p",
		22, "local port of the tunnel to listen")
	openCmd.Flags().StringVarP(&it.server, "server", "s", "",
		"the ssh server address of this tunnel, e.g. user@host.com:22, " +
		"if port not specified, the default port 22 will be used.")
	openCmd.Flags().StringVarP(&it.remote, "remote", "r", "",
		"the remote endpoint of the tunnel. e.g. 192.168.1.2:1080")
	it.command.PersistentFlags().StringVarP(&it.privateKeyPath, "key", "k", "",
		"the ssh private key file path, if not provided, the global key path will be used")
	saveCmd.Flags().StringVarP(&it.configOut, "output", "o", "",
		"the output file path to save tunnels information")

	it.command.SetOut(it.dashboard.GetLogView())
	it.command.SetErr(it.dashboard.GetLogView())
	it.dashboard.SetInputAutoComplete(it.autoComplete)
	return it
}

func (i *interactiveCmd) RunCommand(args []string) (err error) {
	i.command.SetArgs(args)
	return i.command.Execute()
}

func (i *interactiveCmd) execute(cmd *cobra.Command, args []string) (err error) {
	if len(args) < 1 {
		return i.command.Usage()
	}
	act, ok := actions[args[0]]
	if !ok {
		return i.command.Usage()
	}

	switch act {
	case actionOpenTunnel:
		return i.openTunnel(cmd, args[1:])
	case actionCloseTunnel:
		return i.closeTunnel(cmd, args[1:])
	case actionSave:
		return i.saveTunnels(cmd, args[1:])
	case actionReconnect:
		return i.reconnectTunnel(cmd, args[1:])
	default:
		return i.command.Usage()
	}
}

func (i *interactiveCmd) openTunnel(cmd *cobra.Command, args []string) (err error) {
	if i.link != "" {
		// this should split the link into [mapping, server] slice
		parts := strings.SplitN(i.link, "@", 2)
		if len(parts) != 2 {
			logrus.Errorln("wrong link: ", i.link)
			return nil
		}
		// this should split mapping into [port, remote] slice
		mapping := strings.SplitN(parts[0], ":", 2)
		if len(mapping) != 2 {
			logrus.Errorln("wrong link: ", i.link)
			return nil
		}
		i.port, err = strconv.Atoi(mapping[0])
		if err != nil {
			return
		}
		i.remote = mapping[1]

		i.server = parts[1]
	} else {
		if i.server == "" || i.remote == "" {
			return errors.New("[Error]Should specify server by -s and remote by -r")
		}
	}

	err = i.dashboard.NewTunnel(i.name, i.port, i.server, i.remote, i.privateKeyPath)
	if err != nil {
		logrus.WithError(err).Errorf(
			"Open tunnel failed. port: %d, server: %s, remote: %s", i.port, i.server, i.remote)
	}
	return err
}


// closeTunnel the command is like "tunnel close 1 " or "tunnel close --name alias"
// if there is an id provided, it would be at args[0], and the name flag will be ignored
func (i *interactiveCmd) closeTunnel(cmd *cobra.Command, args []string) (err error) {
	if len(args) == 0 && i.name == "" {
		return errors.New("specify tunnel id or tunnel name")
	}
	if len(args) > 0 {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return err
		}
		// close tunnel with id
		logrus.Infoln("[Info] close tunnel ", id)
		return i.dashboard.CloseTunnel(id)
	}

	logrus.Infoln("[Info] close tunnel ", i.name)
	return i.dashboard.CloseTunnel(i.name)
}

func (i *interactiveCmd) reconnectTunnel(cmd *cobra.Command, args []string) (err error) {
	if len(args) == 0 && i.name == "" {
		return errors.New("specify tunnel id or tunnel name")
	}
	if len(args) > 0 {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return err
		}
		// close tunnel with id
		logrus.Infoln("[Info] up tunnel ", id)
		return i.dashboard.UpTunnel(id)
	}

	logrus.Infoln("[Info] close tunnel ", i.name)
	return i.dashboard.UpTunnel(i.name)
}

func (i *interactiveCmd) saveTunnels(cmd *cobra.Command, args []string) error {
	tns := i.dashboard.GetTunnels()
	configs := make([]*tConfig, 0)
	for _, tn := range tns {
		cfg := new(tConfig)
		cfg.Name = tn.GetName()
		cfg.LocalPort = tn.GetLocalPort()
		cfg.PrivateKey = tn.GetPrivateKeyPath()
		cfg.MapTo = tn.GetRemote()
		cfg.SshServer = tn.GetServer()
		configs = append(configs, cfg)
	}

	tnConfig := &tConfigs{Tunnels:configs}
	outPath := i.configOut
	if outPath == "" {
		outPath = path.Join(GetUserHome(), "tunnels.json")
	}

	marshaled, err := jsoniter.MarshalIndent(tnConfig, "", "    ")
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(outPath, marshaled, 0644)
	if err == nil {
		logrus.Infoln("tunnels have been saved to ", outPath)
	}
	return err
}

func (i *interactiveCmd) nextPage(cmd *cobra.Command, args []string) {
	i.dashboard.Page(1)
}

func (i *interactiveCmd) prevPage(cmd *cobra.Command, args []string) {
	i.dashboard.Page(-1)
}




func (i *interactiveCmd) autoComplete(current string) (entries []string) {

	if len(current) == 0 {
		return
	}

	parts := strings.Split(current, " ")
	lastWord := parts[len(parts)-1]

	if lastWord == "" {
		return
	}
	for _, word := range names {
		if strings.HasPrefix(strings.ToLower(word), strings.ToLower(lastWord)) {
			parts[len(parts)-1] = word
			suggested := strings.Join(parts, " ")
			entries = append(entries, suggested)
		}
	}
	return
}