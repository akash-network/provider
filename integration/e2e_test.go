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
	"strings"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"
	bankcli "github.com/cosmos/cosmos-sdk/x/bank/client/testutil"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	types "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	ttypes "github.com/akash-network/akash-api/go/node/take/v1beta3"

	"github.com/akash-network/node/testutil"
	clitestutil "github.com/akash-network/node/testutil/cli"
	"github.com/akash-network/node/testutil/network"
	ccli "github.com/akash-network/node/x/cert/client/cli"
	deploycli "github.com/akash-network/node/x/deployment/client/cli"
	"github.com/akash-network/node/x/provider/client/cli"

	akashclient "github.com/akash-network/provider/pkg/client/clientset/versioned"
	ptestutil "github.com/akash-network/provider/testutil/provider"
)

// IntegrationTestSuite wraps testing components
type IntegrationTestSuite struct {
	suite.Suite

	cfg         network.Config
	network     *network.Network
	validator   *network.Validator
	keyProvider keyring.Info
	keyTenant   keyring.Info

	group     *errgroup.Group
	ctx       context.Context
	ctxCancel context.CancelFunc

	appHost string
	appPort string

	ipMarketplace bool
}

const (
	defaultGasPrice      = "0.03uakt"
	defaultGasAdjustment = "1.4"
	uaktMinDeposit       = "5000000uakt"
	axlUSDCDenom         = "ibc/12C6A0C374171B595A0A9E18B83FA09D295FB1F2D8C6DAA3AC28683471752D84"
	axlUSCDMinDeposit    = "5000000" + axlUSDCDenom
)

var (
	deploymentUAktDeposit    = fmt.Sprintf("--deposit=%s", uaktMinDeposit)
	deploymentAxlUSDCDeposit = fmt.Sprintf("--deposit=%s", axlUSCDMinDeposit)
)

func cliGlobalFlags(args ...string) []string {
	return append(args,
		fmt.Sprintf("--%s=true", flags.FlagSkipConfirmation),
		fmt.Sprintf("--%s=%s", flags.FlagBroadcastMode, flags.BroadcastBlock),
		fmt.Sprintf("--gas=%s", flags.GasFlagAuto),
		fmt.Sprintf("--%s=%s", flags.FlagGasAdjustment, defaultGasAdjustment),
		fmt.Sprintf("--%s=%s", flags.FlagGasPrices, defaultGasPrice))
}

