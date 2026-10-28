package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/operator/hostname"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

const (
	FlagMigrateGatewayTo          = "to"
	FlagMigrateGatewayDryRun      = "dry-run"
	FlagMigrateGatewayConcurrency = "concurrency"
)

var migrateGatewayFlags = []string{
	FlagMigrateGatewayTo,
	FlagMigrateGatewayDryRun,
	FlagMigrateGatewayConcurrency,
	providerflags.FlagK8sManifestNS,
	FlagGatewayName,
	FlagGatewayNamespace,
}

// MigrateGatewayCmd moves every tenant route to one Gateway API implementation's
// shape. Run it with the hostname operator stopped, inside the cutover window:
// before the Gateway's class switch for --to envoy, after it for --to nginx.
func MigrateGatewayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "reconcile tenant routes for a Gateway API implementation (cutover and rollback)",
		Long: "Reconciles every tenant HTTPRoute and its extensions for the target implementation, " +
			"then reports an inventory. Exits non-zero unless the Gateway may change class: no route failed " +
			"and no route is left in a shape the target cannot serve. Run with the hostname operator stopped.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			// Bound only when this command runs: viper keys are global, and
			// gateway-name, gateway-namespace and k8s-manifest-ns are also bound by
			// provider-services run.
			for _, name := range migrateGatewayFlags {
				if err := viper.BindPFlag(name, cmd.Flags().Lookup(name)); err != nil {
					return err
				}
			}
			return clientcommon.SetKubeConfigToCmd(cmd)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			logger := ctxlog.LogcFromCtx(ctx).With("module", "migrate-gateway")

			target := viper.GetString(FlagMigrateGatewayTo)
			if target != "envoy" && target != "nginx" {
				return fmt.Errorf("--%s must be envoy or nginx, got %q", FlagMigrateGatewayTo, target)
			}

			kubecfg := fromctx.MustKubeConfigFromCtx(ctx)
			dc, err := dynamic.NewForConfig(kubecfg)
			if err != nil {
				return fmt.Errorf("failed to create dynamic client: %w", err)
			}
			ac, err := akashclientset.NewForConfig(kubecfg)
			if err != nil {
				return fmt.Errorf("failed to create akash client: %w", err)
			}

			report, err := hostname.MigrateRoutes(ctx, hostname.MigrateConfig{
				Logger:           logger,
				Akash:            ac,
				Dynamic:          dc,
				ManifestNS:       viper.GetString(providerflags.FlagK8sManifestNS),
				GatewayName:      viper.GetString(FlagGatewayName),
				GatewayNamespace: viper.GetString(FlagGatewayNamespace),
				Target:           target,
				DryRun:           viper.GetBool(FlagMigrateGatewayDryRun),
				Concurrency:      viper.GetInt(FlagMigrateGatewayConcurrency),
			})
			if err != nil {
				return err
			}

			printMigrateReport(cmd, report)

			if report.DryRun {
				return nil
			}
			if !report.Ready() {
				return fmt.Errorf("%w: %d failed, %d incompatible; do not switch the Gateway class",
					hostname.ErrMigrationIncomplete, len(report.Failed), len(report.Incompatible))
			}
			return nil
		},
	}

	cmd.Flags().String(FlagMigrateGatewayTo, "", "target implementation: envoy or nginx")
	cmd.Flags().Bool(FlagMigrateGatewayDryRun, false, "report the planned changes without writing")
	cmd.Flags().Int(FlagMigrateGatewayConcurrency, 16, "routes reconciled in parallel")
	cmd.Flags().String(providerflags.FlagK8sManifestNS, "lease", "namespace of the provider's manifest and ProviderHost resources")
	cmd.Flags().String(FlagGatewayName, "akash-gateway", "Gateway that tenant HTTPRoutes attach to")
	cmd.Flags().String(FlagGatewayNamespace, "akash-gateway", "Namespace of the Gateway")

	if err := cmd.MarkFlagRequired(FlagMigrateGatewayTo); err != nil {
		panic(err)
	}
	if err := providerflags.AddKubeConfigPathFlag(cmd); err != nil {
		panic(err)
	}

	return cmd
}

func printMigrateReport(cmd *cobra.Command, report hostname.MigrateReport) {
	out := cmd.OutOrStdout()
	mode := ""
	if report.DryRun {
		mode = " (dry run)"
	}

	_, _ = fmt.Fprintf(out, "gateway migration to %s%s\n", report.Target, mode)
	_, _ = fmt.Fprintf(out, "  migrated:     %d\n", len(report.Migrated))
	_, _ = fmt.Fprintf(out, "  failed:       %d\n", len(report.Failed))
	_, _ = fmt.Fprintf(out, "  no manifest:  %d\n", len(report.NoManifest))
	_, _ = fmt.Fprintf(out, "  incompatible: %d\n", len(report.Incompatible))

	failed := make([]string, 0, len(report.Failed))
	for h := range report.Failed {
		failed = append(failed, h)
	}
	sort.Strings(failed)
	for _, h := range failed {
		_, _ = fmt.Fprintf(out, "  FAILED %s: %s\n", h, report.Failed[h])
	}
	for _, h := range report.NoManifest {
		_, _ = fmt.Fprintf(out, "  no manifest %s\n", h)
	}
	for _, r := range report.Incompatible {
		_, _ = fmt.Fprintf(out, "  INCOMPATIBLE %s\n", r)
	}
}
