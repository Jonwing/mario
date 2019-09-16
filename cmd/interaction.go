package cmd

import (
	"github.com/Jonwing/mario/internal"
	"github.com/c-bata/go-prompt"
	"github.com/c-bata/go-prompt/completer"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"os"
	"regexp"
	"strconv"
)

type action int

const (
	actionOpenTunnel = action(iota)
	actionCloseTunnel
	actionReconnect
	actionSave
	actionHelp
)

var	spacePtn = regexp.MustCompile(`\s+`)

type iArgs struct {
	// name the tunnel name
	name string

	// link shortcut to specify tunnel config. e.g. 0.0.0.0:8080:192.168.1.2:8080@user@one_host.com:22
	// it this flag is set, the local, server, remote flag will be ignored
	link string

	// the tunnel local listening address
	local string

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

	// belows are members for prompt
	pmt *prompt.Prompt

	tw *tablewriter.Table

	exitParser *ExitParser

	children []completeCmder

}

func NewInteractiveCommand(dashboard *internal.Dashboard) *interactiveCmd {
	it := &interactiveCmd{
		dashboard:dashboard,
		tw: tablewriter.NewWriter(os.Stdout),
	}
	it.command = &cobra.Command{
		Use: "[command]",
		Short: "manage tunnels",
		Long: "open, close and save tunnels",
		Run: it.execute,
	}

	it.command.SetUsageTemplate(`mario helps you handle multiple SSH tunnels, you can open,
close, save tunnels in one place.

Usage:{{if .Runnable}}
	[command] [flags]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "[command] --help" for more information about a command.{{end}}
`)

	it.tw.SetHeader([]string{"id", "name", "status", "link", "remark"})
	it.tw.SetRowLine(false)

	it.exitParser = NewExitParser()

	it.pmt = prompt.New(
		it.runCommand,
		it.complete,
		prompt.OptionParser(it.exitParser),
		prompt.OptionTitle("mario: handler multiple SSH tunnels"),
		prompt.OptionPrefix("> "),
		prompt.OptionInputTextColor(prompt.Green),
		prompt.OptionCompletionWordSeparator(completer.FilePathCompletionSeparator),
		prompt.OptionSuggestionTextColor(prompt.DarkGray),
		prompt.OptionSuggestionBGColor(prompt.DarkBlue),
		prompt.OptionDescriptionTextColor(prompt.DarkGray),
		prompt.OptionDescriptionBGColor(prompt.Black),
		prompt.OptionSelectedSuggestionTextColor(prompt.DarkBlue),
		prompt.OptionSelectedSuggestionBGColor(prompt.White),
		prompt.OptionSelectedDescriptionTextColor(prompt.Black),
		prompt.OptionSelectedDescriptionBGColor(prompt.DarkBlue),
		)

	it.command.PersistentFlags().StringVarP(&it.privateKeyPath, "key", "k", "",
		"the ssh private key file path, if not provided, the global key path will be used")
	it.buildCommands()
	return it
}

func (i *interactiveCmd) RunCommand(args []string) (err error) {
	i.command.SetArgs(args)
	return i.command.Execute()
}

func (i *interactiveCmd) execute(cmd *cobra.Command, args []string) {
	if len(args) < 1 {
		i.command.Usage()
		return
	}

	// switch act {
	// case actionOpenTunnel:
	// 	i.openTunnel(cmd, args[1:])
	// case actionCloseTunnel:
	// 	i.closeTunnel(cmd, args[1:])
	// case actionSave:
	// 	i.saveTunnels(cmd, args[1:])
	// case actionReconnect:
	// 	i.reconnectTunnel(cmd, args[1:])
	// default:
	// 	i.command.Usage()
	// }
}

