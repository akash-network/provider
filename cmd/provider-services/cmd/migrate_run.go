package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"cosmossdk.io/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"
	cflags "pkg.akt.dev/go/cli/flags"
	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/cmd/provider-services/cmd/util"
	"github.com/akash-network/provider/migrations"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/version"
)

func MigrateRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "run",
		Short:        "run pending migrations",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := clientcommon.SetKubeConfigToCmd(cmd); err != nil {
				return err
			}

			logger := util.OpenLogger()
			ctx := ctxlog.WithLogger(cmd.Context(), logger)
			cmd.SetContext(ctx)

			kubecfg := fromctx.MustKubeConfigFromCtx(ctx)
			kc, err := kubernetes.NewForConfig(kubecfg)
			if err != nil {
				return fmt.Errorf("failed to create kubernetes client: %w", err)
			}

			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyKubeClientSet, kc)

			gatewayName := viper.GetString(FlagGatewayName)
			gatewayNamespace := viper.GetString(FlagGatewayNamespace)

			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyGatewayName, gatewayName)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyGatewayNamespace, gatewayNamespace)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return doMigrateRun(cmd.Context(), cmd)
		},
	}

	if err := addMigrateRunFlags(cmd); err != nil {
		panic(err.Error())
	}

	return cmd
}

func addMigrateRunFlags(cmd *cobra.Command) error {
	cmd.Flags().String(FlagMigrationsStatePath, "", "path to migrations state file (default: $AP_HOME/migrations.json)")

	if err := providerflags.AddKubeConfigPathFlag(cmd); err != nil {
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

func doMigrateRun(ctx context.Context, cmd *cobra.Command) error {
	logger := ctxlog.LogcFromCtx(ctx)

	statePath, err := determineStatePath(cmd)
	if err != nil {
		return fmt.Errorf("determining state path: %w", err)
	}

	result, err := runMigrations(ctx, statePath, logger)
	if err != nil {
		return err
	}

	// Log completion with success/failed counts (even if there are errors)
	logger.Info("migrations completed", "successful", result.SuccessCount, "failed", len(result.Errs))

	if len(result.Errs) > 0 {
		return fmt.Errorf("%d migration(s) failed", len(result.Errs))
	}

	return nil
}

func determineStatePath(cmd *cobra.Command) (string, error) {
	statePath, err := cmd.Flags().GetString(FlagMigrationsStatePath)
	if err != nil {
		return "", fmt.Errorf("getting migrations state path flag: %w", err)
	}

	if statePath == "" {
		homeDir, err := cmd.Flags().GetString(cflags.FlagHome)
		if err != nil {
			// Try persistent flags if not found in regular flags
			homeDir, err = cmd.PersistentFlags().GetString(cflags.FlagHome)
			if err != nil {
				return "", fmt.Errorf("unable to get home directory flag: %w", err)
			}
		}
		if homeDir == "" {
			return "", fmt.Errorf("home directory flag is not set")
		}
		statePath = filepath.Join(homeDir, "migrations.json")
	}

	return statePath, nil
}

func setCurrentVersionOrDefault(registry *migrations.Registry) string {
	currentVersion := version.Version
	if currentVersion == "" {
		currentVersion = "0.0.0"
	}
	registry.SetCurrentVersion(currentVersion)

	return currentVersion
}

// runMigrationsResult contains the results of running migrations.
type runMigrationsResult struct {
	SuccessCount int
	Errs         []error
}

// runMigrations executes pending migrations with the provided state path.
// The logContext parameter is used to customize log messages (e.g., "running pending migrations" vs "running pending migrations on startup").
// The logMigrationStatus parameter controls whether detailed migration status is logged for each migration.
// Returns the migration results and an error if the migration process itself failed (not individual migration errors).
func runMigrations(ctx context.Context, statePath string, logger log.Logger) (*runMigrationsResult, error) {
	stateManager := migrations.NewStateManager(statePath)
	registry := migrations.NewRegistry(stateManager)

	currentVersion := setCurrentVersionOrDefault(registry)

	previousVersion, err := stateManager.GetProviderVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get previous provider version: %w", err)
	}

	if previousVersion == "" {
		logger.Info("fresh install detected, marking all migrations as applied", "current_version", currentVersion)
		allMigrations := migrations.GetAll()
		for _, m := range allMigrations {
			if err := stateManager.MarkApplied(m.Name()); err != nil {
				return nil, fmt.Errorf("failed to mark migration %q as applied: %w", m.Name(), err)
			}
		}
		if err := stateManager.SetProviderVersion(currentVersion); err != nil {
			return nil, fmt.Errorf("failed to set provider version: %w", err)
		}
		return &runMigrationsResult{SuccessCount: 0, Errs: nil}, nil
	}

	logger.Info("checking for migrations", "previous_version", previousVersion, "current_version", currentVersion)

	allMigrations := migrations.GetAll()
	logger.Info("discovered migrations", "count", len(allMigrations))
	if len(allMigrations) == 0 {
		logger.Info("no migrations found")
		if err := stateManager.SetProviderVersion(currentVersion); err != nil {
			return nil, fmt.Errorf("failed to update provider version: %w", err)
		}
		return &runMigrationsResult{SuccessCount: 0, Errs: nil}, nil
	}

	for _, m := range allMigrations {
		logger.Info("registered migration", "name", m.Name(), "description", m.Description(), "from_version", m.FromVersion())
		applied, _ := stateManager.IsApplied(m.Name())
		logger.Info("migration status", "name", m.Name(), "applied", applied, "previous_version", previousVersion, "from_version", m.FromVersion())
	}

	pending, err := registry.GetPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending migrations: %w", err)
	}

	if len(pending) == 0 {
		logger.Info("no pending migrations")
		if err := stateManager.SetProviderVersion(currentVersion); err != nil {
			return nil, fmt.Errorf("failed to update provider version: %w", err)
		}
		return &runMigrationsResult{SuccessCount: 0, Errs: nil}, nil
	}

	logger.Info("running pending migrations", "count", len(pending))

	successCount, errs := registry.RunMigrations(ctx)

	if len(errs) > 0 {
		for _, err := range errs {
			logger.Error("migration error", "err", err)
		}
		return &runMigrationsResult{SuccessCount: successCount, Errs: errs}, nil
	}

	logger.Info("migrations completed successfully", "count", successCount)
	return &runMigrationsResult{SuccessCount: successCount, Errs: nil}, nil
}
