package cmd

import (
	"time"

	"github.com/go-acme/lego/v4/lego"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	sdkmath "cosmossdk.io/math"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"

	"github.com/akash-network/provider"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
)

func addRunFlags(cmd *cobra.Command) error {
	cfg := provider.NewDefaultConfig()

	cmd.Flags().StringP(cflags.FlagHome, "", cli.DefaultHome, "directory for config and data")
	if err := viper.BindPFlag(cflags.FlagHome, cmd.Flags().Lookup(cflags.FlagHome)); err != nil {
		return err
	}

	cmd.Flags().Bool(FlagClusterK8s, false, "Use Kubernetes cluster")
	if err := viper.BindPFlag(FlagClusterK8s, cmd.Flags().Lookup(FlagClusterK8s)); err != nil {
		return err
	}

	cmd.Flags().String(providerflags.FlagK8sManifestNS, "lease", "Cluster manifest namespace")
	if err := viper.BindPFlag(providerflags.FlagK8sManifestNS, cmd.Flags().Lookup(providerflags.FlagK8sManifestNS)); err != nil {
		return err
	}

	cmd.Flags().String(FlagGatewayListenAddress, "0.0.0.0:8443", "Gateway listen address")
	if err := viper.BindPFlag(FlagGatewayListenAddress, cmd.Flags().Lookup(FlagGatewayListenAddress)); err != nil {
		return err
	}

	cmd.Flags().String(FlagGatewayGRPCListenAddress, "0.0.0.0:8444", "Gateway listen address")
	if err := viper.BindPFlag(FlagGatewayGRPCListenAddress, cmd.Flags().Lookup(FlagGatewayGRPCListenAddress)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidPricingStrategy, "scale", "Pricing strategy to use")
	if err := viper.BindPFlag(FlagBidPricingStrategy, cmd.Flags().Lookup(FlagBidPricingStrategy)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidPriceCPUScale, "0", "cpu pricing scale in uakt per millicpu")
	if err := viper.BindPFlag(FlagBidPriceCPUScale, cmd.Flags().Lookup(FlagBidPriceCPUScale)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidPriceMemoryScale, "0", "memory pricing scale in uakt per megabyte")
	if err := viper.BindPFlag(FlagBidPriceMemoryScale, cmd.Flags().Lookup(FlagBidPriceMemoryScale)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidPriceStorageScale, "0", "storage pricing scale in uakt per megabyte")
	if err := viper.BindPFlag(FlagBidPriceStorageScale, cmd.Flags().Lookup(FlagBidPriceStorageScale)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidPriceEndpointScale, "0", "endpoint pricing scale in uakt")
	if err := viper.BindPFlag(FlagBidPriceEndpointScale, cmd.Flags().Lookup(FlagBidPriceEndpointScale)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidPriceIPScale, "0", "leased ip pricing scale in uakt")
	if err := viper.BindPFlag(FlagBidPriceIPScale, cmd.Flags().Lookup(FlagBidPriceIPScale)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidPriceScriptPath, "", "path to script to run for computing bid price")
	if err := viper.BindPFlag(FlagBidPriceScriptPath, cmd.Flags().Lookup(FlagBidPriceScriptPath)); err != nil {
		return err
	}

	cmd.Flags().Uint(FlagBidPriceScriptProcessLimit, 32, "limit to the number of scripts run concurrently for bid pricing")
	if err := viper.BindPFlag(FlagBidPriceScriptProcessLimit, cmd.Flags().Lookup(FlagBidPriceScriptProcessLimit)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagBidPriceScriptTimeout, time.Second*10, "execution timelimit for bid pricing as a duration")
	if err := viper.BindPFlag(FlagBidPriceScriptTimeout, cmd.Flags().Lookup(FlagBidPriceScriptTimeout)); err != nil {
		return err
	}

	cmd.Flags().String(FlagBidDeposit, cfg.BidDeposit.String(), "Bid deposit amount")
	if err := viper.BindPFlag(FlagBidDeposit, cmd.Flags().Lookup(FlagBidDeposit)); err != nil {
		return err
	}

	cmd.Flags().String(FlagClusterPublicHostname, "", "The public IP of the Kubernetes cluster")
	if err := viper.BindPFlag(FlagClusterPublicHostname, cmd.Flags().Lookup(FlagClusterPublicHostname)); err != nil {
		return err
	}

	cmd.Flags().Uint(FlagClusterNodePortQuantity, 1, "The number of node ports available on the Kubernetes cluster")
	if err := viper.BindPFlag(FlagClusterNodePortQuantity, cmd.Flags().Lookup(FlagClusterNodePortQuantity)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagClusterWaitReadyDuration, time.Second*5, "The time to wait for the cluster to be available")
	if err := viper.BindPFlag(FlagClusterWaitReadyDuration, cmd.Flags().Lookup(FlagClusterWaitReadyDuration)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagInventoryResourcePollPeriod, time.Second*5, "The period to poll the cluster inventory")
	if err := viper.BindPFlag(FlagInventoryResourcePollPeriod, cmd.Flags().Lookup(FlagInventoryResourcePollPeriod)); err != nil {
		return err
	}

	cmd.Flags().Uint(FlagInventoryResourceDebugFrequency, 10, "The rate at which to log all inventory resources")
	if err := viper.BindPFlag(FlagInventoryResourceDebugFrequency, cmd.Flags().Lookup(FlagInventoryResourceDebugFrequency)); err != nil {
		return err
	}

	cmd.Flags().Bool(FlagDeploymentIngressStaticHosts, false, "")
	if err := viper.BindPFlag(FlagDeploymentIngressStaticHosts, cmd.Flags().Lookup(FlagDeploymentIngressStaticHosts)); err != nil {
		return err
	}

	cmd.Flags().String(FlagDeploymentIngressDomain, "", "")
	if err := viper.BindPFlag(FlagDeploymentIngressDomain, cmd.Flags().Lookup(FlagDeploymentIngressDomain)); err != nil {
		return err
	}

	cmd.Flags().Bool(FlagDeploymentIngressExposeLBHosts, false, "")
	if err := viper.BindPFlag(FlagDeploymentIngressExposeLBHosts, cmd.Flags().Lookup(FlagDeploymentIngressExposeLBHosts)); err != nil {
		return err
	}

	cmd.Flags().Bool(FlagDeploymentNetworkPoliciesEnabled, true, "Enable network policies")
	if err := viper.BindPFlag(FlagDeploymentNetworkPoliciesEnabled, cmd.Flags().Lookup(FlagDeploymentNetworkPoliciesEnabled)); err != nil {
		return err
	}

	cmd.Flags().String(FlagDockerImagePullSecretsName, "", "Name of the local image pull secret configured with kubectl")
	if err := viper.BindPFlag(FlagDockerImagePullSecretsName, cmd.Flags().Lookup(FlagDockerImagePullSecretsName)); err != nil {
		return err
	}

	cmd.Flags().Uint64(FlagOvercommitPercentMemory, 0, "Percentage of memory overcommit")
	if err := viper.BindPFlag(FlagOvercommitPercentMemory, cmd.Flags().Lookup(FlagOvercommitPercentMemory)); err != nil {
		return err
	}

	cmd.Flags().Uint64(FlagOvercommitPercentCPU, 0, "Percentage of CPU overcommit")
	if err := viper.BindPFlag(FlagOvercommitPercentCPU, cmd.Flags().Lookup(FlagOvercommitPercentCPU)); err != nil {
		return err
	}

	cmd.Flags().Uint64(FlagOvercommitPercentStorage, 0, "Percentage of storage overcommit")
	if err := viper.BindPFlag(FlagOvercommitPercentStorage, cmd.Flags().Lookup(FlagOvercommitPercentStorage)); err != nil {
		return err
	}

	cmd.Flags().StringSlice(FlagDeploymentBlockedHostnames, nil, "hostnames blocked for deployments")
	if err := viper.BindPFlag(FlagDeploymentBlockedHostnames, cmd.Flags().Lookup(FlagDeploymentBlockedHostnames)); err != nil {
		return err
	}

	cmd.Flags().String(FlagAuthPem, "", "")

	if err := providerflags.AddKubeConfigPathFlag(cmd); err != nil {
		return err
	}

	cmd.Flags().String(FlagDeploymentRuntimeClass, "gvisor", "kubernetes runtime class for deployments, use none for no specification")
	if err := viper.BindPFlag(FlagDeploymentRuntimeClass, cmd.Flags().Lookup(FlagDeploymentRuntimeClass)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagBidTimeout, 5*time.Minute, "time after which bids are cancelled if no lease is created")
	if err := viper.BindPFlag(FlagBidTimeout, cmd.Flags().Lookup(FlagBidTimeout)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagManifestTimeout, 5*time.Minute, "time after which bids are cancelled if no manifest is received")
	if err := viper.BindPFlag(FlagManifestTimeout, cmd.Flags().Lookup(FlagManifestTimeout)); err != nil {
		return err
	}

	cmd.Flags().String(FlagMetricsListener, "", "ip and port to start the metrics listener on")
	if err := viper.BindPFlag(FlagMetricsListener, cmd.Flags().Lookup(FlagMetricsListener)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagWithdrawalPeriod, time.Hour*24, "period at which withdrawals are made from the escrow accounts")
	if err := viper.BindPFlag(FlagWithdrawalPeriod, cmd.Flags().Lookup(FlagWithdrawalPeriod)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagLeaseFundsMonitorInterval, time.Minute*10, "interval at which lease is checked for funds available on the escrow accounts. >= 1m")
	if err := viper.BindPFlag(FlagLeaseFundsMonitorInterval, cmd.Flags().Lookup(FlagLeaseFundsMonitorInterval)); err != nil {
		return err
	}

	cmd.Flags().Uint64(FlagMinimumBalance, mvbeta.DefaultBidMinDeposit.Amount.Mul(sdkmath.NewIntFromUint64(2)).Uint64(), "minimum account balance at which withdrawal is started")
	if err := viper.BindPFlag(FlagMinimumBalance, cmd.Flags().Lookup(FlagMinimumBalance)); err != nil {
		return err
	}

	cmd.Flags().String(FlagProviderConfig, "", "provider configuration file path")
	if err := viper.BindPFlag(FlagProviderConfig, cmd.Flags().Lookup(FlagProviderConfig)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagRPCQueryTimeout, time.Minute, "timeout for requests made to the RPC node")
	if err := viper.BindPFlag(FlagRPCQueryTimeout, cmd.Flags().Lookup(FlagRPCQueryTimeout)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagCachedResultMaxAge, 5*time.Second, "max. cache age for results from the RPC node")
	if err := viper.BindPFlag(FlagCachedResultMaxAge, cmd.Flags().Lookup(FlagCachedResultMaxAge)); err != nil {
		return err
	}

	cmd.Flags().Bool(FlagEnableIPOperator, false, "enable usage of the IP operator to lease IP addresses")
	if err := viper.BindPFlag(FlagEnableIPOperator, cmd.Flags().Lookup(FlagEnableIPOperator)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagTxBroadcastTimeout, 30*time.Second, "tx broadcast timeout. defaults to 30s")
	if err := viper.BindPFlag(FlagTxBroadcastTimeout, cmd.Flags().Lookup(FlagTxBroadcastTimeout)); err != nil {
		return err
	}

	cmd.Flags().Uint(FlagMonitorMaxRetries, 40, "max count of status retries before closing the lease. defaults to 40")
	if err := viper.BindPFlag(FlagMonitorMaxRetries, cmd.Flags().Lookup(FlagMonitorMaxRetries)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagMonitorRetryPeriod, 4*time.Second, "monitor status retry period. defaults to 4s (min value)")
	if err := viper.BindPFlag(FlagMonitorRetryPeriod, cmd.Flags().Lookup(FlagMonitorRetryPeriod)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagMonitorRetryPeriodJitter, 15*time.Second, "monitor status retry window. defaults to 15s")
	if err := viper.BindPFlag(FlagMonitorRetryPeriodJitter, cmd.Flags().Lookup(FlagMonitorRetryPeriodJitter)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagMonitorHealthcheckPeriod, 10*time.Second, "monitor healthcheck period. defaults to 10s")
	if err := viper.BindPFlag(FlagMonitorHealthcheckPeriod, cmd.Flags().Lookup(FlagMonitorHealthcheckPeriod)); err != nil {
		return err
	}

	cmd.Flags().Duration(FlagMonitorHealthcheckPeriodJitter, 5*time.Second, "monitor healthcheck window. defaults to 5s")
	if err := viper.BindPFlag(FlagMonitorHealthcheckPeriodJitter, cmd.Flags().Lookup(FlagMonitorHealthcheckPeriodJitter)); err != nil {
		return err
	}

	cmd.Flags().String(FlagPersistentConfigBackend, "bbolt", "backend storage for persistent config. defaults to bbolt. available options: bbolt")
	if err := viper.BindPFlag(FlagPersistentConfigBackend, cmd.Flags().Lookup(FlagPersistentConfigBackend)); err != nil {
		return err
	}

	cmd.Flags().String(FlagPersistentConfigPath, "", "path to backend storage for persistent config")
	if err := viper.BindPFlag(FlagPersistentConfigPath, cmd.Flags().Lookup(FlagPersistentConfigPath)); err != nil {
		return err
	}

	if err := providerflags.AddServiceEndpointFlag(cmd, serviceHostnameOperator); err != nil {
		return err
	}

	if err := providerflags.AddServiceEndpointFlag(cmd, serviceIPOperator); err != nil {
		return err
	}

	cmd.Flags().Bool(FlagCertIssuerEnabled, false, "enable certificate issuer")
	if err := viper.BindPFlag(FlagCertIssuerEnabled, cmd.Flags().Lookup(FlagCertIssuerEnabled)); err != nil {
		return err
	}

	cmd.Flags().String(FlagCertIssuerEmail, "", "user email for certificate registar")
	if err := viper.BindPFlag(FlagCertIssuerEmail, cmd.Flags().Lookup(FlagCertIssuerEmail)); err != nil {
		return err
	}

	cmd.Flags().String(FlagCertIssuerCADirURL, lego.LEDirectoryStaging, "URL to server directory URL. Defaults to Let's Encrypt Staging")
	if err := viper.BindPFlag(FlagCertIssuerCADirURL, cmd.Flags().Lookup(FlagCertIssuerCADirURL)); err != nil {
		return err
	}

	cmd.Flags().String(FlagCertIssuerStorageDir, "", "path to store acme information. defaults to $AP_HOME/.certissuer")
	if err := viper.BindPFlag(FlagCertIssuerStorageDir, cmd.Flags().Lookup(FlagCertIssuerStorageDir)); err != nil {
		return err
	}

	cmd.Flags().Int(FlagCertIssuerHTTPChallengePort, 0, "port to listen for http-01 challenge; 0 disables http-01. any port other than 80 requires proxy forwarding")
	if err := viper.BindPFlag(FlagCertIssuerHTTPChallengePort, cmd.Flags().Lookup(FlagCertIssuerHTTPChallengePort)); err != nil {
		return err
	}

	cmd.Flags().Int(FlagCertIssuerTLSChallengePort, 0, "port to listen for tls-alpn-01 challenge; 0 disables tls-alpn-01. any port other than 443 requires proxy forwarding")
	if err := viper.BindPFlag(FlagCertIssuerTLSChallengePort, cmd.Flags().Lookup(FlagCertIssuerTLSChallengePort)); err != nil {
		return err
	}

	cmd.Flags().StringSlice(FlagCertIssuerDNSProviders, nil, "comma separated list of dns providers")
	if err := viper.BindPFlag(FlagCertIssuerDNSProviders, cmd.Flags().Lookup(FlagCertIssuerDNSProviders)); err != nil {
		return err
	}

	cmd.Flags().StringSlice(FlagCertIssuerDNSResolvers, []string{"1.1.1.1", "8.8.8.8"}, "comma separated list of dns services")
	if err := viper.BindPFlag(FlagCertIssuerDNSResolvers, cmd.Flags().Lookup(FlagCertIssuerDNSResolvers)); err != nil {
		return err
	}

	cmd.Flags().String(FlagCertIssuerKID, "", "")
	if err := viper.BindPFlag(FlagCertIssuerKID, cmd.Flags().Lookup(FlagCertIssuerKID)); err != nil {
		return err
	}

	cmd.Flags().String(FlagCertIssuerHMAC, "", "")
	if err := viper.BindPFlag(FlagCertIssuerHMAC, cmd.Flags().Lookup(FlagCertIssuerHMAC)); err != nil {
		return err
	}

	cmd.Flags().Bool(FlagMigrationsEnabled, true, "enable migrations to run automatically on startup")
	if err := viper.BindPFlag(FlagMigrationsEnabled, cmd.Flags().Lookup(FlagMigrationsEnabled)); err != nil {
		return err
	}

	cmd.Flags().String(FlagMigrationsStatePath, "", "path to migrations state file (default: $AP_HOME/migrations.json)")
	if err := viper.BindPFlag(FlagMigrationsStatePath, cmd.Flags().Lookup(FlagMigrationsStatePath)); err != nil {
		return err
	}

	cmd.Flags().String(FlagIngressMode, "ingress", "Ingress mode: 'ingress' for NGINX Ingress (default) or 'gateway-api' for Gateway API")
	if err := viper.BindPFlag(FlagIngressMode, cmd.Flags().Lookup(FlagIngressMode)); err != nil {
		return err
	}

	cmd.Flags().String(FlagGatewayName, "akash-gateway", "Gateway name when using gateway-api mode")
	if err := viper.BindPFlag(FlagGatewayName, cmd.Flags().Lookup(FlagGatewayName)); err != nil {
		return err
	}

	cmd.Flags().String(FlagGatewayNamespace, "akash-gateway", "Gateway namespace when using gateway-api mode")
	if err := viper.BindPFlag(FlagGatewayNamespace, cmd.Flags().Lookup(FlagGatewayNamespace)); err != nil {
		return err
	}

	return nil
}