// func (i *interactiveCmd) openTunnel(cmd *cobra.Command, args []string) {
// 	if i.link != "" {
// 		// this should split the link into [mapping, server] slice
// 		parts := strings.SplitN(i.link, "@", 2)
// 		if len(parts) != 2 {
// 			logrus.Errorln("wrong link: ", i.link)
// 			return
// 		}
// 		// this should split mapping into [local host, local port, remote] slice
// 		mapping := strings.SplitN(parts[0], ":", 3)
// 		if len(mapping) != 3 {
// 			logrus.Errorln("wrong link: ", i.link)
// 			return
// 		}
//
// 		_, err := strconv.Atoi(mapping[1])
// 		if err != nil {
// 			logrus.Errorln("port must be a number: ", mapping[1])
// 			return
// 		}
// 		i.local = strings.Join(mapping[:2], ":")
// 		i.remote = mapping[2]
//
// 		i.server = parts[1]
// 	} else {
// 		if i.server == "" || i.remote == "" {
// 			logrus.Errorln("[Error]Should specify server by -s and remote by -r")
// 			return
// 		}
// 	}
//
// 	err := i.dashboard.NewTunnel(i.name, i.local, i.server, i.remote, i.privateKeyPath)
// 	if err != nil {
// 		logrus.WithError(err).Errorf(
// 			"Open tunnel failed. local: %d, server: %s, remote: %s", i.local, i.server, i.remote)
// 	}
// }


// closeTunnel the command is like "tunnel close 1 " or "tunnel close --name alias"
// if there is an id provided, it would be at args[0], and the name flag will be ignored
// func (i *interactiveCmd) closeTunnel(cmd *cobra.Command, args []string) {
// 	if len(args) == 0 && i.name == "" {
// 		logrus.Errorln("specify tunnel id or tunnel name")
// 		return
// 	}
// 	if len(args) > 0 {
// 		id, err := strconv.Atoi(args[0])
// 		if err != nil {
// 			logrus.Errorln("id should be a number", args[0])
// 			return
// 		}
// 		// close tunnel with id
// 		logrus.Infoln("[Info] close tunnel ", id)
// 		i.dashboard.CloseTunnel(id)
// 	}
//
// 	logrus.Infoln("[Info] close tunnel ", i.name)
// 	i.dashboard.CloseTunnel(i.name)
// }

// func (i *interactiveCmd) reconnectTunnel(cmd *cobra.Command, args []string) {
// 	if len(args) == 0 && i.name == "" {
// 		logrus.Errorln("specify tunnel id or tunnel name")
// 		return
// 	}
// 	if len(args) > 0 {
// 		id, err := strconv.Atoi(args[0])
// 		if err != nil {
// 			logrus.Errorln("id should be a number", args[0])
// 			return
// 		}
// 		// close tunnel with id
// 		logrus.Infoln("[Info] up tunnel ", id)
// 		i.dashboard.UpTunnel(id)
// 	}
//
// 	logrus.Infoln("[Info] close tunnel ", i.name)
// 	i.dashboard.UpTunnel(i.name)
// }

// func (i *interactiveCmd) saveTunnels(cmd *cobra.Command, args []string) {
// 	tns := i.dashboard.GetTunnels()
// 	configs := make([]*tConfig, 0)
// 	for _, tn := range tns {
// 		cfg := new(tConfig)
// 		cfg.Name = tn.GetName()
// 		cfg.Local = tn.GetLocal()
// 		cfg.PrivateKey = tn.GetPrivateKeyPath()
// 		cfg.MapTo = tn.GetRemote()
// 		cfg.SshServer = tn.GetServer()
// 		configs = append(configs, cfg)
// 	}
//
// 	tnConfig := &tConfigs{Tunnels:configs}
// 	outPath := i.configOut
// 	if outPath == "" {
// 		outPath = path.Join(GetUserHome(), "tunnels.json")
// 	}
//
// 	marshaled, err := json.MarshalIndent(tnConfig, "", "    ")
// 	if err != nil {
// 		logrus.WithError(err).Errorln("save tunnels failed.")
// 	}
//
// 	err = ioutil.WriteFile(outPath, marshaled, 0644)
// 	if err != nil {
// 		logrus.Errorln("can not write file to disk because of: ", err)
// 	}
// }

func (i *interactiveCmd) listTunnels(cmd *cobra.Command, args []string) {
	i.tw.ClearRows()
	tns := i.dashboard.GetTunnels()
	rows := make([][]string, len(tns))
	for i, tn := range tns {
		var errStr string
		if tn.Error() != nil {
			errStr = tn.Error().Error()
		}
		rows[i] = []string{strconv.Itoa(tn.GetID()), tn.GetName(), tn.GetStatus(), tn.Represent(), errStr}
	}
	i.tw.AppendBulk(rows)
	i.tw.Render()
}

func (i *interactiveCmd) Run() {
	i.pmt.Run()
}