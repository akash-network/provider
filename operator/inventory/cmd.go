package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cskr/pubsub"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/gorilla/mux"
	"github.com/ovrclk/akash/util/runner"
	rookclientset "github.com/rook/rook/pkg/client/clientset/versioned"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	cmdutil "github.com/ovrclk/provider-services/cmd/provider-services/cmd/util"

	"github.com/ovrclk/provider-services/cluster/kube/clientcommon"
	providerflags "github.com/ovrclk/provider-services/cmd/provider-services/cmd/flags"
	akashv2beta1 "github.com/ovrclk/provider-services/pkg/apis/akash.network/v2beta1"
	akashclientset "github.com/ovrclk/provider-services/pkg/client/clientset/versioned"
)

func CmdSetContextValue(cmd *cobra.Command, key, val interface{}) {
	cmd.SetContext(context.WithValue(cmd.Context(), key, val))
}

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "inventory",
		Short:        "kubernetes operator interfacing inventory",
		Args:         cobra.ExactArgs(0),
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			zconf := zap.NewDevelopmentConfig()
			zconf.DisableCaller = true
			zconf.EncoderConfig.EncodeTime = func(time.Time, zapcore.PrimitiveArrayEncoder) {}

			zapLog, _ := zconf.Build()

			cmd.SetContext(logr.NewContext(cmd.Context(), zapr.NewLogger(zapLog)))

			if err := loadKubeConfig(cmd); err != nil {
				return err
			}

			kubecfg := KubeConfigFromCtx(cmd.Context())

			clientset, err := kubernetes.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			rc, err := rookclientset.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			ac, err := akashclientset.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			group, ctx := errgroup.WithContext(cmd.Context())
			cmd.SetContext(ctx)

			CmdSetContextValue(cmd, CtxKeyKubeClientSet, clientset)
			CmdSetContextValue(cmd, CtxKeyRookClientSet, rc)
			CmdSetContextValue(cmd, CtxKeyAkashClientSet, ac)
			CmdSetContextValue(cmd, CtxKeyPubSub, pubsub.New(1000))
			CmdSetContextValue(cmd, CtxKeyErrGroup, group)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			bus := PubSubFromCtx(cmd.Context())
			group := ErrGroupFromCtx(cmd.Context())

			var storage []Storage
			st, err := NewCeph(cmd.Context())
			if err != nil {
				return err
			}
			storage = append(storage, st)

			if st, err = NewRancher(cmd.Context()); err != nil {
				return err
			}
			storage = append(storage, st)

			CmdSetContextValue(cmd, CtxKeyStorage, storage)

			apiTimeout, _ := cmd.Flags().GetDuration(FlagAPITimeout)
			queryTimeout, _ := cmd.Flags().GetDuration(FlagQueryTimeout)
			port, _ := cmd.Flags().GetUint16(FlagAPIPort)

			srv := &http.Server{
				Addr:    fmt.Sprintf(":%d", port),
				Handler: newRouter(LogFromCtx(cmd.Context()).WithName("router"), apiTimeout, queryTimeout),
				BaseContext: func(_ net.Listener) context.Context {
					return cmd.Context()
				},
			}

			group.Go(func() error {
				return srv.ListenAndServe()
			})

			group.Go(func() error {
				select {
				case <-cmd.Context().Done():
				}
				return srv.Shutdown(cmd.Context())
			})

			factory := informers.NewSharedInformerFactory(KubeClientFromCtx(cmd.Context()), 0)

			InformKubeObjects(cmd.Context(),
				bus,
				factory.Core().V1().Namespaces().Informer(),
				"ns")

			InformKubeObjects(cmd.Context(),
				bus,
				factory.Storage().V1().StorageClasses().Informer(),
				"sc")

			InformKubeObjects(cmd.Context(),
				bus,
				factory.Core().V1().PersistentVolumes().Informer(),
				"pv")

			InformKubeObjects(cmd.Context(),
				bus,
				factory.Core().V1().Nodes().Informer(),
				"nodes")

			return group.Wait()
		},
	}

	err := providerflags.AddKubeConfigPathFlag(cmd)
	if err != nil {
		panic(err)
	}

	cmd.Flags().Duration(FlagAPITimeout, 3*time.Second, "api timeout")
	if err = viper.BindPFlag(FlagAPITimeout, cmd.Flags().Lookup(FlagAPITimeout)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(FlagQueryTimeout, 2*time.Second, "query timeout")
	if err = viper.BindPFlag(FlagQueryTimeout, cmd.Flags().Lookup(FlagQueryTimeout)); err != nil {
		panic(err)
	}

	cmd.Flags().Uint16(FlagAPIPort, 8080, "port to REST api")
	if err = viper.BindPFlag(FlagAPIPort, cmd.Flags().Lookup(FlagAPIPort)); err != nil {
		panic(err)
	}

	return cmd
}

func loadKubeConfig(c *cobra.Command) error {
	configPath, _ := c.Flags().GetString(providerflags.FlagKubeConfig)

	config, err := clientcommon.OpenKubeConfig(configPath, cmdutil.OpenLogger().With("cmp", "provider"))
	if err != nil {
		return err
	}

	CmdSetContextValue(c, CtxKeyKubeConfig, config)

	return nil
}

func newRouter(_ logr.Logger, apiTimeout, queryTimeout time.Duration) *mux.Router {
	router := mux.NewRouter()

	router.Use(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rCtx, cancel := context.WithTimeout(r.Context(), apiTimeout)
			defer cancel()

			h.ServeHTTP(w, r.WithContext(rCtx))
		})
	})

	router.HandleFunc("/inventory", func(w http.ResponseWriter, req *http.Request) {
		storage := StorageFromCtx(req.Context())
		inv := akashv2beta1.Inventory{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Inventory",
				APIVersion: "akash.network/v2beta1",
			},
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Now().UTC()),
			},
			Spec: akashv2beta1.InventorySpec{},
			Status: akashv2beta1.InventoryStatus{
				State: akashv2beta1.InventoryStatePulled,
			},
		}

		var data []byte

		ctx, cancel := context.WithTimeout(req.Context(), queryTimeout)
		defer func() {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				w.WriteHeader(http.StatusRequestTimeout)
			}

			if len(data) > 0 {
				_, _ = w.Write(data)
			}
		}()

		datach := make(chan runner.Result, 1)
		var wg sync.WaitGroup

		wg.Add(len(storage))

		for idx := range storage {
			go func(idx int) {
				defer wg.Done()

				datach <- runner.NewResult(storage[idx].Query(ctx))
			}(idx)
		}

		go func() {
			defer cancel()
			wg.Wait()
		}()

	done:
		for {
			select {
			case <-ctx.Done():
				break done
			case res := <-datach:
				if res.Error() != nil {
					inv.Status.Messages = append(inv.Status.Messages, res.Error().Error())
				}

				if inventory, valid := res.Value().([]akashv2beta1.InventoryClusterStorage); valid {
					inv.Spec.Storage = append(inv.Spec.Storage, inventory...)
				}
			}
		}

		var err error
		if data, err = json.Marshal(&inv); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data = []byte(err.Error())
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
	})

	return router
}
