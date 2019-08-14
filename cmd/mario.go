package cmd

import (
	"github.com/Jonwing/mario/internal"
	json "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"io/ioutil"
	"os/user"
	"path"
	"strings"
)


type handler func(cmd *cobra.Command, args []string) error

type cmder interface {
	getCommand() *cobra.Command
}


type baseCommand struct {
	cmd *cobra.Command

	// global config file path
	configPath string

	// private key file path, default to ~/.ssh/id_rsa
	pkPath string

	// interval to check whether tunnel is alive, default to 15 seconds, unit: second
	heartbeatInterval int

	// runs in background, without terminal UI
	bg bool
}


func (b *baseCommand) getCommand() *cobra.Command {
	return b.cmd
}

func (b *baseCommand) runDefault(cmd *cobra.Command, args []string) error {
	logger := new(log)
	dashBoard := internal.DefaultDashboard(b.pkPath, logger)
	logrus.SetLevel(logrus.InfoLevel)
	logrus.SetOutput(dashBoard.GetLogView())
	// logrus.SetFormatter(&logrus.TextFormatter{})
	configs := &tConfigs{Tunnels:make([]*tConfig, 0)}
	// if we get a configPath, load the config
	if b.configPath != ""{
		content, err := ioutil.ReadFile(b.configPath)
		if err != nil {
			return err
		}
		err = json.Unmarshal(content, configs)
		if err != nil {
			return err
		}
	}

	// TODO: if bg is true
	tCmd := NewInteractiveCommand(dashBoard)

	go func() {
		for _, cfg := range configs.Tunnels {
			err := dashBoard.NewTunnel(cfg.Name, cfg.LocalPort, cfg.SshServer, cfg.MapTo, cfg.PrivateKey)
			if err != nil {
				logrus.WithError(err).Errorf(
					"Open tunnel failed. port: %d, server: %s, remote: %s", cfg.LocalPort, cfg.SshServer, cfg.MapTo)
			}
		}
		for txt := range dashBoard.GetInput() {
			args := strings.Split(txt, " ")
			err := tCmd.RunCommand(args[1:])
			if err != nil {
				logrus.Errorln("command error: ", err.Error())
			}
		}
	}()
	err := dashBoard.Show()
	return err
}

func (b *baseCommand) Execute() {
	logrus.Fatal(b.cmd.Execute())
}


func BuildCommand() *baseCommand {
	b := &baseCommand{}
	b.cmd = &cobra.Command{
		Use:                        "mario [options] [flags]",
		Short:                      "mario handles pipes(ssh tunnels) for you",
		Long:                       "Manage ssh tunnels(establishing, closing, health check, reconnect...)",
		RunE: 						b.runDefault,
	}


	if u, err := user.Current(); err == nil {
		b.pkPath = path.Join(u.HomeDir, ".ssh/id_rsa")
	}
	b.cmd.PersistentFlags().StringVarP(
		&b.configPath, "config", "c", "", "the config file path")
	b.cmd.PersistentFlags().StringVar(
		&b.pkPath, "pk", b.pkPath, "pk(private key): the SSH private key file path")
	b.cmd.PersistentFlags().IntVar(
		&b.heartbeatInterval, "i", 15, "i(interval): the check-alive interval of a tunnel in second")
	b.cmd.PersistentFlags().BoolVarP(
		&b.bg, "detach", "d", false, "d(detach): run mario in background")
	return b
}

func Execute() {
	b := BuildCommand()
	b.Execute()
}
