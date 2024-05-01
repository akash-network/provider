package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"

	"github.com/akash-network/akash-api/go/manifest/v2beta2"
	ptypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	cmdcommon "github.com/akash-network/node/cmd/common"
	cutils "github.com/akash-network/node/x/cert/utils"
	dcli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	cltypes "github.com/akash-network/provider/cluster/types/v1beta3"

	aclient "github.com/akash-network/provider/client"
	gwgrpc "github.com/akash-network/provider/gateway/grpc"
	gwrest "github.com/akash-network/provider/gateway/rest"
)

func leaseStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-status",
		Short:        "get lease status",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doLeaseStatus(cmd)
		},
	}

	addLeaseFlags(cmd)

	return cmd
}

func doLeaseStatus(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	provAddr, err := providerFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	bid, err := mcli.BidIDFromFlags(cmd.Flags(), dcli.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	cert, err := cutils.LoadAndQueryCertificateForAccount(cmd.Context(), cctx, nil)
	if err != nil {
		return markRPCServerError(err)
	}

	prov, err := cl.Provider(ctx, &ptypes.QueryProviderRequest{Owner: provAddr.String()})
	if err != nil {
		return fmt.Errorf("query client provider: %w", err)
	}

	hostURIgRPC, err := grpcURI(prov.GetProvider().HostURI)
	if err != nil {
		return fmt.Errorf("grpc uri: %w", err)
	}

	ctxDial, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var leaseStatus gwrest.LeaseStatus

	client, err := gwgrpc.NewClient(ctxDial, hostURIgRPC, cert, cl)
	if err == nil {
		res, err := client.ServiceStatus(ctx, &leasev1.ServiceStatusRequest{
			LeaseId: bid.LeaseID(),
		})
		if err != nil {
			return fmt.Errorf("service status: %w", err)
		}

		leaseStatus = toLeaseStatus(res)
	} else {
		gclient, err := gwrest.NewClient(cl, provAddr, []tls.Certificate{cert})
		if err != nil {
			return err
		}

		leaseStatus, err = gclient.LeaseStatus(cmd.Context(), bid.LeaseID())
		if err != nil {
			return showErrorToUser(err)
		}

	}

	return cmdcommon.PrintJSON(cctx, leaseStatus)
}

func toLeaseStatus(r *leasev1.ServiceStatusResponse) gwrest.LeaseStatus {
	s := gwrest.LeaseStatus{
		Services: make(map[string]*cltypes.ServiceStatus),
	}

	for _, svc := range r.Services {
		s.Services[svc.Name] = &cltypes.ServiceStatus{
			Name:               svc.Name,
			Available:          svc.Status.Available,
			Total:              svc.Status.Total,
			URIs:               svc.Status.Uris,
			ObservedGeneration: svc.Status.ObservedGeneration,
			Replicas:           svc.Status.Replicas,
			UpdatedReplicas:    svc.Status.UpdatedReplicas,
			ReadyReplicas:      svc.Status.ReadyReplicas,
			AvailableReplicas:  svc.Status.AvailableReplicas,
		}

		if len(svc.Ports) > 0 {
			s.ForwardedPorts = make(map[string][]cltypes.ForwardedPortStatus)
		}
		for _, fp := range svc.Ports {
			s.ForwardedPorts[svc.Name] = append(s.ForwardedPorts[svc.Name], cltypes.ForwardedPortStatus{
				Host:         fp.Host,
				Port:         uint16(fp.Port),
				ExternalPort: uint16(fp.ExternalPort),
				Proto:        v2beta2.ServiceProtocol(fp.Proto),
				Name:         fp.Name,
			})
		}

		if len(svc.Ips) > 0 {
			s.IPs = make(map[string][]gwrest.LeasedIPStatus)
		}
		for _, ip := range svc.Ips {
			s.IPs[svc.Name] = append(s.IPs[svc.Name], gwrest.LeasedIPStatus{
				Port:         ip.Port,
				ExternalPort: ip.Port,
				Protocol:     ip.Protocol,
				IP:           ip.Ip,
			})
		}
	}

	return s
}
