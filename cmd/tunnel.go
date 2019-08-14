package cmd

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type tunnelAddCmd struct {
	cmd *cobra.Command

	name string

	localPort int

	server string

	remote string
}

type tunnelCmd struct {
	cmd *cobra.Command

	name string

}

func (t *tunnelCmd) RunE(cmd *cobra.Command, args []string) error  {
	logrus.Infoln("tunnel args: ", args)
	logrus.Infoln("name: ", t.name)
	return nil
}

func getTunnelCommand() *tunnelCmd {
	tc := &tunnelCmd{}
	tc.cmd = &cobra.Command{
		Use:                        "tunnel [action] [flags]",
		Short:                      "managing tunnels",
		Long:                       "open, close channel with alias",
		RunE:                       tc.RunE,
	}
	tc.cmd.PersistentFlags().StringVarP(&tc.name, "name", "n", "", "assign tunnel's name")
	return tc
}
