package cmd

import (
	"context"

	"github.com/cosmos/cosmos-sdk/client"
	sdktest "github.com/cosmos/cosmos-sdk/testutil"

	testutilcli "pkg.akt.dev/go/cli/testutil"
)

func ExecProviderLeaseStatus(ctx context.Context, clientCtx client.Context, extraArgs ...string) (sdktest.BufferWriter, error) {
	return testutilcli.ExecTestCLICmd(ctx, clientCtx, leaseStatusCmd(), extraArgs...)
}

func ExecProviderServiceStatus(ctx context.Context, clientCtx client.Context, extraArgs ...string) (sdktest.BufferWriter, error) {
	return testutilcli.ExecTestCLICmd(ctx, clientCtx, serviceStatusCmd(), extraArgs...)
}

func ExecProviderStatus(ctx context.Context, clientCtx client.Context, extraArgs ...string) (sdktest.BufferWriter, error) {
	return testutilcli.ExecTestCLICmd(ctx, clientCtx, statusCmd(), extraArgs...)
}

func ExecProviderService(ctx context.Context, clientCtx client.Context, extraArgs ...string) (sdktest.BufferWriter, error) {
	return testutilcli.ExecTestCLICmd(ctx, clientCtx, leaseLogsCmd(), extraArgs...)
}
