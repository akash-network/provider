package v2beta2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/theckman/yacspin"
	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/homedir"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/types/query"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	mmigrate "github.com/akash-network/akash-api/go/node/market/v1beta3/migrate"

	"github.com/akash-network/node/client"
	"github.com/akash-network/node/util/cli"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	cmdutil "github.com/akash-network/provider/cmd/provider-services/cmd/util"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta1"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	v2beta2migrate "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2/migrate"
	akashclient "github.com/akash-network/provider/pkg/client/clientset/versioned"
)

const (
	flagDryRun              = "crd-dry-run"
	flagCrdBackupPath       = "crd-backup-path"
	flagCrdRestoreOnly      = "crd-restore-only"
	flagCrdBackupOnly       = "crd-backup-only"
	flagCleanDanglingLeases = "clean-dangling"
	FlagKubeConfig          = "kubeconfig"
	FlagK8sManifestNS       = "k8s-manifest-ns"
	flagCrdOld              = "crd-v2beta1"
	flagCrdNew              = "crd-v2beta2"
)

func MigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "v2beta2",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doMigrateCRDs(cmd.Context(), cmd)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	cmd.Flags().String(FlagK8sManifestNS, "lease", "Cluster manifest namespace")
	if err := viper.BindPFlag(FlagK8sManifestNS, cmd.Flags().Lookup(FlagK8sManifestNS)); err != nil {
		return nil
	}

	cmd.Flags().String(FlagKubeConfig, path.Join(homedir.HomeDir(), ".kube", "config"), "kubernetes configuration file path")
	if err := viper.BindPFlag(FlagKubeConfig, cmd.Flags().Lookup(FlagKubeConfig)); err != nil {
		return nil
	}

	cmd.Flags().String(flagCrdBackupPath, "./", "path to backup CRDs")
	if err := viper.BindPFlag(flagCrdBackupPath, cmd.Flags().Lookup(flagCrdBackupPath)); err != nil {
		return nil
	}

	cmd.Flags().Bool(flagCrdRestoreOnly, false, "proceed to restore step without making current backup")
	if err := viper.BindPFlag(flagCrdRestoreOnly, cmd.Flags().Lookup(flagCrdRestoreOnly)); err != nil {
		return nil
	}

	cmd.Flags().Bool(flagCrdBackupOnly, false, "just create backup of the current CRDs")
	if err := viper.BindPFlag(flagCrdBackupOnly, cmd.Flags().Lookup(flagCrdBackupOnly)); err != nil {
		return nil
	}

	cmd.Flags().Bool(flagDryRun, true, "patch CRDs and save them as files. this does not delete CRDs from cluster")
	if err := viper.BindPFlag(flagDryRun, cmd.Flags().Lookup(flagDryRun)); err != nil {
		return nil
	}

	cmd.Flags().Bool(flagCleanDanglingLeases, false, "clean dangling leases")
	if err := viper.BindPFlag(flagCleanDanglingLeases, cmd.Flags().Lookup(flagCleanDanglingLeases)); err != nil {
		return nil
	}

	cmd.Flags().String(flagCrdOld, "", "path or URL to pre-upgrade CRD")
	if err := viper.BindPFlag(flagCrdOld, cmd.Flags().Lookup(flagCrdOld)); err != nil {
		return nil
	}

	cmd.Flags().String(flagCrdNew, "", "path or URL to post-upgrade CRD")
	if err := viper.BindPFlag(flagCrdNew, cmd.Flags().Lookup(flagCrdNew)); err != nil {
		return nil
	}

	_ = cmd.MarkFlagRequired(flagCrdNew)

	return cmd
}

func queryActiveLeases(ctx context.Context, cctx sdkclient.Context) (map[mtypes.LeaseID]dtypes.GroupSpec, error) {
	aclient := client.NewQueryClientFromCtx(cctx)

	activeLeases := make(map[mtypes.LeaseID]dtypes.GroupSpec)

	var pkey []byte

	for {
		var pgn *query.PageRequest
		if pkey != nil {
			pgn = &query.PageRequest{
				Key: pkey,
			}
		}

		lresp, err := aclient.Leases(ctx, &mtypes.QueryLeasesRequest{
			Filters: mtypes.LeaseFilters{
				Provider: cctx.FromAddress.String(),
				State:    mtypes.LeaseActive.String(),
			},
			Pagination: pgn,
		})
		if err != nil {
			if err != nil {
				return nil, err
			}
		}

		for _, lease := range lresp.Leases {
			lid := lease.Lease.LeaseID

			order, err := aclient.Order(ctx, &mtypes.QueryOrderRequest{ID: lid.OrderID()})
			if err != nil {
				fmt.Printf("couldn't query order for lease [%s]: %s", lid, err.Error())
				continue
			}

			activeLeases[lease.Lease.LeaseID] = order.Order.Spec
		}

		if pg := lresp.Pagination; pg != nil && len(pg.NextKey) > 0 {
			pkey = pg.NextKey
		} else {
			break
		}
	}

	return activeLeases, nil
}

