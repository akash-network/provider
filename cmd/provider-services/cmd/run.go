package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/libs/log"
	tpubsub "github.com/troian/pubsub"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/node/cmd/common"
	"github.com/akash-network/node/events"
	"github.com/akash-network/node/pubsub"
	"github.com/akash-network/node/sdl"

	cltypes "github.com/akash-network/akash-api/go/node/client/types"
	ptypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	apclient "github.com/akash-network/akash-api/go/provider/client"

	xpconfig "github.com/akash-network/node/x/provider/config"

	"github.com/akash-network/provider"
	"github.com/akash-network/provider/bidengine"
	"github.com/akash-network/provider/client"
	"github.com/akash-network/provider/cluster"
	"github.com/akash-network/provider/cluster/kube"
	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	kubehostname "github.com/akash-network/provider/cluster/kube/operators/clients/hostname"
	kubeinventory "github.com/akash-network/provider/cluster/kube/operators/clients/inventory"
	kubeip "github.com/akash-network/provider/cluster/kube/operators/clients/ip"
	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	clfromctx "github.com/akash-network/provider/cluster/types/v1beta3/fromctx"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	cmdutil "github.com/akash-network/provider/cmd/provider-services/cmd/util"
	gwgrpc "github.com/akash-network/provider/gateway/grpc"
	gwrest "github.com/akash-network/provider/gateway/rest"
	"github.com/akash-network/provider/operator/waiter"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/certissuer"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/pconfig"
	"github.com/akash-network/provider/tools/pconfig/bbolt"
)

const (
	// FlagClusterK8s informs the provider to scan and use localized kubernetes client configuration
	FlagClusterK8s = "cluster-k8s"

	// FlagGatewayListenAddress determines listening address for Manifests
	FlagGatewayListenAddress             = "gateway-listen-address"
	FlagGatewayGRPCListenAddress         = "gateway-grpc-listen-address"
	FlagBidPricingStrategy               = "bid-price-strategy"
	FlagBidPriceCPUScale                 = "bid-price-cpu-scale"
	FlagBidPriceMemoryScale              = "bid-price-memory-scale"
	FlagBidPriceStorageScale             = "bid-price-storage-scale"
	FlagBidPriceEndpointScale            = "bid-price-endpoint-scale"
	FlagBidPriceScriptPath               = "bid-price-script-path"
	FlagBidPriceScriptProcessLimit       = "bid-price-script-process-limit"
	FlagBidPriceScriptTimeout            = "bid-price-script-process-timeout"
	FlagBidDeposit                       = "bid-deposit"
	FlagClusterPublicHostname            = "cluster-public-hostname"
	FlagClusterNodePortQuantity          = "cluster-node-port-quantity"
	FlagClusterWaitReadyDuration         = "cluster-wait-ready-duration"
	FlagInventoryResourcePollPeriod      = "inventory-resource-poll-period"
	FlagInventoryResourceDebugFrequency  = "inventory-resource-debug-frequency"
	FlagDeploymentIngressStaticHosts     = "deployment-ingress-static-hosts"
	FlagDeploymentIngressDomain          = "deployment-ingress-domain"
	FlagDeploymentIngressExposeLBHosts   = "deployment-ingress-expose-lb-hosts"
	FlagDeploymentNetworkPoliciesEnabled = "deployment-network-policies-enabled"
	FlagDockerImagePullSecretsName       = "docker-image-pull-secrets-name" // nolint: gosec
	FlagOvercommitPercentMemory          = "overcommit-pct-mem"
	FlagOvercommitPercentCPU             = "overcommit-pct-cpu"
	FlagOvercommitPercentStorage         = "overcommit-pct-storage"
	FlagDeploymentBlockedHostnames       = "deployment-blocked-hostnames"
	FlagAuthPem                          = "auth-pem"
	FlagDeploymentRuntimeClass           = "deployment-runtime-class"
	FlagBidTimeout                       = "bid-timeout"
	FlagManifestTimeout                  = "manifest-timeout"
	FlagMetricsListener                  = "metrics-listener"
	FlagWithdrawalPeriod                 = "withdrawal-period"
	FlagLeaseFundsMonitorInterval        = "lease-funds-monitor-interval"
	FlagMinimumBalance                   = "minimum-balance"
	FlagProviderConfig                   = "provider-config"
	FlagCachedResultMaxAge               = "cached-result-max-age"
	FlagRPCQueryTimeout                  = "rpc-query-timeout"
	FlagBidPriceIPScale                  = "bid-price-ip-scale"
	FlagEnableIPOperator                 = "ip-operator"
	FlagTxBroadcastTimeout               = "tx-broadcast-timeout"
	FlagMonitorMaxRetries                = "monitor-max-retries"
	FlagMonitorRetryPeriod               = "monitor-retry-period"
	FlagMonitorRetryPeriodJitter         = "monitor-retry-period-jitter"
	FlagMonitorHealthcheckPeriod         = "monitor-healthcheck-period"
	FlagMonitorHealthcheckPeriodJitter   = "monitor-healthcheck-period-jitter"
	FlagPersistentConfigBackend          = "persistent-config-backend"
	FlagPersistentConfigPath             = "persistent-config-path"
	FlagGatewayTLSCert                   = "gateway-tls-cert"
	FlagGatewayTLSKey                    = "gateway-tls-key"
	FlagCertIssuerEnabled                = "cert-issuer-enabled"
	FlagCertIssuerKID                    = "cert-issuer-kid"
	FlagCertIssuerHMAC                   = "cert-issuer-hmac"
	FlagCertIssuerStorageDir             = "cert-issuer-storage-dir"
	FlagCertIssuerCADirURL               = "cert-issuer-ca-dir-url"
	FlagCertIssuerDNSProviders           = "cert-issuer-dns-providers"
	FlagCertIssuerDNSResolvers           = "cert-issuer-dns-resolvers"
	FlagCertIssuerEmail                  = "cert-issuer-email"
)

