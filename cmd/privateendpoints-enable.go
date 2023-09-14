package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var privateEndpointsEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enables the Private Endpoints feature on a cloud cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		err := cloudDeployer.EnablePrivateEndpoints(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to enable private endpoints", zap.Error(err))
		}
	},
}

func init() {
	privateEndpointsCmd.AddCommand(privateEndpointsEnableCmd)
}
