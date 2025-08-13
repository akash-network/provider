package provider

import (
	"context"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdktest "github.com/cosmos/cosmos-sdk/testutil"
	testutilcli "pkg.akt.dev/go/cli/testutil"

	pcmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	"github.com/akash-network/provider/operator"
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

// ExecSendManifest for integration testing
// this is similar to cli command exampled below
//
//	akash provider send-manifest --owner <address> \
//		--dseq 7 \
//		--provider <address> ./../_run/kube/deployment.yaml \
//		--home=/tmp/akash_integration_TestE2EApp_324892307/.akashctl --node=tcp://0.0.0.0:41863
func ExecSendManifest(ctx context.Context, clientCtx sdkclient.Context, args ...string) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cobraCmd := pcmd.SendManifestCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cobraCmd, args...)
}

func ExecLeaseShell(ctx context.Context, clientCtx sdkclient.Context, args ...string) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cobraCmd := pcmd.LeaseShellCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cobraCmd, args...)
}

func ExecMigrateHostname(ctx context.Context, clientCtx sdkclient.Context, args ...string) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cobraCmd := pcmd.MigrateHostnamesCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cobraCmd, args...)
}

// RunLocalProvider wraps up the Provider cobra command for testing and supplies
// new default values to the flags.
// prev: akash provider run --from=foo --cluster-k8s --gateway-listen-address=localhost:39729 --home=/tmp/akash_integration_TestE2EApp_324892307/.akash --node=tcp://0.0.0.0:41863 --keyring-backend test
func RunLocalProvider(
	ctx context.Context,
	clientCtx sdkclient.Context,
	args ...string,
) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cmd := pcmd.RunCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cmd, args...)
}

func RunLocalOperator(ctx context.Context, clientCtx sdkclient.Context, args ...string) (sdktest.BufferWriter, error) {
	takeCmdLock()
	cmd := operator.OperatorsCmd()
	releaseCmdLock()

	return testutilcli.ExecTestCLICmd(ctx, clientCtx, cmd, args...)
}