const (
	serviceIPOperator       = "ip-operator"
	serviceHostnameOperator = "hostname-operator"
)

var (
	errInvalidConfig = errors.New("Invalid configuration")
)

// RunCmd launches the Akash Provider service
func RunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "run",
		Short:        "run akash provider",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			leaseFundsMonInterval := viper.GetDuration(FlagLeaseFundsMonitorInterval)
			withdrawPeriod := viper.GetDuration(FlagWithdrawalPeriod)

			if leaseFundsMonInterval < time.Minute || leaseFundsMonInterval > 24*time.Hour {
				return errors.Errorf(`flag "%s" contains invalid value. expected >=1m<=24h`, FlagLeaseFundsMonitorInterval) // nolint: err113
			}

			if withdrawPeriod > 0 && withdrawPeriod < leaseFundsMonInterval {
				return errors.Errorf(`flag "%s" value must be > "%s"`, FlagWithdrawalPeriod, FlagLeaseFundsMonitorInterval) // nolint: err113
			}

			if viper.GetDuration(FlagMonitorRetryPeriod) < 4*time.Second {
				return errors.Errorf(`flag "%s" value must be > "%s"`, FlagMonitorRetryPeriod, 4*time.Second) // nolint: err113
			}

			if viper.GetDuration(FlagMonitorHealthcheckPeriod) < 4*time.Second {
				return errors.Errorf(`flag "%s" value must be > "%s"`, FlagMonitorHealthcheckPeriod, 4*time.Second) // nolint: err113
			}

			pconfigBackend := viper.GetString(FlagPersistentConfigBackend)
			pconfigPath := viper.GetString(FlagPersistentConfigPath)

			cctx, err := sdkclient.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			var pstorage pconfig.Storage

			switch pconfigBackend {
			case "bbolt":
				if pconfigPath == "" {
					pconfigPath = fmt.Sprintf("%s/pconfig.db", cctx.HomeDir)
				}

				var err error
				pstorage, err = bbolt.NewBBolt(pconfigPath)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupport persistent-config backend \"%s\"", pconfigBackend)
			}

			if err := clientcommon.SetKubeConfigToCmd(cmd); err != nil {
				return err
			}

			pctx := cmd.Context()

			group, ctx := errgroup.WithContext(pctx)
			cmd.SetContext(ctx)

			kubecfg := fromctx.MustKubeConfigFromCtx(pctx)

			kc, err := kubernetes.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			ac, err := akashclientset.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			logger := cmdutil.OpenLogger().With("cmp", "provider")

			bus := tpubsub.New(pctx, 1000)

			if viper.GetBool(FlagCertIssuerEnabled) {
				var certIssuer certissuer.CertIssuer

				storageDir := viper.GetString(FlagCertIssuerStorageDir)
				if storageDir == "" {
					storageDir = fmt.Sprintf("%s/.certissuer", cctx.HomeDir)
				}

				ciCfg := certissuer.Config{
					Bus:          bus,
					Owner:        cctx.FromAddress,
					KID:          viper.GetString(FlagCertIssuerKID),
					HMAC:         viper.GetString(FlagCertIssuerHMAC),
					StorageDir:   storageDir,
					CADirURL:     viper.GetString(FlagCertIssuerCADirURL),
					Email:        viper.GetString(FlagCertIssuerEmail),
					DNSProviders: viper.GetStringSlice(FlagCertIssuerDNSProviders),
					DNSResolvers: viper.GetStringSlice(FlagCertIssuerDNSResolvers),
					Domains: []string{
						viper.GetString(FlagDeploymentIngressDomain),
					},
				}

				if err = ciCfg.Validate(); err != nil {
					return err
				}

				certIssuer, err = certissuer.NewLego(ctx, logger, ciCfg)
				if err != nil {
					return err
				}

				go func() {
					<-ctx.Done()
					_ = certIssuer.Close()
				}()

				fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyCertIssuer, certIssuer)
			}

			startupch := make(chan struct{}, 1)

			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyStartupCh, (chan<- struct{})(startupch))
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyErrGroup, group)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyLogc, logger)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyKubeClientSet, kc)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyAkashClientSet, ac)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyPersistentConfig, pstorage)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyPubSub, bus)

			pctx, pcancel := context.WithCancel(context.Background())
			go func() {
				defer pcancel()

				select {
				case <-ctx.Done():
					return
				case <-startupch:
				}

				_ = group.Wait()
			}()

			go func() {
				<-ctx.Done()

				_ = pstorage.Close()
			}()

			return nil
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			return common.RunForeverWithContext(cmd.Context(), func(ctx context.Context) error {
				return doRunCmd(ctx, cmd, args)
			})
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	if err := addRunFlags(cmd); err != nil {
		panic(err)
	}

	return cmd
}

