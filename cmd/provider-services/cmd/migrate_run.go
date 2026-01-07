package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/cmd/provider-services/cmd/util"
	"github.com/akash-network/provider/migrations"
	"github.com/akash-network/provider/tools/fromctx"
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

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return doMigrateRun(cmd.Context(), cmd)
		},
	}

	cmd.Flags().String(FlagMigrationsStatePath, "", "path to migrations state file (default: $AP_HOME/migrations.json)")

	if err := providerflags.AddKubeConfigPathFlag(cmd); err != nil {
		panic(err.Error())
	}

	return cmd
}

func doMigrateRun(ctx context.Context, cmd *cobra.Command) error {
	logger := ctxlog.LogcFromCtx(ctx)

	statePath, err := determineStatePath(cmd)
	if err != nil {
		return fmt.Errorf("determining state path: %w", err)
	}

	stateManager := migrations.NewStateManager(statePath)
	registry := migrations.NewRegistry(stateManager)

	currentVersion := setCurrentVersionOrDefault(registry)

	previousVersion, err := stateManager.GetProviderVersion()
	if err != nil {
		return fmt.Errorf("failed to get previous provider version: %w", err)
	}

	if previousVersion == "" {
		logger.Info("fresh install detected, marking all migrations as applied", "current_version", currentVersion)
		allMigrations := migrations.GetAll()
		for _, m := range allMigrations {
			if err := stateManager.MarkApplied(m.Name()); err != nil {
				return fmt.Errorf("failed to mark migration %q as applied: %w", m.Name(), err)
			}
		}
		if err := stateManager.SetProviderVersion(currentVersion); err != nil {
			return fmt.Errorf("failed to set provider version: %w", err)
		}
		return nil
	}

	logger.Info("checking for migrations", "previous_version", previousVersion, "current_version", currentVersion)

	allMigrations := migrations.GetAll()
	logger.Info("discovered migrations", "count", len(allMigrations))

	if len(allMigrations) == 0 {
		logger.Info("no migrations found")
		if err := stateManager.SetProviderVersion(currentVersion); err != nil {
			return fmt.Errorf("failed to update provider version: %w", err)
		}
		return nil
	}

	for _, m := range allMigrations {
		logger.Info("registered migration", "name", m.Name(), "description", m.Description(), "from_version", m.FromVersion())
	}

	pending, err := registry.GetPending(ctx)
	if err != nil {
		return fmt.Errorf("failed to get pending migrations: %w", err)
	}

	if len(pending) == 0 {
		logger.Info("no pending migrations")
		if err := stateManager.SetProviderVersion(currentVersion); err != nil {
			return fmt.Errorf("failed to update provider version: %w", err)
		}
		return nil
	}

	logger.Info("running pending migrations", "count", len(pending))

	successCount, errs := registry.RunMigrations(ctx)

	if len(errs) > 0 {
		for _, err := range errs {
			logger.Error("migration error", "err", err)
		}
	}

	logger.Info("migrations completed", "successful", successCount, "failed", len(errs))

	if len(errs) > 0 {
		return fmt.Errorf("%d migration(s) failed", len(errs))
	}

	return nil
}
