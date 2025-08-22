//go:build e2e

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/homedir"
	atypes "pkg.akt.dev/go/node/audit/v1"
	"pkg.akt.dev/go/sdkutil"
	"pkg.akt.dev/go/util/events"
	"pkg.akt.dev/go/util/pubsub"
	"pkg.akt.dev/node/app"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"pkg.akt.dev/go/cli"
	clitestutil "pkg.akt.dev/go/cli/testutil"
	arpcclient "pkg.akt.dev/go/node/client"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	ttypes "pkg.akt.dev/go/node/take/v1"
	"pkg.akt.dev/go/testutil"
	nodetestutil "pkg.akt.dev/node/testutil"
	testnet "pkg.akt.dev/node/testutil/network"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	pcmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	operatorcommon "github.com/akash-network/provider/operator/common"
	akashclient "github.com/akash-network/provider/pkg/client/clientset/versioned"
	ptestutil "github.com/akash-network/provider/testutil/provider"
	"github.com/akash-network/provider/tools/fromctx"
)

// IntegrationTestSuite wraps testing components
type IntegrationTestSuite struct {
	suite.Suite

	cctx         sdkclient.Context
	cfg          testnet.Config
	network      *testnet.Network
	validator    *testnet.Validator
	keyProvider  *keyring.Record
	keyTenant    *keyring.Record
	addrProvider sdk.AccAddress
	addrTenant   sdk.AccAddress

	group     *errgroup.Group
	ctx       context.Context
	ctxCancel context.CancelFunc

	ev                   events.Service
	bus                  pubsub.Bus
	sub                  pubsub.Subscriber
	deploymentMinDeposit sdk.DecCoin

	appHost string
	appPort string

	ipMarketplace bool
}

const (
	defaultGasPrice         = "0.03uakt"
	defaultGasAdjustment    = "1.4"
	axlUSDCDenom            = "ibc/12C6A0C374171B595A0A9E18B83FA09D295FB1F2D8C6DAA3AC28683471752D84"
	axlUSCDMinDepositAmount = 5000000
)

var (
	axlUSCDMinDeposit        = fmt.Sprintf("%d%s", axlUSCDMinDepositAmount, axlUSDCDenom)
	deploymentAxlUSDCDeposit = fmt.Sprintf("--deposit=%s", axlUSCDMinDeposit)
)

var cliFlags = cli.TestFlags().
	WithSkipConfirm().
	WithBroadcastModeBlock().
	WithGasAuto()