const (
	bidPricingStrategyScale       = "scale"
	bidPricingStrategyRandomRange = "randomRange"
	bidPricingStrategyShellScript = "shellScript"
)

var allowedBidPricingStrategies = [...]string{
	bidPricingStrategyScale,
	bidPricingStrategyRandomRange,
	bidPricingStrategyShellScript,
}

var errNoSuchBidPricingStrategy = fmt.Errorf("no such bid pricing strategy. Allowed: %v", allowedBidPricingStrategies)
var errInvalidValueForBidPrice = errors.New("not a valid bid price")
var errBidPriceNegative = errors.New("bid price cannot be a negative number")

func strToBidPriceScale(val string) (decimal.Decimal, error) {
	v, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: %s", errInvalidValueForBidPrice, val)
	}

	if v.IsNegative() {
		return decimal.Decimal{}, errBidPriceNegative
	}

	return v, nil
}

func createBidPricingStrategy(strategy string) (bidengine.BidPricingStrategy, error) {
	if strategy == bidPricingStrategyScale {
		cpuScale, err := strToBidPriceScale(viper.GetString(FlagBidPriceCPUScale))
		if err != nil {
			return nil, err
		}
		memoryScale, err := strToBidPriceScale(viper.GetString(FlagBidPriceMemoryScale))
		if err != nil {
			return nil, err
		}
		storageScale := make(bidengine.Storage)

		storageScales := strings.Split(viper.GetString(FlagBidPriceStorageScale), ",")
		for _, scalePair := range storageScales {
			vals := strings.Split(scalePair, "=")

			name := sdl.StorageEphemeral
			scaleVal := vals[0]

			if len(vals) == 2 {
				name = vals[0]
				scaleVal = vals[1]
			}

			storageScale[name], err = strToBidPriceScale(scaleVal)
			if err != nil {
				return nil, err
			}
		}

		endpointScale, err := strToBidPriceScale(viper.GetString(FlagBidPriceEndpointScale))
		if err != nil {
			return nil, err
		}

		ipScale, err := strToBidPriceScale(viper.GetString(FlagBidPriceIPScale))
		if err != nil {
			return nil, err
		}

		return bidengine.MakeScalePricing(cpuScale, memoryScale, storageScale, endpointScale, ipScale)
	}

	if strategy == bidPricingStrategyRandomRange {
		return bidengine.MakeRandomRangePricing()
	}

	if strategy == bidPricingStrategyShellScript {
		scriptPath := viper.GetString(FlagBidPriceScriptPath)
		processLimit := viper.GetUint(FlagBidPriceScriptProcessLimit)
		runtimeLimit := viper.GetDuration(FlagBidPriceScriptTimeout)
		return bidengine.MakeShellScriptPricing(scriptPath, processLimit, runtimeLimit)
	}

	return nil, errNoSuchBidPricingStrategy
}

