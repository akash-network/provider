package provider

import (
	"context"
	"fmt"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	testutilcli "github.com/akash-network/node/testutil/cli"
	"github.com/cosmos/cosmos-sdk/client"
	cosmosclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktest "github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	tmcli "github.com/tendermint/tendermint/libs/cli"

	pcmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	"github.com/akash-network/provider/operator"
	operatorcommon "github.com/akash-network/provider/operator/common"
)

const (
	TestClusterPublicHostname   = "e2e.test"
	TestClusterNodePortQuantity = 100
)

var cmdLock = make(chan struct{}, 1)

func init() {
	releaseCmdLock()
}

func takeCmdLock() {
	<-cmdLock
}

func releaseCmdLock() {
	cmdLock <- struct{}{}
}

// TestSendManifest for integration testing
// this is similar to cli command exampled below
//
//	akash provider send-manifest --owner <address> \
//		--dseq 7 \
//		--provider <address> ./../_run/kube/deployment.yaml \
//		--home=/tmp/akash_integration_TestE2EApp_324892307/.akashctl --node=tcp://0.0.0.0:41863
func TestSendManifest(clientCtx client.Context, id mtypes.BidID, sdlPath string, extraArgs ...string) (sdktest.BufferWriter, error) {
	args := []string{
		fmt.Sprintf("--dseq=%v", id.DSeq),
		fmt.Sprintf("--provider=%s", id.Provider),
	}
	args = append(args, sdlPath)
	args = append(args, extraArgs...)
	fmt.Printf("%v\n", args)

	takeCmdLock()
	cobraCmd := pcmd.SendManifestCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(context.Background(), clientCtx, cobraCmd, args...)
}

func TestLeaseShell(ctx context.Context, clientCtx client.Context, extraArgs []string, lID mtypes.LeaseID, replicaIndex int, tty bool, stdin bool, serviceName string, cmd ...string) (sdktest.BufferWriter, error) {
	args := []string{
		fmt.Sprintf("--provider=%s", lID.Provider),
		fmt.Sprintf("--replica-index=%d", replicaIndex),
		fmt.Sprintf("--dseq=%v", lID.DSeq),
		fmt.Sprintf("--gseq=%v", lID.GSeq),
	}
	if tty {
		args = append(args, "--tty")
	}
	if stdin {
		args = append(args, "--stdin")
	}
	args = append(args, extraArgs...)
	args = append(args, serviceName)
	args = append(args, cmd...)

	takeCmdLock()
	cobraCmd := pcmd.LeaseShellCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cobraCmd, args...)
}

func TestMigrateHostname(clientCtx client.Context, leaseID mtypes.LeaseID, dseq uint64, hostname string, cmd ...string) (sdktest.BufferWriter, error) {
	args := []string{
		fmt.Sprintf("--provider=%s", leaseID.Provider),
		fmt.Sprintf("--dseq=%v", dseq),
	}
	args = append(args, cmd...)
	args = append(args, hostname)
	fmt.Printf("%v\n", args)

	takeCmdLock()
	cobraCmd := pcmd.MigrateHostnamesCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(context.Background(), clientCtx, cobraCmd, args...)
}

// RunLocalProvider wraps up the Provider cobra command for testing and supplies
// new default values to the flags.
// prev: akash provider run --from=foo --cluster-k8s --gateway-listen-address=localhost:39729 --home=/tmp/akash_integration_TestE2EApp_324892307/.akash --node=tcp://0.0.0.0:41863 --keyring-backend test
func RunLocalProvider(
	ctx context.Context,
	clientCtx cosmosclient.Context,
	chainID, nodeRPC, akashHome, from, gatewayListenAddress string,
	extraArgs ...string,
) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cmd := pcmd.RunCmd()
	releaseCmdLock()
	// Flags added because command not being wrapped by the Tendermint's PrepareMainCmd()
	cmd.PersistentFlags().StringP(tmcli.HomeFlag, "", akashHome, "directory for config and data")
	cmd.PersistentFlags().Bool(tmcli.TraceFlag, false, "print out full stack trace on errors")

	args := []string{
		fmt.Sprintf("--%s", pcmd.FlagClusterK8s),
		fmt.Sprintf("--%s=%s", flags.FlagChainID, chainID),
		fmt.Sprintf("--%s=%s", flags.FlagNode, nodeRPC),
		fmt.Sprintf("--%s=%s", flags.FlagHome, akashHome),
		fmt.Sprintf("--from=%s", from),
		fmt.Sprintf("--%s=%s", pcmd.FlagGatewayListenAddress, gatewayListenAddress),
		fmt.Sprintf("--%s=%s", flags.FlagKeyringBackend, keyring.BackendTest),
		fmt.Sprintf("--%s=%s", pcmd.FlagClusterPublicHostname, TestClusterPublicHostname),
		fmt.Sprintf("--%s=%d", pcmd.FlagClusterNodePortQuantity, TestClusterNodePortQuantity),
		fmt.Sprintf("--%s=%s", pcmd.FlagBidPricingStrategy, "randomRange"),
	}

	args = append(args, extraArgs...)

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cmd, args...)
}

func RunLocalHostnameOperator(ctx context.Context, clientCtx cosmosclient.Context, listenPort int) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cmd := operator.OperatorsCmd()
	releaseCmdLock()
	args := []string{
		"hostname",
		fmt.Sprintf("--%s=127.0.0.1", operatorcommon.FlagRESTAddress),
		fmt.Sprintf("--%s=%d", operatorcommon.FlagRESTPort, listenPort),
	}

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cmd, args...)
}

func RunLocalIPOperator(ctx context.Context, clientCtx cosmosclient.Context, listenPort int, providerAddress sdk.AccAddress) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cmd := operator.OperatorsCmd()
	releaseCmdLock()

	args := []string{
		"ip",
		fmt.Sprintf("--%s=127.0.0.1", operatorcommon.FlagRESTAddress),
		fmt.Sprintf("--%s=%d", operatorcommon.FlagRESTPort, listenPort),
		fmt.Sprintf("--provider=%s", providerAddress.String()),
	}

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cmd, args...)
}