func (s *IntegrationTestSuite) SetupSuite() {
	s.appHost, s.appPort = appEnv(s.T())

	encCfg := sdkutil.MakeEncodingConfig()
	app.ModuleBasics().RegisterInterfaces(encCfg.InterfaceRegistry)

	// Create a network for test
	cfg := testnet.DefaultConfig(nodetestutil.NewTestNetworkFixture, testnet.WithInterceptState(func(cdc codec.Codec, s string, istate json.RawMessage) json.RawMessage {
		var res json.RawMessage

		switch s {
		case "take":
			state := &ttypes.GenesisState{}
			cdc.MustUnmarshalJSON(istate, state)

			state.Params.DenomTakeRates = append(state.Params.DenomTakeRates, ttypes.DenomTakeRate{
				Denom: axlUSDCDenom,
				Rate:  20,
			})

			res = cdc.MustMarshalJSON(state)
		case "deployment":
			state := &dvbeta.GenesisState{}
			cdc.MustUnmarshalJSON(istate, state)
			state.Params.MinDeposits = append(state.Params.MinDeposits, sdk.NewInt64Coin(axlUSDCDenom, 5000000))

			res = cdc.MustMarshalJSON(state)
		}

		return res
	}))

	cfg.NumValidators = 1
	cfg.MinGasPrices = fmt.Sprintf("0%s", testutil.CoinDenom)
	s.cfg = cfg

	ctx := context.WithValue(context.Background(), cli.ContextTypeAddressCodec, encCfg.SigningOptions.AddressCodec)
	s.ctx = context.WithValue(ctx, cli.ContextTypeValidatorCodec, encCfg.SigningOptions.ValidatorAddressCodec)
	s.ctx = context.WithValue(ctx, fromctx.CtxKeyLogc, log.NewLogger(os.Stdout))

	s.network = testnet.New(s.T(), cfg)

	client, err := arpcclient.NewClient(s.ctx, s.network.Validators[0].RPCAddress)
	require.NoError(s.T(), err)

	s.cctx = s.network.Validators[0].ClientCtx.WithClient(client)

	cctx := s.cctx

	kb := cctx.Keyring
	_, _, err = kb.NewMnemonic("keyBar", keyring.English, sdk.FullFundraiserPath, "", hd.Secp256k1)
	s.Require().NoError(err)
	_, _, err = kb.NewMnemonic("keyFoo", keyring.English, sdk.FullFundraiserPath, "", hd.Secp256k1)
	s.Require().NoError(err)

	// Wait for the network to start
	_, err = s.network.WaitForHeight(1)
	s.Require().NoError(err)

	s.bus = pubsub.NewBus()
	s.sub, err = s.bus.Subscribe()
	s.Require().NoError(err)

	s.validator = s.network.Validators[0]

	s.ev, err = events.NewEvents(s.ctx, s.validator.ClientCtx.Client, "e2e-tests", s.bus)
	s.Require().NoError(err)

	// Send coins value
	sendTokens := sdk.Coins{
		sdk.NewCoin(s.cfg.BondDenom, mvbeta.DefaultBidMinDeposit.Amount.MulRaw(4)),
		sdk.NewCoin(axlUSDCDenom, sdkmath.NewInt(axlUSCDMinDepositAmount*4)),
	}

	// Setup a Provider key
	s.keyProvider, err = s.validator.ClientCtx.Keyring.Key("keyFoo")
	s.Require().NoError(err)

	s.addrProvider, err = s.keyProvider.GetAddress()
	s.Require().NoError(err)

	s.Require().NoError(s.network.WaitForNextBlock())

	// give provider some coins
	res, err := clitestutil.ExecSend(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(
				s.addrProvider.String(),
				sendTokens.String()).
			WithFrom(s.validator.Address.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Set up second tenant key
	s.keyTenant, err = s.validator.ClientCtx.Keyring.Key("keyBar")
	s.Require().NoError(err)

	s.addrTenant, err = s.keyTenant.GetAddress()
	s.Require().NoError(err)

	// give the tenant some coins too
	res, err = clitestutil.ExecSend(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(
				s.addrTenant.String(),
				sendTokens.String()).
			WithFrom(s.validator.Address.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	numPorts := 4
	if s.ipMarketplace {
		numPorts += 2
	}

	ports, err := testnet.GetFreePorts(numPorts)
	s.Require().NoError(err)

	// address for provider to listen on
	provHost := fmt.Sprintf("localhost:%d", ports[0])
	provURL := url.URL{
		Host:   provHost,
		Scheme: "https",
	}

	provFileStr := fmt.Sprintf(providerTemplate, provURL.String())
	tmpFile, err := os.CreateTemp(s.network.BaseDir, "provider.yaml")
	require.NoError(s.T(), err)

	_, err = tmpFile.WriteString(provFileStr)
	require.NoError(s.T(), err)

	defer func() {
		err := tmpFile.Close()
		require.NoError(s.T(), err)
	}()

	fstat, err := tmpFile.Stat()
	require.NoError(s.T(), err)

	// create Provider blockchain declaration
	_, err = clitestutil.ExecTxCreateProvider(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(fmt.Sprintf("%s/%s", s.network.BaseDir, fstat.Name())).
			WithFrom(s.addrProvider.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Generate provider's certificate
	_, err = clitestutil.TxGenerateServerExec(
		s.ctx,
		cctx,
		cli.TestFlags().
			With("localhost").
			WithFrom(s.addrProvider.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)

	// Publish provider's certificate
	_, err = clitestutil.TxPublishServerExec(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithFrom(s.addrProvider.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Generate tenant's certificate
	_, err = clitestutil.TxGenerateClientExec(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithFrom(s.addrTenant.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)

	// Publish tenant's certificate
	_, err = clitestutil.TxPublishClientExec(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithFrom(s.addrTenant.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	pemSrc := fmt.Sprintf("%s/%s.pem", s.cctx.HomeDir, s.addrProvider.String())
	pemDst := fmt.Sprintf("%s/%s.pem", strings.Replace(s.cctx.HomeDir, "simd", "simcli", 1), s.addrProvider.String())
	input, err := os.ReadFile(pemSrc)
	s.Require().NoError(err)

	err = os.WriteFile(pemDst, input, 0400)
	s.Require().NoError(err)

	pemSrc = fmt.Sprintf("%s/%s.pem", s.cctx.HomeDir, s.addrTenant.String())
	pemDst = fmt.Sprintf("%s/%s.pem", strings.Replace(s.cctx.HomeDir, "simd", "simcli", 1), s.addrTenant.String())
	input, err = os.ReadFile(pemSrc)
	s.Require().NoError(err)

	err = os.WriteFile(pemDst, input, 0400)
	s.Require().NoError(err)

	// test query providers
	resp, err := clitestutil.ExecQueryProviders(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	out := &ptypes.QueryProvidersResponse{}
	err = s.cctx.Codec.UnmarshalJSON(resp.Bytes(), out)
	s.Require().NoError(err)
	s.Require().Len(out.Providers, 1, "Provider Creation Failed")
	providers := out.Providers
	s.Require().Equal(s.addrProvider.String(), providers[0].Owner)

	// test query provider
	createdProvider := providers[0]
	resp, err = clitestutil.ExecQueryProvider(s.ctx, cctx, cli.TestFlags().With(createdProvider.Owner).WithOutputJSON()...)
	s.Require().NoError(err)

	var provider ptypes.Provider
	err = s.cctx.Codec.UnmarshalJSON(resp.Bytes(), &provider)
	s.Require().NoError(err)
	s.Require().Equal(createdProvider, provider)

	// Change the akash home directory for CLI to access the test keyring
	cliHome := strings.Replace(s.cctx.HomeDir, "simd", "simcli", 1)

	// A context object to tie the lifetime of the provider & hostname operator to
	ctx, cancel := context.WithCancel(context.Background())
	s.ctxCancel = cancel

	group, ctx := errgroup.WithContext(ctx)
	ctx = context.WithValue(ctx, fromctx.CtxKeyErrGroup, group)

	kubecfg, err := clientcommon.OpenKubeConfig(providerflags.KubeConfigDefaultPath, testutil.Logger(s.T()))
	require.NoError(s.T(), err)
	require.NotNil(s.T(), kubecfg)

	kubecfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	kc, err := kubernetes.NewForConfig(kubecfg)
	require.NoError(s.T(), err)

	ac, err := akashclient.NewForConfig(kubecfg)
	require.NoError(s.T(), err)

	startupch := make(chan struct{}, 10)
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeConfig, kubecfg)
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kc)
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, ac)
	ctx = context.WithValue(ctx, fromctx.CtxKeyStartupCh, (chan<- struct{})(startupch))

	s.ctx = ctx
	s.group = group

	hostnameOperatorPort := ports[1]
	hostnameOperatorHost := fmt.Sprintf("localhost:%d", hostnameOperatorPort)

	var ipOperatorHost string
	var ipOperatorPort int

	// all commands use Viper which is meant for use by a single goroutine only
	// so wait for the provider to start before running the hostname operator
	pArgs := cli.TestFlags().
		WithHome(cliHome).
		WithFrom(s.addrProvider.String()).
		WithGasAuto().
		WithFlag(pcmd.FlagClusterK8s, true).
		WithFlag(pcmd.FlagGatewayListenAddress, provURL.Host).
		WithFlag(pcmd.FlagClusterPublicHostname, ptestutil.TestClusterPublicHostname).
		WithFlag(pcmd.FlagClusterNodePortQuantity, ptestutil.TestClusterNodePortQuantity).
		WithFlag(pcmd.FlagPersistentConfigBackend, "memory").
		WithFlag("deployment-runtime-class", "none").
		WithFlag("hostname-operator-endpoint", hostnameOperatorHost).
		WithFlag(pcmd.FlagBidPricingStrategy, "randomRange")

	if s.ipMarketplace {
		ipOperatorPort = ports[2]
		ipOperatorHost = fmt.Sprintf("localhost:%d", ipOperatorPort)

		pArgs = pArgs.
			WithFlag("ip-operator-endpoint", ipOperatorHost).
			WithFlag("ip-operator", true)
	}

	dialer := net.Dialer{
		Timeout: time.Second * 3,
	}

	// --- Start hostname operator
	s.group.Go(func() error {
		s.T().Logf("starting hostname operator for test on %s", hostnameOperatorHost)

		_, err := ptestutil.RunLocalOperator(
			s.ctx,
			cctx,
			cli.TestFlags().
				With("hostname").
				WithFlag(operatorcommon.FlagRESTAddress, "127.0.0.1").
				WithFlag(operatorcommon.FlagRESTPort, hostnameOperatorPort)...,
		)
		s.Assert().NoError(err)
		return err
	})

	s.T().Log("waiting for hostname operator")
	waitForTCPSocket(s.ctx, dialer, hostnameOperatorHost, s.T())

	if s.ipMarketplace {
		s.group.Go(func() error {
			s.T().Logf("starting ip operator for test on %v", ipOperatorHost)
			_, err := ptestutil.RunLocalOperator(
				s.ctx,
				cctx,
				cli.TestFlags().
					With("ip").
					WithFlag(operatorcommon.FlagRESTAddress, "127.0.0.1").
					WithFlag(operatorcommon.FlagRESTPort, ipOperatorPort).
					WithProvider(s.addrProvider.String())...,
			)
			s.Assert().NoError(err)
			return err
		})

		s.T().Log("waiting for IP operator")
		waitForTCPSocket(s.ctx, dialer, ipOperatorHost, s.T())
	}

	s.group.Go(func() error {
		_, err := ptestutil.RunLocalProvider(
			ctx,
			cctx,
			pArgs...,
		)

		if err != nil {
			s.T().Logf("provider stopped with error: %v", err)
		}

		return err
	})

	// Wait for the provider gateway to be up and running
	s.T().Log("waiting for provider gateway")
	waitForTCPSocket(s.ctx, dialer, provHost, s.T())

	s.Require().NoError(s.network.WaitForNextBlock())
}

func waitForTCPSocket(ctx context.Context, dialer net.Dialer, host string, t *testing.T) {
	// Wait no more than 30 seconds for the socket to be listening
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		if err := ctx.Err(); err != nil {
			t.Fatalf("timed out trying to connect to host %q", host)
		}

		// Just test for TCP socket accepting connections, not for an actual functional server
		conn, err := dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				t.Fatalf("timed out trying to connect to host %q", host)
			}

			_, ok := err.(net.Error)
			require.Truef(t, ok, "error should be net.Error not [%T] %v", err, err)

			//var eerr net.Error
			//require.ErrorAs(t, err, eerr, "error should be net.Error not [%T]", err)
			time.Sleep(333 * time.Millisecond)
			continue
		}
		_ = conn.Close()
		return
	}
}

func (s *IntegrationTestSuite) TearDownTest() {
	s.T().Log("Cleaning up after E2E test")
	s.closeDeployments()
}

func (s *IntegrationTestSuite) closeDeployments() int {
	cctx := s.cctx

	resp, err := clitestutil.ExecQueryDeployments(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	deployResp := &dvbeta.QueryDeploymentsResponse{}
	err = s.cctx.Codec.UnmarshalJSON(resp.Bytes(), deployResp)
	s.Require().NoError(err)

	deployments := deployResp.Deployments

	s.T().Logf("Cleaning up %d deployments", len(deployments))
	for _, createdDep := range deployments {
		if createdDep.Deployment.State != dtypes.DeploymentActive {
			continue
		}
		// teardown lease
		res, err := clitestutil.ExecDeploymentClose(
			s.ctx,
			cctx,
			cli.TestFlags().
				WithFrom(s.addrTenant.String()).
				WithGasAuto().
				WithOwner(createdDep.Groups[0].ID.Owner).
				WithDSeq(createdDep.Deployment.ID.DSeq)...,
		)
		s.Require().NoError(err)
		s.Require().NoError(s.waitForBlocksCommitted(1))
		clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())
	}

	return len(deployments)
}

func (s *IntegrationTestSuite) TearDownSuite() {
	s.T().Log("Cleaning up after E2E suite")
	n := s.closeDeployments()
	s.Require().NoError(s.waitForBlocksCommitted(1))

	cctx := s.cctx

	// test query deployments with state filter closed
	resp, err := clitestutil.ExecQueryDeployments(
		s.ctx,
		cctx,
		cli.TestFlags().WithOutputJSON().
			WithState("closed")...,
	)
	s.Require().NoError(err)

	qResp := &dvbeta.QueryDeploymentsResponse{}
	err = s.cctx.Codec.UnmarshalJSON(resp.Bytes(), qResp)
	s.Require().NoError(err)
	s.Require().True(len(qResp.Deployments) == n, "Deployment Close Failed")

	s.ev.Shutdown()
	s.bus.Close()

	s.network.Cleanup()

	// remove all entries of the provider host CRD
	cfgPath := path.Join(homedir.HomeDir(), ".kube", "config")

	restConfig, err := clientcmd.BuildConfigFromFlags("", cfgPath)
	s.Require().NoError(err)

	ac, err := akashclient.NewForConfig(restConfig)
	s.Require().NoError(err)
	const ns = "lease"
	propagation := metav1.DeletePropagationForeground
	err = ac.AkashV2beta2().ProviderHosts(ns).DeleteCollection(s.ctx, metav1.DeleteOptions{
		TypeMeta:           metav1.TypeMeta{},
		GracePeriodSeconds: nil,
		Preconditions:      nil,
		OrphanDependents:   nil,
		PropagationPolicy:  &propagation,
		DryRun:             nil,
	}, metav1.ListOptions{

		LabelSelector:        `akash.network=true`,
		FieldSelector:        "",
		Watch:                false,
		AllowWatchBookmarks:  false,
		ResourceVersion:      "",
		ResourceVersionMatch: "",
		TimeoutSeconds:       nil,
		Limit:                0,
		Continue:             "",
	},
	)
	s.Require().NoError(err)

	time.Sleep(3 * time.Second) // Make sure hostname operator has time to delete ingress

	s.ctxCancel() // Stop context that provider & hostname operator are tied to

	_ = s.group.Wait()
}

func newestLease(leases []mvbeta.QueryLeaseResponse) mtypes.Lease {
	result := mtypes.Lease{}
	assigned := false

	for _, lease := range leases {
		if !assigned {
			result = lease.Lease
			assigned = true
		} else if result.GetID().DSeq < lease.Lease.GetID().DSeq {
			result = lease.Lease
		}
	}

	return result
}

func getKubernetesIP() string {
	return os.Getenv("KUBE_NODE_IP")
}

func TestIntegrationTestSuite(t *testing.T) {
	integrationTestOnly(t)

	suite.Run(t, new(E2EContainerToContainer))
	suite.Run(t, new(E2EAppNodePort))
	suite.Run(t, new(E2EDeploymentUpdate))
	suite.Run(t, new(E2EApp))
	suite.Run(t, new(E2EPersistentStorageDefault))
	suite.Run(t, new(E2EPersistentStorageDefault))
	suite.Run(t, new(E2EPersistentStorageBeta2))
	suite.Run(t, new(E2EPersistentStorageDeploymentUpdate))
	suite.Run(t, new(E2EStorageClassRam))
	suite.Run(t, new(E2EMigrateHostname))
	suite.Run(t, new(E2ECustomCurrency))
	suite.Run(t, &E2EIPAddress{IntegrationTestSuite{ipMarketplace: true}})
}

// TestQueryApp enables rapid testing of the querying functionality locally
// Not for CI tests.
func TestQueryApp(t *testing.T) {
	integrationTestOnly(t)
	host, appPort := appEnv(t)

	appURL := fmt.Sprintf("http://%s:%s/", host, appPort)
	queryApp(t, appURL, 1)
}

func (s *IntegrationTestSuite) waitForBlocksCommitted(height int) error {
	h, err := s.network.LatestHeight()
	if err != nil {
		return err
	}

	blocksToWait := h + int64(height)
	_, err = s.network.WaitForHeightWithTimeout(blocksToWait, time.Duration(blocksToWait+1)*5*time.Second)
	if err != nil {
		return err
	}

	return nil
}

func (s *IntegrationTestSuite) waitForBlockchainEvent(expEv interface{}) error {
	ctx, cancel := context.WithTimeout(s.ctx, 1*time.Minute)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-s.sub.Events():
			if reflect.TypeOf(ev) == reflect.TypeOf(expEv) {
				switch pev := ev.(type) {
				case *atypes.EventTrustedAuditorCreated:
					ev1 := expEv.(*atypes.EventTrustedAuditorCreated)
					if ev1.Equal(pev) {
						return nil
					}
				case *atypes.EventTrustedAuditorDeleted:
					ev1 := expEv.(*atypes.EventTrustedAuditorDeleted)
					if ev1.Equal(pev) {
						return nil
					}
				case *dtypes.EventDeploymentCreated:
					ev1 := expEv.(*dtypes.EventDeploymentCreated)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *dtypes.EventDeploymentUpdated:
					ev1 := expEv.(*dtypes.EventDeploymentUpdated)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *dtypes.EventDeploymentClosed:
					ev1 := expEv.(*dtypes.EventDeploymentClosed)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *dtypes.EventGroupStarted:
					ev1 := expEv.(*dtypes.EventGroupClosed)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *dtypes.EventGroupPaused:
					ev1 := expEv.(*dtypes.EventGroupClosed)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *dtypes.EventGroupClosed:
					ev1 := expEv.(*dtypes.EventGroupClosed)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *mtypes.EventOrderCreated:
					ev1 := expEv.(*mtypes.EventOrderCreated)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *mtypes.EventOrderClosed:
					ev1 := expEv.(*mtypes.EventOrderClosed)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *mtypes.EventBidCreated:
					ev1 := expEv.(*mtypes.EventBidCreated)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *mtypes.EventBidClosed:
					ev1 := expEv.(*mtypes.EventBidClosed)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *mtypes.EventLeaseCreated:
					ev1 := expEv.(*mtypes.EventLeaseCreated)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				case *mtypes.EventLeaseClosed:
					ev1 := expEv.(*mtypes.EventLeaseClosed)
					if ev1.ID.Equals(pev.ID) {
						return nil
					}
				}
			}
		}
	}
}
