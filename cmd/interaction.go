package cmd

import (
	"github.com/Jonwing/mario/internal"
	"github.com/c-bata/go-prompt"
	"github.com/c-bata/go-prompt/completer"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"regexp"
)

var spacePtn = regexp.MustCompile(`\s+`)

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

	exitParser *ExitParser

	children []promptCommand

	logger *zap.SugaredLogger
}

func NewInteractiveCommand(dashboard *internal.Dashboard) *interactiveCmd {
	it := &interactiveCmd{
		dashboard: dashboard,
	}
	it.command = &cobra.Command{
		Use:   "[command]",
		Short: "manage tunnels",
		Long:  "open, close and save tunnels",
		Run:   it.execute,
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
		_ = i.command.Usage()
		return
	}
}

func (i *interactiveCmd) Run() {
	i.pmt.Run()
}

func (i *interactiveCmd) configLogger(debug bool) {
	var logger *zap.Logger
	if debug {
		logger, _ = zap.NewDevelopment()
	} else {
		logger, _ = zap.NewProduction()
	}
	i.logger = logger.Sugar()
}
