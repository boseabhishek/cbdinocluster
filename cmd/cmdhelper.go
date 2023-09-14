package cmd

import (
	"context"
	"log"
	"os"
	"os/user"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/couchbaselabs/cbdinocluster/deployment/dockerdeploy"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
)

type CmdHelper struct {
	logger *zap.Logger

	config *cbdcconfig.Config
}

func (h *CmdHelper) GetContext() context.Context {
	return context.Background()
}

func (h *CmdHelper) GetLogger() *zap.Logger {
	if h.logger == nil {
		verbose, _ := rootCmd.Flags().GetBool("verbose")

		logConfig := zap.NewDevelopmentConfig()
		if !verbose {
			logConfig.Level.SetLevel(zap.InfoLevel)
			logConfig.DisableCaller = true
		}

		logger, err := logConfig.Build()
		if err != nil {
			log.Fatalf("failed to initialize verbose logger: %s", err)
		}

		logger.Info("logger initialized")

		h.logger = logger
	}

	return h.logger
}

func (h *CmdHelper) GetConfig(ctx context.Context) *cbdcconfig.Config {
	logger := h.GetLogger()

	if h.config == nil {
		curConfig, err := cbdcconfig.Load(ctx)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				logger.Fatal("failed to load config file", zap.Error(err))
			}
		}

		if curConfig == nil {
			logger.Fatal("you must run the `init` command first")
		}

		h.config = curConfig
	}

	return h.config
}

