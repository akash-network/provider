package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/jaypipes/ghw/pkg/memory"
	"github.com/jaypipes/ghw/pkg/pci"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
)

const (
	flagAPIPort = "api-port"
)

type hwInfo struct {
	Errors []string     `json:"errors"`
	CPU    *cpu.Info    `json:"cpu,omitempty"`
	Memory *memory.Info `json:"memory,omitempty"`
	GPU    *gpu.Info    `json:"gpu,omitempty"`
	PCI    *pci.Info    `json:"pci,omitempty"`
}

func cmdPsutil() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "psutil",
		Short:        "dump node hardware spec",
		Args:         cobra.ExactArgs(0),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}

	cmd.AddCommand(cmdPsutilList())
	cmd.AddCommand(cmdPsutilServe())

	return cmd
}

func cmdPsutilServe() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "serve",
		Short:        "dump node hardware spec via REST",
		Args:         cobra.ExactArgs(0),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			router := mux.NewRouter()

			router.HandleFunc("/", infoHandler).Methods(http.MethodGet)
			router.HandleFunc("/cpu", cpuInfoHandler).Methods(http.MethodGet)
			router.HandleFunc("/gpu", gpuHandler).Methods(http.MethodGet)
			router.HandleFunc("/memory", memoryHandler).Methods(http.MethodGet)
			router.HandleFunc("/pci", pciHandler).Methods(http.MethodGet)

			port := viper.GetUint16(flagAPIPort)

			group, ctx := errgroup.WithContext(cmd.Context())

			endpoint := fmt.Sprintf(":%d", port)

			srv := &http.Server{
				Addr:    endpoint,
				Handler: router,
				BaseContext: func(_ net.Listener) context.Context {
					return ctx
				},
				ReadHeaderTimeout: 5 * time.Second,
			}

			group.Go(func() error {
				fmt.Printf("listening on %s\n", endpoint)

				return srv.ListenAndServe()
			})

			group.Go(func() error {
				<-ctx.Done()

				fmt.Printf("received shutdown signal\n")

				_ = srv.Shutdown(context.Background())
				return ctx.Err()
			})

			err := group.Wait()
			if !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
				return err
			}

			return nil
		},
	}

	cmd.Flags().Uint16(flagAPIPort, 8081, "api port")
	if err := viper.BindPFlag(flagAPIPort, cmd.Flags().Lookup(flagAPIPort)); err != nil {
		panic(err)
	}

	return cmd
}

func cmdPsutilList() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "dump node hardware spec into stdout",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			var res interface{}
			var err error
			switch args[0] {
			case "cpu":
				res, err = cpu.New()
			case "gpu":
				res, err = gpu.New()
			case "memory":
				res, err = memory.New()
			case "pci":
				res, err = pci.New()
			default:
				return fmt.Errorf("invalid command \"%s\"", args[0]) // nolint: err113
			}

			if err != nil {
				return err
			}

			data, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}

			fmt.Printf("%s\n", string(data))

			return nil
		},
	}

	return cmd
}

func infoHandler(w http.ResponseWriter, _ *http.Request) {
	res := &hwInfo{}
	var err error

	res.CPU, err = cpu.New()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	}

	res.GPU, err = gpu.New()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	}

	res.Memory, err = memory.New()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	}

	res.PCI, err = pci.New()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	}

	writeJSON(w, res)
}

func cpuInfoHandler(w http.ResponseWriter, _ *http.Request) {
	res, err := cpu.New()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, res)
}

func gpuHandler(w http.ResponseWriter, _ *http.Request) {
	res, err := gpu.New()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, res)
}

func memoryHandler(w http.ResponseWriter, _ *http.Request) {
	res, err := memory.New()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, res)
}

func pciHandler(w http.ResponseWriter, _ *http.Request) {
	res, err := pci.New()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, res)
}

func writeJSON(w http.ResponseWriter, obj interface{}) {
	bytes, err := json.Marshal(obj)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	_, err = w.Write(bytes)
	if err != nil {
		return
	}
}
