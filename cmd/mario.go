package cmd

import (
	"fmt"
	"github.com/Jonwing/mario/internal"
	json "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"io/ioutil"
	"os"
	"os/user"
	"path"
	"runtime/pprof"
)

type baseCommand struct {
	cmd *cobra.Command

	// global config file path
	configPath string

	// private key file path, default to ~/.ssh/id_rsa
	pkPath string

	// interval to check whether tunnel is alive, default to 15 seconds, unit: second
	heartbeatInterval int

	// Debug if true, logs the debug logs
	debug bool
}

func (b *baseCommand) getCommand() *cobra.Command {
	return b.cmd
}

func (b *baseCommand) runDefault(cmd *cobra.Command, args []string) error {
	doProf := os.Getenv("MARIO_PROF")
	if doProf != "" {
		cpuProf, err := os.Create("cpu.prof")
		if err != nil {
			return err
		}
		memProf, err := os.Create("mem.prof")
		if err != nil {
			return err
		}
		err = pprof.StartCPUProfile(cpuProf)
		if err != nil {
			return err
		}

		defer func() {
			pprof.StopCPUProfile()
			pprof.WriteHeapProfile(memProf)
			cpuProf.Close()
			memProf.Close()
		}()
	}
	logger := new(log)
	dashBoard := internal.DefaultDashboard(b.pkPath, logger)
	if b.debug {
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}
	// logrus.SetFormatter(&logrus.TextFormatter{})
	configs := &tConfigs{Tunnels: make([]*tConfig, 0)}
	// if we get a configPath, load the config
	if b.configPath != "" {
		content, err := ioutil.ReadFile(b.configPath)
		if err != nil {
			return err
		}
		err = json.Unmarshal(content, configs)
		if err != nil {
			return err
		}
	}

	tCmd := NewInteractiveCommand(dashBoard)

	// establish tunnels fo existed config
	go func() {
		for _, cfg := range configs.Tunnels {
			_ = dashBoard.NewTunnel(cfg.Name, cfg.Local, cfg.SshServer, cfg.MapTo, cfg.PrivateKey)
		}
	}()

	err := dashBoard.Work()
	if err != nil {
		return err
	}
	_ = tCmd.command.Usage()
	tCmd.Run()
	return nil
}

func (b *baseCommand) Execute() {
	err := b.cmd.Execute()
	if err != nil {
		fmt.Printf("error: %s", err.Error())
	}
}

func BuildCommand() *baseCommand {
	b := &baseCommand{}
	b.cmd = &cobra.Command{
		Use:   "mario [options] [flags]",
		Short: "mario handles pipes(ssh tunnels) for you",
		Long:  "Manage ssh tunnels(establishing, closing, health check, reconnect...)",
		RunE:  b.runDefault,
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
		&b.debug, "debug", "v", false, "(v)verbose: logs the debug info")
	return b
}

func Execute() {
	b := BuildCommand()
	b.Execute()
}