func (s *IntegrationTestSuite) SetupSuite() {
	s.appHost, s.appPort = appEnv(s.T())

	// Create a network for test
	cfg := testutil.DefaultConfig(testutil.WithInterceptState(func(cdc codec.Codec, s string, istate json.RawMessage) json.RawMessage {
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
			state := &dtypes.GenesisState{}
			cdc.MustUnmarshalJSON(istate, state)
			state.Params.MinDeposits = append(state.Params.MinDeposits, sdk.NewInt64Coin(axlUSDCDenom, 5000000))

			res = cdc.MustMarshalJSON(state)
		}

		return res
	}))

	cfg.NumValidators = 1
	cfg.MinGasPrices = fmt.Sprintf("0%s", testutil.CoinDenom)
	s.cfg = cfg
	s.network = network.New(s.T(), cfg)

	kb := s.network.Validators[0].ClientCtx.Keyring
	_, _, err := kb.NewMnemonic("keyBar", keyring.English, sdk.FullFundraiserPath, "", hd.Secp256k1)
	s.Require().NoError(err)
	_, _, err = kb.NewMnemonic("keyFoo", keyring.English, sdk.FullFundraiserPath, "", hd.Secp256k1)
	s.Require().NoError(err)

	// Wait for the network to start
	_, err = s.network.WaitForHeight(1)
	s.Require().NoError(err)

	//
	s.validator = s.network.Validators[0]

	// Send coins value
	sendTokens := sdk.Coins{
		sdk.NewCoin(s.cfg.BondDenom, mtypes.DefaultBidMinDeposit.Amount.MulRaw(4)),
		sdk.NewCoin(axlUSDCDenom, mtypes.DefaultBidMinDeposit.Amount.MulRaw(4)),
	}

	// Setup a Provider key
	s.keyProvider, err = s.validator.ClientCtx.Keyring.Key("keyFoo")
	s.Require().NoError(err)

	// give provider some coins
	res, err := bankcli.MsgSendExec(
		s.validator.ClientCtx,
		s.validator.Address,
		s.keyProvider.GetAddress(),
		sendTokens,
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	// Set up second tenant key
	s.keyTenant, err = s.validator.ClientCtx.Keyring.Key("keyBar")
	s.Require().NoError(err)

	// give tenant some coins too
	res, err = bankcli.MsgSendExec(
		s.validator.ClientCtx,
		s.validator.Address,
		s.keyTenant.GetAddress(),
		sendTokens,
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	// address for provider to listen on
	_, port, err := server.FreeTCPAddr()
	require.NoError(s.T(), err)
	provHost := fmt.Sprintf("localhost:%s", port)
	provURL := url.URL{
		Host:   provHost,
		Scheme: "https",
	}
	// address for JWT server to listen on
	_, port, err = server.FreeTCPAddr()
	require.NoError(s.T(), err)
	jwtHost := fmt.Sprintf("localhost:%s", port)
	jwtURL := url.URL{
		Host:   jwtHost,
		Scheme: "https",
	}

	_, port, err = server.FreeTCPAddr()
	require.NoError(s.T(), err)
	hostnameOperatorHost := fmt.Sprintf("localhost:%s", port)

	var ipOperatorHost string
	if s.ipMarketplace {
		_, port, err = server.FreeTCPAddr()
		require.NoError(s.T(), err)
		ipOperatorHost = fmt.Sprintf("localhost:%s", port)
	}

	provFileStr := fmt.Sprintf(providerTemplate, provURL.String(), jwtURL.String())
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
	_, err = cli.TxCreateProviderExec(
		s.validator.ClientCtx,
		s.keyProvider.GetAddress(),
		fmt.Sprintf("%s/%s", s.network.BaseDir, fstat.Name()),
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	// Generate provider's certificate
	_, err = ccli.TxGenerateServerExec(
		context.Background(),
		s.validator.ClientCtx,
		s.keyProvider.GetAddress(),
		"localhost",
	)
	s.Require().NoError(err)

	// Publish provider's certificate
	_, err = ccli.TxPublishServerExec(
		context.Background(),
		s.validator.ClientCtx,
		s.keyProvider.GetAddress(),
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	// Generate tenant's certificate
	_, err = ccli.TxGenerateClientExec(
		context.Background(),
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		fmt.Sprintf("--%s=true", flags.FlagSkipConfirmation),
		fmt.Sprintf("--%s=%s", flags.FlagBroadcastMode, flags.BroadcastBlock),
		fmt.Sprintf("--%s=%s", flags.FlagFees, sdk.NewCoins(sdk.NewCoin(s.cfg.BondDenom, sdk.NewInt(10))).String()),
		fmt.Sprintf("--gas=%d", flags.DefaultGasLimit),
	)
	s.Require().NoError(err)

	// Publish tenant's certificate
	_, err = ccli.TxPublishClientExec(
		context.Background(),
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	pemSrc := fmt.Sprintf("%s/%s.pem", s.validator.ClientCtx.HomeDir, s.keyProvider.GetAddress().String())
	pemDst := fmt.Sprintf("%s/%s.pem", strings.Replace(s.validator.ClientCtx.HomeDir, "simd", "simcli", 1), s.keyProvider.GetAddress().String())
	input, err := os.ReadFile(pemSrc)
	s.Require().NoError(err)

	err = os.WriteFile(pemDst, input, 0400)
	s.Require().NoError(err)

	pemSrc = fmt.Sprintf("%s/%s.pem", s.validator.ClientCtx.HomeDir, s.keyTenant.GetAddress().String())
	pemDst = fmt.Sprintf("%s/%s.pem", strings.Replace(s.validator.ClientCtx.HomeDir, "simd", "simcli", 1), s.keyTenant.GetAddress().String())
	input, err = os.ReadFile(pemSrc)
	s.Require().NoError(err)

	err = os.WriteFile(pemDst, input, 0400)
	s.Require().NoError(err)

	localCtx := s.validator.ClientCtx.WithOutputFormat("json")
	// test query providers
	resp, err := cli.QueryProvidersExec(localCtx)
	s.Require().NoError(err)

	out := &types.QueryProvidersResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(resp.Bytes(), out)
	s.Require().NoError(err)
	s.Require().Len(out.Providers, 1, "Provider Creation Failed")
	providers := out.Providers
	s.Require().Equal(s.keyProvider.GetAddress().String(), providers[0].Owner)

	// test query provider
	createdProvider := providers[0]
	resp, err = cli.QueryProviderExec(localCtx, createdProvider.Owner)
	s.Require().NoError(err)

	var provider types.Provider
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(resp.Bytes(), &provider)
	s.Require().NoError(err)
	s.Require().Equal(createdProvider, provider)

	// Run Provider service
	keyName := s.keyProvider.GetName()

	// Change the akash home directory for CLI to access the test keyring
	cliHome := strings.Replace(s.validator.ClientCtx.HomeDir, "simd", "simcli", 1)

	cctx := s.validator.ClientCtx

	// A context object to tie the lifetime of the provider & hostname operator to
	ctx, cancel := context.WithCancel(context.Background())
	s.ctxCancel = cancel

	s.group, s.ctx = errgroup.WithContext(ctx)

	// all command use viper which is meant for use by a single goroutine only
	// so wait for the provider to start before running the hostname operator
	extraArgs := []string{
		fmt.Sprintf("--%s=%s", flags.FlagFees, sdk.NewCoins(sdk.NewCoin(s.cfg.BondDenom, sdk.NewInt(20))).String()),
		"--deployment-runtime-class=none", // do not use gvisor in test
		fmt.Sprintf("--hostname-operator-endpoint=%s", hostnameOperatorHost),
	}

	if s.ipMarketplace {
		extraArgs = append(extraArgs, fmt.Sprintf("--ip-operator-endpoint=%s", ipOperatorHost))
		extraArgs = append(extraArgs, "--ip-operator")
	}
	s.group.Go(func() error {
		_, err := ptestutil.RunLocalProvider(ctx,
			cctx,
			cctx.ChainID,
			s.validator.RPCAddress,
			cliHome,
			keyName,
			provURL.Host,
			extraArgs...,
		)

		if err != nil {
			s.T().Logf("provider exit %v", err)
		}

		return err
	})

	dialer := net.Dialer{
		Timeout: time.Second * 3,
	}

	// Wait for the provider gateway to be up and running

	s.T().Log("waiting for provider gateway")
	waitForTCPSocket(s.ctx, dialer, provHost, s.T())

	// --- Start JWT Server
	s.group.Go(func() error {
		s.T().Logf("starting JWT server for test on %v", jwtURL.Host)
		_, err := ptestutil.RunProviderJWTServer(s.ctx,
			cctx,
			keyName,
			jwtURL.Host,
		)
		s.Assert().NoError(err)
		return err
	})

	s.T().Log("waiting for JWT server")
	waitForTCPSocket(s.ctx, dialer, jwtHost, s.T())

	// --- Start hostname operator
	s.group.Go(func() error {
		s.T().Logf("starting hostname operator for test on %v", hostnameOperatorHost)
		_, err := ptestutil.RunLocalHostnameOperator(s.ctx, cctx, hostnameOperatorHost)
		s.Assert().NoError(err)
		return err
	})

	s.T().Log("waiting for hostname operator")
	waitForTCPSocket(s.ctx, dialer, hostnameOperatorHost, s.T())

	if s.ipMarketplace {
		s.group.Go(func() error {
			s.T().Logf("starting ip operator for test on %v", ipOperatorHost)
			_, err := ptestutil.RunLocalIPOperator(s.ctx, cctx, ipOperatorHost, s.keyProvider.GetAddress())
			s.Assert().NoError(err)
			return err
		})

		s.T().Log("waiting for IP operator")
		waitForTCPSocket(s.ctx, dialer, ipOperatorHost, s.T())
	}

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
	keyTenant, err := s.validator.ClientCtx.Keyring.Key("keyBar")
	s.Require().NoError(err)
	resp, err := deploycli.QueryDeploymentsExec(s.validator.ClientCtx.WithOutputFormat("json"))
	s.Require().NoError(err)
	deployResp := &dtypes.QueryDeploymentsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(resp.Bytes(), deployResp)
	s.Require().NoError(err)

	deployments := deployResp.Deployments

	s.T().Logf("Cleaning up %d deployments", len(deployments))
	for _, createdDep := range deployments {
		if createdDep.Deployment.State != dtypes.DeploymentActive {
			continue
		}
		// teardown lease
		res, err := deploycli.TxCloseDeploymentExec(
			s.validator.ClientCtx,
			keyTenant.GetAddress(),
			cliGlobalFlags(fmt.Sprintf("--owner=%s", createdDep.Groups[0].GroupID.Owner),
				fmt.Sprintf("--dseq=%v", createdDep.Deployment.DeploymentID.DSeq))...,
		)
		s.Require().NoError(err)
		s.Require().NoError(s.waitForBlocksCommitted(1))
		clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())
	}

	return len(deployments)
}

func (s *IntegrationTestSuite) TearDownSuite() {
	s.T().Log("Cleaning up after E2E suite")
	n := s.closeDeployments()
	// test query deployments with state filter closed
	resp, err := deploycli.QueryDeploymentsExec(
		s.validator.ClientCtx.WithOutputFormat("json"),
		"--state=closed",
	)
	s.Require().NoError(err)

	qResp := &dtypes.QueryDeploymentsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(resp.Bytes(), qResp)
	s.Require().NoError(err)
	s.Require().True(len(qResp.Deployments) == n, "Deployment Close Failed")

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

func newestLease(leases []mtypes.QueryLeaseResponse) mtypes.Lease {
	result := mtypes.Lease{}
	assigned := false

	for _, lease := range leases {
		if !assigned {
			result = lease.Lease
			assigned = true
		} else if result.GetLeaseID().DSeq < lease.Lease.GetLeaseID().DSeq {
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
	suite.Run(t, new(E2EPersistentStorageBeta2))
	suite.Run(t, new(E2EPersistentStorageDeploymentUpdate))
	suite.Run(t, new(E2EMigrateHostname))
	suite.Run(t, new(E2EJWTServer))
	suite.Run(t, new(E2ECustomCurrency))
	suite.Run(t, &E2EIPAddress{IntegrationTestSuite{ipMarketplace: true}})
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

// TestQueryApp enables rapid testing of the querying functionality locally
// Not for CI tests.
func TestQueryApp(t *testing.T) {
	integrationTestOnly(t)
	host, appPort := appEnv(t)

	appURL := fmt.Sprintf("http://%s:%s/", host, appPort)
	queryApp(t, appURL, 1)
}