// doRunCmd initializes all the Provider functionality, hangs, and awaits shutdown signals.
func doRunCmd(ctx context.Context, cmd *cobra.Command, _ []string) error {
	clusterPublicHostname := viper.GetString(FlagClusterPublicHostname)
	// TODO - validate that clusterPublicHostname is a valid hostname
	nodePortQuantity := viper.GetUint(FlagClusterNodePortQuantity)
	clusterWaitReadyDuration := viper.GetDuration(FlagClusterWaitReadyDuration)
	inventoryResourcePollPeriod := viper.GetDuration(FlagInventoryResourcePollPeriod)
	inventoryResourceDebugFreq := viper.GetUint(FlagInventoryResourceDebugFrequency)
	deploymentIngressStaticHosts := viper.GetBool(FlagDeploymentIngressStaticHosts)
	deploymentIngressDomain := viper.GetString(FlagDeploymentIngressDomain)
	deploymentNetworkPoliciesEnabled := viper.GetBool(FlagDeploymentNetworkPoliciesEnabled)
	dockerImagePullSecretsName := viper.GetString(FlagDockerImagePullSecretsName)
	strategy := viper.GetString(FlagBidPricingStrategy)
	deploymentIngressExposeLBHosts := viper.GetBool(FlagDeploymentIngressExposeLBHosts)
	overcommitPercentStorage := 1.0 + float64(viper.GetUint64(FlagOvercommitPercentStorage)/100.0)
	overcommitPercentCPU := 1.0 + float64(viper.GetUint64(FlagOvercommitPercentCPU)/100.0)
	// no GPU overcommit
	overcommitPercentGPU := 1.0
	overcommitPercentMemory := 1.0 + float64(viper.GetUint64(FlagOvercommitPercentMemory)/100.0)
	blockedHostnames := viper.GetStringSlice(FlagDeploymentBlockedHostnames)
	deploymentRuntimeClass := viper.GetString(FlagDeploymentRuntimeClass)
	bidTimeout := viper.GetDuration(FlagBidTimeout)
	manifestTimeout := viper.GetDuration(FlagManifestTimeout)
	metricsListener := viper.GetString(FlagMetricsListener)
	providerConfig := viper.GetString(FlagProviderConfig)
	cachedResultMaxAge := viper.GetDuration(FlagCachedResultMaxAge)
	rpcQueryTimeout := viper.GetDuration(FlagRPCQueryTimeout)
	enableIPOperator := viper.GetBool(FlagEnableIPOperator)
	monitorMaxRetries := viper.GetUint(FlagMonitorMaxRetries)
	monitorRetryPeriod := viper.GetDuration(FlagMonitorRetryPeriod)
	monitorRetryPeriodJitter := viper.GetDuration(FlagMonitorRetryPeriodJitter)
	monitorHealthcheckPeriod := viper.GetDuration(FlagMonitorHealthcheckPeriod)
	monitorHealthcheckPeriodJitter := viper.GetDuration(FlagMonitorHealthcheckPeriodJitter)

	pricing, err := createBidPricingStrategy(strategy)
	if err != nil {
		return err
	}

	logger := fromctx.LogcFromCtx(cmd.Context())

	var metricsRouter http.Handler
	if len(metricsListener) != 0 {
		metricsRouter = makeMetricsRouter()
	}

	group := fromctx.MustErrGroupFromCtx(ctx)

	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	cctx = cctx.WithSkipConfirmation(true)

	opts, err := cltypes.ClientOptionsFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	cl, err := client.DiscoverClient(ctx, cctx, opts...)
	if err != nil {
		return err
	}

	gwaddr := viper.GetString(FlagGatewayListenAddress)
	grpcaddr := viper.GetString(FlagGatewayGRPCListenAddress)

	res, err := cl.Query().Provider(
		cmd.Context(),
		&ptypes.QueryProviderRequest{Owner: cctx.FromAddress.String()},
	)
	if err != nil {
		return err
	}

	pinfo := &res.Provider

	// k8s client creation
	kubeSettings := builder.NewDefaultSettings()
	kubeSettings.DeploymentIngressDomain = deploymentIngressDomain
	kubeSettings.DeploymentIngressExposeLBHosts = deploymentIngressExposeLBHosts
	kubeSettings.DeploymentIngressStaticHosts = deploymentIngressStaticHosts
	kubeSettings.NetworkPoliciesEnabled = deploymentNetworkPoliciesEnabled
	kubeSettings.ClusterPublicHostname = clusterPublicHostname
	kubeSettings.CPUCommitLevel = overcommitPercentCPU
	kubeSettings.GPUCommitLevel = overcommitPercentGPU
	kubeSettings.MemoryCommitLevel = overcommitPercentMemory
	kubeSettings.StorageCommitLevel = overcommitPercentStorage
	kubeSettings.DeploymentRuntimeClass = deploymentRuntimeClass
	kubeSettings.DockerImagePullSecretsName = strings.TrimSpace(dockerImagePullSecretsName)

	if err := builder.ValidateSettings(kubeSettings); err != nil {
		return err
	}

	clusterSettings := map[interface{}]interface{}{
		builder.SettingsKey: kubeSettings,
	}

	cclient, err := createClusterClient(ctx, logger, cmd)
	if err != nil {
		return err
	}

	statusResult, err := cctx.Client.Status(ctx)
	if err != nil {
		return err
	}
	currentBlockHeight := statusResult.SyncInfo.LatestBlockHeight
	sessionMgr := session.New(logger, cl, pinfo, currentBlockHeight)

	if err := cctx.Client.Start(); err != nil {
		return err
	}

	bus := pubsub.NewBus()
	defer bus.Close()

	// Provider service creation
	config := provider.NewDefaultConfig()
	config.ClusterWaitReadyDuration = clusterWaitReadyDuration
	config.ClusterPublicHostname = clusterPublicHostname
	config.ClusterExternalPortQuantity = nodePortQuantity
	config.InventoryResourceDebugFrequency = inventoryResourceDebugFreq
	config.InventoryResourcePollPeriod = inventoryResourcePollPeriod
	config.CPUCommitLevel = overcommitPercentCPU
	config.MemoryCommitLevel = overcommitPercentMemory
	config.StorageCommitLevel = overcommitPercentStorage
	config.BlockedHostnames = blockedHostnames
	config.DeploymentIngressStaticHosts = deploymentIngressStaticHosts
	config.DeploymentIngressDomain = deploymentIngressDomain
	config.BidTimeout = bidTimeout
	config.ManifestTimeout = manifestTimeout
	config.MonitorMaxRetries = monitorMaxRetries
	config.MonitorRetryPeriod = monitorRetryPeriod
	config.MonitorRetryPeriodJitter = monitorRetryPeriodJitter
	config.MonitorHealthcheckPeriod = monitorHealthcheckPeriod
	config.MonitorHealthcheckPeriodJitter = monitorHealthcheckPeriodJitter

	if len(providerConfig) != 0 {
		pConf, err := xpconfig.ReadConfigPath(providerConfig)
		if err != nil {
			return err
		}
		config.Attributes = pConf.Attributes
		if err = config.Attributes.Validate(); err != nil {
			return err
		}
	}

	config.BalanceCheckerCfg = provider.BalanceCheckerConfig{
		WithdrawalPeriod:        viper.GetDuration(FlagWithdrawalPeriod),
		LeaseFundsCheckInterval: viper.GetDuration(FlagLeaseFundsMonitorInterval),
	}

	config.BidPricingStrategy = pricing
	config.ClusterSettings = clusterSettings

	bidDeposit, err := sdk.ParseCoinNormalized(viper.GetString(FlagBidDeposit))
	if err != nil {
		return err
	}
	config.BidDeposit = bidDeposit
	config.RPCQueryTimeout = rpcQueryTimeout
	config.CachedResultMaxAge = cachedResultMaxAge

	// This value can be nil, the operator is not mandatory
	var ipOperatorClient cip.Client
	if enableIPOperator {
		endpoint, err := providerflags.GetServiceEndpointFlagValue(logger, serviceIPOperator)
		if err != nil {
			return err
		}
		ipOperatorClient, err = kubeip.NewClient(ctx, logger, endpoint)
		if err != nil {
			return err
		}
	}

	endpoint, err := providerflags.GetServiceEndpointFlagValue(logger, serviceHostnameOperator)
	if err != nil {
		return err
	}
	hostnameOperatorClient, err := kubehostname.NewClient(ctx, logger, endpoint)
	if err != nil {
		return err
	}

	ctx = context.WithValue(ctx, clfromctx.CtxKeyClientHostname, hostnameOperatorClient)

	inventory, err := kubeinventory.NewClient(ctx)
	if err != nil {
		return err
	}

	ctx = context.WithValue(ctx, clfromctx.CtxKeyClientInventory, inventory)

	waitClients := make([]waiter.Waitable, 0)
	waitClients = append(waitClients, hostnameOperatorClient)

	if ipOperatorClient != nil {
		waitClients = append(waitClients, ipOperatorClient)
		ctx = context.WithValue(ctx, clfromctx.CtxKeyClientIP, ipOperatorClient)
	}

	operatorWaiter := waiter.NewOperatorWaiter(ctx, logger, waitClients...)

	service, err := provider.NewService(ctx, cctx, cctx.FromAddress, sessionMgr, bus, cclient, operatorWaiter, config)
	if err != nil {
		return err
	}

	ctx = context.WithValue(ctx, fromctx.CtxKeyErrGroup, group)

	var acQuerierOpts []accountQuerierOption

	if _, err := fromctx.CertIssuerFromCtx(ctx); err == nil {
		acQuerierOpts = append(acQuerierOpts, WithTLSDomainWatch(clusterPublicHostname))
	} else if certFile := viper.GetString(FlagGatewayTLSCert); certFile != "" {
		keyFile := viper.GetString(FlagGatewayTLSKey)
		acQuerierOpts = append(acQuerierOpts, WithTLSCert(certFile, keyFile))
	}

	accQuerier, err := newAccountQuerier(ctx, cctx, logger, bus, cl, acQuerierOpts...)
	if err != nil {
		return err
	}

	gwRest, err := gwrest.NewServer(
		ctx,
		logger,
		service,
		accQuerier,
		clusterPublicHostname,
		gwaddr,
		cctx.FromAddress,
		clusterSettings,
	)
	if err != nil {
		return err
	}

	err = gwgrpc.NewServer(ctx, grpcaddr, accQuerier, service)
	if err != nil {
		return err
	}

	evtSvc, err := events.NewEvents(ctx, cctx.Client, "provider-cli", bus)
	if err != nil {
		return err
	}

	group.Go(func() error {
		<-service.Done()
		return nil
	})

	group.Go(func() error {
		// certificates are supplied via tls.Config
		return gwRest.ListenAndServeTLS("", "")
	})

	group.Go(func() error {
		<-ctx.Done()
		evtSvc.Shutdown()
		return gwRest.Close()
	})

	if metricsRouter != nil {
		group.Go(func() error {
			// nolint: gosec
			srv := http.Server{Addr: metricsListener, Handler: metricsRouter}
			go func() {
				<-ctx.Done()
				_ = srv.Close()
			}()
			err := srv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	}

	fromctx.MustStartupChFromCtx(ctx) <- struct{}{}

	err = group.Wait()

	if ipOperatorClient != nil {
		ipOperatorClient.Stop()
	}

	hostnameOperatorClient.Stop()
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func createClusterClient(ctx context.Context, log log.Logger, _ *cobra.Command) (cluster.Client, error) {
	if !viper.GetBool(FlagClusterK8s) {
		// Condition that there is no Kubernetes API to work with.
		return cluster.NullClient(), nil
	}
	ns := viper.GetString(providerflags.FlagK8sManifestNS)
	if ns == "" {
		return nil, fmt.Errorf("%w: --%s required", errInvalidConfig, providerflags.FlagK8sManifestNS)
	}

	return kube.NewClient(ctx, log, ns)
}

func showErrorToUser(err error) error {
	// If the error has a complete message associated with it then show it
	terr := &apclient.ClientResponseError{}

	if errors.As(err, terr) && len(terr.Message) != 0 {
		_, _ = fmt.Fprintf(os.Stderr, "provider error messsage:\n%v\n", terr.Message)
		err = terr
	}

	return err
}
