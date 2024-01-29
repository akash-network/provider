package operator

import (
	"context"
	"encoding/json"
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
)

const (
	flagAPIPort = "api-port"
)

func cmdPsutil() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "psutil",
		Short:        "dump node hardware spec",
		Args:         cobra.ExactArgs(0),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {

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
		RunE: func(cmd *cobra.Command, args []string) error {
			router := mux.NewRouter()
			router.HandleFunc("/cpu", cpuInfoHandler).Methods(http.MethodGet)
			router.HandleFunc("/gpu", gpuHandler).Methods(http.MethodGet)
			router.HandleFunc("/memory", memoryHandler).Methods(http.MethodGet)
			router.HandleFunc("/pci", pciHandler).Methods(http.MethodGet)

			port := viper.GetUint16(flagAPIPort)

			srv := &http.Server{
				Addr:    fmt.Sprintf(":%d", port),
				Handler: router,
				BaseContext: func(_ net.Listener) context.Context {
					return cmd.Context()
				},
				ReadHeaderTimeout: 5 * time.Second,
			}

			return srv.ListenAndServe()
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
		RunE: func(cmd *cobra.Command, args []string) error {
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
				return fmt.Errorf("invalid command \"%s\"", args[0]) // nolint: goerr113
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