func doMigrateCRDs(ctx context.Context, cmd *cobra.Command) (err error) {
	var spinner *yacspin.Spinner

	defer func() {
		if spinner != nil && spinner.Status() == yacspin.SpinnerRunning {
			if err != nil {
				spinner.StopFailMessage(fmt.Sprintf("failed with error: %s", err.Error()))
				_ = spinner.StopFail()
			} else {
				_ = spinner.Stop()
			}
		}
	}()

	ycfg := yacspin.Config{
		Frequency:         100 * time.Millisecond,
		CharSet:           yacspin.CharSets[52],
		ColorAll:          true,
		Prefix:            "",
		Suffix:            " ",
		SuffixAutoColon:   true,
		StopCharacter:     "✓",
		StopFailCharacter: "✗",
		StopColors:        []string{"fgGreen"},
		StopFailColors:    []string{"fgRed"},
	}

	spinner, err = yacspin.New(ycfg)
	if err != nil {
		return err
	}

	cmd.SilenceErrors = true

	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return fmt.Errorf("couldn't initialize query context: %w", err)
	}

	ns := viper.GetString(FlagK8sManifestNS)
	kubeConfig := viper.GetString(FlagKubeConfig)
	backupPath := path.Dir(viper.GetString(flagCrdBackupPath)) + "/crds/v2beta1"
	dryrunPath := path.Dir(viper.GetString(flagCrdBackupPath)) + "/crds/v2beta2"

	manifestsBackupPath := backupPath + "/manifests"
	hostsBackupPath := backupPath + "/hosts"
	ipsBackupPath := backupPath + "/ips"

	manifestsDryRunPath := dryrunPath + "/manifests"
	hostsDryRunPath := dryrunPath + "/hosts"
	ipsDryRunPath := dryrunPath + "/ips"

	restoreOnly := viper.GetBool(flagCrdRestoreOnly)
	backupOnly := viper.GetBool(flagCrdBackupOnly)
	dryRun := viper.GetBool(flagDryRun)
	// cleanDanglingLeases := viper.GetBool(flagCleanDanglingLeases)

	if !isKubectlAvail() {
		return errors.New("kubectl has not been found. install to proceed") // nolint goerr113
	}

	config, err := clientcommon.OpenKubeConfig(kubeConfig, cmdutil.OpenLogger().With("cmp", "migrator"))
	if err != nil {
		return err
	}

	if err != nil {
		return fmt.Errorf("kube: error building config flags: %w", err)
	}

	spinner.Message("loading CRDs")
	spinner.StopMessage("loaded CRDs")
	spinner.StopFailMessage("failed to load CRDs")

	_ = spinner.Start()
	crdOld, err := readOrDownload(viper.GetString(flagCrdOld))
	if err != nil {
		return err
	}

	crdNew, err := readOrDownload(viper.GetString(flagCrdNew))
	if err != nil {
		return err
	}

	_ = spinner.Stop()

	ac, err := akashclient.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("kube: error creating manifest client: %w", err)
	}

	spinner.Message(fmt.Sprintf("loading active leases for provider \"%s\"", cctx.FromAddress))
	spinner.StopMessage(fmt.Sprintf("loaded active leases for provider \"%s\"", cctx.FromAddress))
	_ = spinner.Start()

	activeLeases, err := queryActiveLeases(cmd.Context(), cctx)
	if err != nil {
		return err
	}
	_ = spinner.Stop()

	var oldMani []v2beta1.Manifest
	var oldHosts []v2beta1.ProviderHost
	var oldIPs []v2beta1.ProviderLeasedIP

	if dryRun {
		isEmpty, err := isDirEmpty(manifestsDryRunPath)
		if os.IsNotExist(err) {
			isEmpty = true
		} else if err != nil {
			return err
		}

		if isEmpty {
			isEmpty, err = isDirEmpty(hostsDryRunPath)
			if os.IsNotExist(err) {
				isEmpty = true
			} else if err != nil {
				return err
			}
		}

		if isEmpty {
			isEmpty, err = isDirEmpty(ipsDryRunPath)
			if os.IsNotExist(err) {
				isEmpty = true
			} else if err != nil {
				return err
			}
		}

		var yes bool

		if !isEmpty && !restoreOnly {
			yes, err = cli.GetConfirmation(cmd, "previous dry run already present. \"y\" to override. \"N\" to exit")
			if err != nil {
				return err
			}

			if yes {
				_ = os.RemoveAll(backupPath)
			} else {
				return nil
			}
		}
	}

	if !restoreOnly {
		isEmpty, err := isDirEmpty(manifestsBackupPath)
		if os.IsNotExist(err) {
			isEmpty = true
		} else if err != nil {
			return err
		}

		if isEmpty {
			isEmpty, err = isDirEmpty(hostsBackupPath)
			if os.IsNotExist(err) {
				isEmpty = true
			} else if err != nil {
				return err
			}
		}

		if isEmpty {
			isEmpty, err = isDirEmpty(ipsBackupPath)
			if os.IsNotExist(err) {
				isEmpty = true
			} else if err != nil {
				return err
			}
		}

		yes := true

		if !isEmpty {
			yes, err = cli.GetConfirmation(cmd, "backup already present. \"y\" to remove. \"N\" jump to restore. Ctrl+C exit")
			if err != nil {
				return err
			}

			if yes {
				_ = os.RemoveAll(backupPath)
			}
		}

		if yes {
			spinner.Message("loading CRDs")
			spinner.StopMessage("loaded CRDs")
			_ = spinner.Start()
			mList, err := ac.AkashV2beta1().Manifests(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}

			hList, err := ac.AkashV2beta1().ProviderHosts(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}

			iList, err := ac.AkashV2beta1().ProviderLeasedIPs(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}

			_ = spinner.Stop()

			if len(mList.Items) == 0 && len(hList.Items) == 0 && len(iList.Items) == 0 {
				fmt.Println("no v2beta1 objects found. nothing to backup")
				return nil
			}

			oldMani = mList.Items
			oldHosts = hList.Items
			oldIPs = iList.Items

			if len(mList.Items) > 0 {
				spinner.Message("backup manifests")
				spinner.StopMessage(fmt.Sprintf("backup manifests DONE: %d", len(mList.Items)))

				_ = spinner.Start()
				_ = os.MkdirAll(manifestsBackupPath, 0755)
				for i := range oldMani {
					data, _ := json.MarshalIndent(&oldMani[i], "", "  ")
					if err = backupObject(manifestsBackupPath+"/"+oldMani[i].Name+".yaml", data); err != nil {
						return err
					}

					if backupOnly || !dryRun {
						_ = ac.AkashV2beta1().Manifests(ns).Delete(ctx, oldMani[i].Name, metav1.DeleteOptions{})
					}
				}
				_ = spinner.Stop()
			}

			if len(hList.Items) > 0 {
				spinner.Message("backup providers hosts")
				spinner.StopMessage(fmt.Sprintf("backup providers hosts DONE: %d", len(hList.Items)))

				_ = spinner.Start()
				_ = os.MkdirAll(hostsBackupPath, 0755)
				for i := range oldHosts {
					data, _ := json.MarshalIndent(&oldHosts[i], "", "  ")
					if err = backupObject(hostsBackupPath+"/"+oldHosts[i].Name+".yaml", data); err != nil {
						return err
					}

					if backupOnly || !dryRun {
						_ = ac.AkashV2beta1().ProviderHosts(ns).Delete(ctx, oldHosts[i].Name, metav1.DeleteOptions{})
					}
				}
				_ = spinner.Stop()
			}

			if len(iList.Items) > 0 {
				spinner.Message("backup IP addresses")
				spinner.StopMessage(fmt.Sprintf("backup IP addresses DONE: %d", len(iList.Items)))

				_ = spinner.Start()

				_ = os.MkdirAll(ipsBackupPath, 0755)
				for i := range oldIPs {
					data, _ := json.MarshalIndent(&oldIPs[i], "", "  ")
					if err = backupObject(ipsBackupPath+"/"+oldIPs[i].Name+".yaml", data); err != nil {
						return err
					}

					if backupOnly || !dryRun {
						_ = ac.AkashV2beta1().ProviderLeasedIPs(ns).Delete(ctx, oldIPs[i].Name, metav1.DeleteOptions{})
					}
				}
				_ = spinner.Stop()
			}
		}
	}

	if len(oldMani) == 0 {
		oldMani, err = loadObjects[v2beta1.Manifest](manifestsBackupPath)
		if err != nil {
			return err
		}
	}

	if len(oldHosts) == 0 {
		oldHosts, err = loadObjects[v2beta1.ProviderHost](hostsBackupPath)
		if err != nil {
			return err
		}
	}

	if len(oldIPs) == 0 {
		oldIPs, err = loadObjects[v2beta1.ProviderLeasedIP](ipsBackupPath)
		if err != nil {
			return err
		}
	}

	if !dryRun {
		spinner.Message("applying CRDs")
		spinner.StopMessage("applied CRDs")
		spinner.StopFailMessage("failed to apply CRDs")

		_ = spinner.Start()

		_ = kubectl(cmd, "delete", string(crdOld), kubeConfig)

		if err = kubectl(cmd, "apply", string(crdNew), kubeConfig); err != nil {
			return err
		}

		_ = spinner.Stop()
	}

	if len(oldMani) > 0 {
		spinner.Message("migrating manifests to v2beta2")
		spinner.StopMessage("migrated manifests to v2beta2")
		spinner.StopFailMessage("failed to migrate manifests to v2beta2")

		_ = spinner.Start()

		var errs []error
		for _, mani := range oldMani {
			olid, _ := mani.Spec.LeaseID.ToAkash()
			nlid := mmigrate.LeaseIDFromV1beta2(olid)

			gspec, exists := activeLeases[nlid]
			if !exists {
				errs = append(errs, fmt.Errorf("no groupspec for lease %s", nlid)) // nolint goerr113
				continue
			}

			nmani := &v2beta2.Manifest{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Manifest",
					APIVersion: "akash.network/v2beta2",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      mani.Name,
					Labels:    mani.Labels,
				},
			}

			nmani.Spec, err = v2beta2migrate.ManifestSpecFromV2beta1(gspec.Resources, mani.Spec)
			if err != nil {
				errs = append(errs, err)
			} else {
				if dryRun {
					_ = os.MkdirAll(manifestsDryRunPath, 0755)
					data, _ := json.MarshalIndent(nmani, "", "  ")
					if err = backupObject(manifestsDryRunPath+"/"+nmani.Name+".yaml", data); err != nil {
						return err
					}
				} else {
					// double check this manifest not present in the new api
					_, err = ac.AkashV2beta2().Manifests(ns).Get(ctx, mani.Name, metav1.GetOptions{})
					if err != nil && !kubeErrors.IsNotFound(err) {
						fmt.Printf("unable to check presence of \"%s\" manifest. still trying to migrate. %s\n", mani.Name, err.Error())
					}

					if _, err = ac.AkashV2beta2().Manifests(ns).Create(ctx, nmani, metav1.CreateOptions{}); err != nil {
						errs = append(errs, err)
					}
				}
			}
		}

		if len(errs) == 0 {
			_ = spinner.Stop()
		} else {
			_ = spinner.StopFail()

			fmt.Printf("some manifest migrations have failed:\n")
			for _, e := range errs {
				fmt.Printf("\t%s\n", e.Error())
			}
		}
	}

	if len(oldHosts) > 0 {
		spinner.Message("migrating hosts to v2beta2")
		spinner.StopMessage("migrated hosts to v2beta2")
		spinner.StopFailMessage("failed migrate to hosts to v2beta2")

		_ = spinner.Start()

		var errs []error

		for _, host := range oldHosts {
			nhost := &v2beta2.ProviderHost{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ProviderHost",
					APIVersion: "akash.network/v2beta2",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      host.Name,
					Namespace: ns,
				},
			}

			nhost.Labels = host.Labels
			nhost.Spec = v2beta2migrate.ProviderHostsSpecFromV2beta1(host.Spec)

			if dryRun {
				_ = os.MkdirAll(hostsDryRunPath, 0755)
				data, _ := json.MarshalIndent(nhost, "", "  ")
				if err = backupObject(hostsDryRunPath+"/"+nhost.Name+".yaml", data); err != nil {
					return err
				}
			} else {
				// double check this manifest not present in the new api
				_, err = ac.AkashV2beta2().ProviderHosts(ns).Get(ctx, host.Name, metav1.GetOptions{})
				if err != nil && !kubeErrors.IsNotFound(err) {
					fmt.Printf("unable to check presence of \"%s\" provider hosts. still trying to migrate. %s\n", host.Name, err.Error())
				}

				if _, err = ac.AkashV2beta2().ProviderHosts(ns).Create(ctx, nhost, metav1.CreateOptions{}); err != nil {
					errs = append(errs, err)
				}
			}
		}

		if len(errs) == 0 {
			_ = spinner.Stop()
		} else {
			_ = spinner.StopFail()

			fmt.Printf("some provider hosts migrations have failed:\n")
			for _, e := range errs {
				fmt.Printf("\t%s\n", e.Error())
			}
		}
	}

	if len(oldIPs) > 0 {
		spinner.Message("migrating IP addresses to v2beta2")
		spinner.StopMessage("migrated IP addresses to v2beta2")
		spinner.StopFailMessage("failed to migrate IP addresses to v2beta2")

		_ = spinner.Start()

		var errs []error

		for _, host := range oldIPs {
			nIPs := &v2beta2.ProviderLeasedIP{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ProviderLeasedIP",
					APIVersion: "akash.network/v2beta2",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      host.Name,
					Namespace: ns,
				},
			}

			nIPs.Labels = host.Labels
			nIPs.Spec = v2beta2migrate.ProviderIPsSpecFromV2beta1(host.Spec)

			if dryRun {
				_ = os.MkdirAll(ipsDryRunPath, 0755)
				data, _ := json.MarshalIndent(nIPs, "", "  ")
				if err = backupObject(ipsDryRunPath+"/"+nIPs.Name+".yaml", data); err != nil {
					return err
				}
			} else {
				// double check this manifest not present in the new api
				_, err = ac.AkashV2beta2().ProviderLeasedIPs(ns).Get(ctx, host.Name, metav1.GetOptions{})
				if err != nil && !kubeErrors.IsNotFound(err) {
					fmt.Printf("unable to check presence of \"%s\" leased IPs. still trying to migrate. %s\n", host.Name, err.Error())
				}

				if _, err = ac.AkashV2beta2().ProviderLeasedIPs(ns).Create(ctx, nIPs, metav1.CreateOptions{}); err != nil {
					errs = append(errs, err)
				}
			}
		}

		if len(errs) == 0 {
			_ = spinner.Stop()
		} else {
			_ = spinner.StopFail()

			fmt.Printf("some IP address migrations have failed:\n")
			for _, e := range errs {
				fmt.Printf("\t%s\n", e.Error())
			}
		}
	}

	return nil
}

func isDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = f.Close()
	}()

	// read in ONLY one file
	_, err = f.Readdir(1)

	// and if the file is EOF... well, the dir is empty.
	if err == io.EOF {
		return true, nil
	}

	return false, nil
}

func backupObject(path string, data []byte) error {
	fl, err := os.Create(path)
	if err != nil {
		return err
	}

	defer func() {
		_ = fl.Close()
	}()

	_, err = fl.Write(data)
	if err != nil {
		return err
	}

	return nil
}

func loadObjects[C v2beta1.Manifest | v2beta1.ProviderHost | v2beta1.ProviderLeasedIP](path string) ([]C, error) {
	files, err := os.ReadDir(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var res []C

	for _, fl := range files {
		if fl.IsDir() || !strings.HasSuffix(fl.Name(), ".yaml") {
			continue
		}

		obj := *new(C)

		if err = readObject(path+"/"+fl.Name(), &obj); err == nil {
			res = append(res, obj)
		} else {
			fmt.Printf("error reading provider hosts from \"%s\". %s", fl.Name(), err.Error())
		}
	}

	return res, nil
}

func readObject(path string, obj interface{}) error {
	fl, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = fl.Close()
	}()

	data, err := io.ReadAll(fl)
	if err != nil {
		return err
	}

	if err = json.Unmarshal(data, &obj); err != nil {
		return err
	}

	return nil
}

func isKubectlAvail() bool {
	_, err := exec.LookPath("kubectl")
	return err == nil
}

func kubectl(cmd *cobra.Command, command, content string, kubeconfig string) error {
	exe := exec.CommandContext(cmd.Context(), "kubectl", command, "-f", "-")

	exe.Stdin = bytes.NewBufferString(content)
	exe.Stdout = cmd.OutOrStdout()
	exe.Stderr = cmd.ErrOrStderr()

	if len(kubeconfig) != 0 {
		exe.Env = []string{
			"KUBECONFIG=" + kubeconfig,
		}
	}

	return exe.Run()
}

func readOrDownload(path string) ([]byte, error) {
	var stream io.ReadCloser

	defer func() {
		if stream != nil {
			_ = stream.Close()
		}
	}()

	if strings.HasPrefix(path, "http") {
		resp, err := http.Get(path) // nolint: gosec
		if err != nil {
			return nil, err
		}

		stream = resp.Body
	} else {
		fl, err := os.Open(path)
		if err != nil {
			return nil, err
		}

		stream = fl
	}

	return io.ReadAll(stream)
}
