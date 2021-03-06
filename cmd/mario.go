package cmd

import (
	"fmt"
	"github.com/Jonwing/mario/internal"
	json "github.com/json-iterator/go"
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
			_ = pprof.WriteHeapProfile(memProf)
			_ = cpuProf.Close()
			_ = memProf.Close()
		}()
	}

	configs := &tConfigs{Tunnels: make([]*tConfig, 0), TunnelTimeout: b.heartbeatInterval}
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
	dashBoard := internal.DefaultDashboard(b.pkPath, configs.TunnelTimeout)

	tCmd := NewInteractiveCommand(dashBoard)
	tCmd.configLogger(b.debug)

	err := dashBoard.Work()
	if err != nil {
		return err
	}
	_ = tCmd.command.Usage()

	// establish tunnels for existed config
	go func() {
		for _, cfg := range configs.Tunnels {
			err = dashBoard.NewTunnel(cfg.Name, cfg.Local, cfg.SshServer, cfg.MapTo, cfg.PrivateKey, cfg.DontConnect)
			if err != nil {
				fmt.Printf("[Error] tunnel `%s` open failed because of %s", cfg.Name, err.Error())
			}
		}
	}()

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
	b := &baseCommand{heartbeatInterval: 15}
	b.cmd = &cobra.Command{
		Use:   "mario [options] [flags]",
		Short: "mario handles pipes(ssh tunnels) for you",
		Long:  "Manage ssh tunnels(establishing, closing, health check, reconnect...)",
		RunE:  b.runDefault,
	}

	if u, err := user.Current(); err == nil {
		b.pkPath = path.Join(u.HomeDir, ".ssh/id_rsa")
	}
	b.cmd.Flags().StringVarP(
		&b.configPath, "config", "c", "", "the config file path")
	b.cmd.Flags().StringVar(
		&b.pkPath, "pk", b.pkPath, "pk(private key): the SSH private key file path")
	b.cmd.Flags().IntVar(
		&b.heartbeatInterval, "i", 15, "i(interval): the check-alive interval of a tunnel in second")
	b.cmd.Flags().BoolVarP(
		&b.debug, "debug", "v", false, "(v)verbose: logs the debug info")
	return b
}

func Execute() {
	b := BuildCommand()
	b.Execute()
}