func (h *CmdHelper) getDockerDeployer(ctx context.Context) (*dockerdeploy.Deployer, error) {
	logger := h.GetLogger()
	config := h.GetConfig(ctx)

	if !config.Docker.Enabled.Value() {
		return nil, nil
	}

	githubToken := config.GitHub.Token
	githubUser := config.GitHub.User
	dockerHost := config.Docker.Host
	dockerNetwork := config.Docker.Network

	dockerCli, err := client.NewClientWithOpts(
		client.WithHost(dockerHost),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to docker")
	}

	deployer, err := dockerdeploy.NewDeployer(&dockerdeploy.DeployerOptions{
		Logger:       logger,
		DockerCli:    dockerCli,
		NetworkName:  dockerNetwork,
		GhcrUsername: githubUser,
		GhcrPassword: githubToken,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to initializer deployer")
	}

	return deployer, nil
}

func (h *CmdHelper) getCloudDeployer(ctx context.Context) (*clouddeploy.Deployer, error) {
	logger := h.GetLogger()
	config := h.GetConfig(ctx)

	if !config.Capella.Enabled.Value() {
		return nil, nil
	}

	capellaUser := config.Capella.Username
	capellaPass := config.Capella.Password
	capellaOid := config.Capella.OrganizationID

	client, err := capellacontrol.NewController(ctx, &capellacontrol.ControllerOptions{
		Logger:   logger,
		Endpoint: "https://api.cloud.couchbase.com",
		Auth: &capellacontrol.BasicCredentials{
			Username: capellaUser,
			Password: capellaPass,
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create controller")
	}

	defaultCloud := config.Capella.DefaultCloud
	defaultAwsRegion := config.Capella.DefaultAwsRegion
	defaultAzureRegion := config.Capella.DefaultAzureRegion
	defaultGcpRegion := config.Capella.DefaultGcpRegion

	prov, err := clouddeploy.NewDeployer(&clouddeploy.NewDeployerOptions{
		Logger:             logger,
		Client:             client,
		TenantID:           capellaOid,
		DefaultCloud:       defaultCloud,
		DefaultAwsRegion:   defaultAwsRegion,
		DefaultAzureRegion: defaultAzureRegion,
		DefaultGcpRegion:   defaultGcpRegion,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create deployer")
	}

	return prov, nil
}

func (h *CmdHelper) GetAllDeployers(ctx context.Context) map[string]deployment.Deployer {
	logger := h.GetLogger()

	out := make(map[string]deployment.Deployer)

	dockerDeployer, _ := h.getDockerDeployer(ctx)
	if dockerDeployer != nil {
		out["docker"] = dockerDeployer
	}

	cloudDeployer, _ := h.getCloudDeployer(ctx)
	if cloudDeployer != nil {
		out["cloud"] = cloudDeployer
	}

	logger.Info("identified available deployers",
		zap.Strings("deployers", maps.Keys(out)))

	return out
}

func (h *CmdHelper) GetDeployer(ctx context.Context) deployment.Deployer {
	config := h.GetConfig(ctx)

	if config.DefaultDeployer == "cloud" {
		return h.GetCloudDeployer(ctx)
	} else {
		return h.GetDockerDeployer(ctx)
	}
}

func (h *CmdHelper) GetDeployerByName(ctx context.Context, deployerName string) deployment.Deployer {
	logger := h.GetLogger()
	allDeployers := h.GetAllDeployers(ctx)

	deployer := allDeployers[deployerName]
	if deployer == nil {
		logger.Fatal("failed to find deployer",
			zap.String("deployer", deployerName),
			zap.Strings("availableDeployers", maps.Keys(allDeployers)))
	}

	return deployer
}

func (h *CmdHelper) GetDefaultDeployer(ctx context.Context) deployment.Deployer {
	logger := h.GetLogger()
	config := h.GetConfig(ctx)
	allDeployers := h.GetAllDeployers(ctx)

	deployerName := config.DefaultDeployer
	deployer := allDeployers[deployerName]
	if deployer == nil {
		logger.Fatal("failed to find default deployer",
			zap.String("defaultDeployer", deployerName),
			zap.Strings("availableDeployers", maps.Keys(allDeployers)))
	}

	return deployer
}

func (h *CmdHelper) GetDockerDeployer(ctx context.Context) *dockerdeploy.Deployer {
	logger := h.GetLogger()

	deployer, err := h.getDockerDeployer(ctx)
	if err != nil {
		logger.Fatal("failed to get docker deployer", zap.Error(err))
	}

	err = deployer.Cleanup(ctx)
	if err != nil {
		logger.Fatal("failed to run pre-cleanup", zap.Error(err))
	}

	return deployer
}

func (h *CmdHelper) GetCloudDeployer(ctx context.Context) *clouddeploy.Deployer {
	logger := h.GetLogger()

	deployer, err := h.getCloudDeployer(ctx)
	if err != nil {
		logger.Fatal("failed to get cloud deployer", zap.Error(err))
	}

	// This can take a long time sometimes, so this is only run manually.
	/*
		err = prov.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to run pre-cleanup", zap.Error(err))
		}
	*/

	return deployer
}

func (h *CmdHelper) GetAWSCredentials(ctx context.Context) aws.Credentials {
	logger := h.GetLogger()
	cbdcConfig := h.GetConfig(ctx)

	if !cbdcConfig.AWS.Enabled.Value() {
		logger.Fatal("cannot use aws when configuration is disabled")
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		logger.Fatal("failed to load AWS config", zap.Error(err))
	}

	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		logger.Fatal("failed to retreive AWS credentials", zap.Error(err))
	}

	return creds
}

func (h *CmdHelper) GetAzureCredentials(ctx context.Context) azcore.TokenCredential {
	logger := h.GetLogger()
	cbdcConfig := h.GetConfig(ctx)

	if !cbdcConfig.Azure.Enabled.Value() {
		logger.Fatal("cannot use azure when configuration is disabled")
	}

	creds, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		logger.Fatal("failed to fetch azure credentials", zap.Error(err))
	}

	return creds
}

func (h *CmdHelper) IdentifyCurrentUser() string {
	osUser, err := user.Current()
	if err != nil {
		return ""
	}

	return osUser.Username
}

func (h *CmdHelper) IdentifyCluster(ctx context.Context, userInput string) (string, deployment.Deployer, deployment.ClusterInfo) {
	logger := h.GetLogger()
	logger.Info("attempting to identify cluster", zap.String("input", userInput))

	type clusterWithDeployer struct {
		DeployerName string
		Deployer     deployment.Deployer
		Cluster      deployment.ClusterInfo
	}

	cancelCtx, cancel := context.WithCancel(ctx)

	var wg sync.WaitGroup
	identifiedCluster := make(chan *clusterWithDeployer, 1)

	allDeployers := h.GetAllDeployers(cancelCtx)
	for deployerName, deployer := range allDeployers {
		wg.Add(1)
		go func(deployerName string, deployer deployment.Deployer) {
			clusters, err := deployer.ListClusters(cancelCtx)
			if err != nil {
				// ignore errors if the context is cancelled
				if cancelCtx.Err() != nil {
					return
				}

				logger.Warn("failed to list clusters",
					zap.Error(err),
					zap.String("deployer", deployerName))
				return
			}

			logger.Debug("identified deployer clusters",
				zap.String("deployer", deployerName))

			for _, cluster := range clusters {
				if strings.HasPrefix(cluster.GetID(), userInput) {
					identifiedCluster <- &clusterWithDeployer{
						DeployerName: deployerName,
						Deployer:     deployer,
						Cluster:      cluster,
					}
				}
			}

			wg.Done()
		}(deployerName, deployer)
	}
	go func() {
		wg.Wait()
		close(identifiedCluster)
	}()

	for ident := range identifiedCluster {
		// once we find a cluster, we can cancel everyone else who is searching
		cancel()

		return ident.DeployerName, ident.Deployer, ident.Cluster
	}

	cancel()
	logger.Fatal("failed to identify cluster using specified identifier",
		zap.String("identifier", userInput))
	return "", nil, nil
}
