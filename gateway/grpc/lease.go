package grpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	kubeErrors "k8s.io/apimachinery/pkg/api/errors"

	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	"github.com/akash-network/provider/tools/fromctx"
)

var ErrNoRunningPods = errors.New("no running pods")

func (*server) SendManifest(context.Context, *leasev1.SendManifestRequest) (*leasev1.SendManifestResponse, error) {
	panic("unimplemented")
}

func (s *server) ServiceLogs(ctx context.Context, r *leasev1.ServiceLogsRequest) (*leasev1.ServiceLogsResponse, error) {
	var (
		id       = r.GetLeaseId()
		services = strings.Join(r.GetServices(), " ")
		lines    = int64(0)
	)

	logs, err := s.cc.LeaseLogs(ctx, id, services, true, &lines)
	if err != nil {
		return nil, fmt.Errorf("lease logs: %w", err)
	}

	if len(logs) == 0 {
		return nil, ErrNoRunningPods
	}

	var resp leasev1.ServiceLogsResponse
	for _, l := range logs {
		defer l.Stream.Close()

		for l.Scanner.Scan() {
			resp.Services = append(resp.Services,
				newLeaseV1ServiceLogs(l.Name, l.Scanner.Bytes()))
		}

		if err := l.Scanner.Err(); err != nil {
			return nil, fmt.Errorf("%s scanner: %w", l.Name, err)
		}
	}

	return &resp, nil
}

func (s *server) ServiceStatus(ctx context.Context, r *leasev1.ServiceStatusRequest) (*leasev1.ServiceStatusResponse, error) {
	ctx = fromctx.ApplyToContext(ctx, s.clusterSettings)

	id := r.GetLeaseId()
	found, m, err := s.cc.GetManifestGroup(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get manifest groups: %w", err)
	}

	if !found {
		return nil, status.Error(codes.NotFound, "lease does not exist")
	}

	i := getInfo(m)

	ips := make(map[string][]leasev1.LeaseIPStatus)
	if s.ip != nil && i.hasLeasedIPs {
		var statuses []ip.LeaseIPStatus
		if statuses, err = s.ip.GetIPAddressStatus(ctx, id.OrderID()); err != nil {
			return nil, fmt.Errorf("get ip address status: %w", err)
		}

		for _, stat := range statuses {
			ips[stat.ServiceName] = append(ips[stat.ServiceName], leasev1.LeaseIPStatus{
				Port:         stat.Port,
				ExternalPort: stat.ExternalPort,
				Protocol:     stat.Protocol,
				Ip:           stat.IP,
			})
		}
	}

	ports := make(map[string][]leasev1.ForwarderPortStatus)
	if i.hasForwardedPorts {
		var allStatuses map[string][]ctypes.ForwardedPortStatus
		if allStatuses, err = s.cc.ForwardedPortStatus(ctx, id); err != nil {
			return nil, fmt.Errorf("forward port status: %w", err)
		}

		for name, statuses := range allStatuses {
			for _, stat := range statuses {
				ports[name] = append(ports[name], leasev1.ForwarderPortStatus{
					Name:         stat.Name,
					Host:         stat.Host,
					Port:         uint32(stat.Port),
					ExternalPort: uint32(stat.ExternalPort),
					Proto:        string(stat.Proto),
				})
			}
		}
	}

	serviceStatus, err := s.cc.LeaseStatus(ctx, id)
	switch {
	case errors.Is(err, kubeclienterrors.ErrNoDeploymentForLease):
		return nil, status.Error(codes.NotFound, "no deployment for lease")
	case errors.Is(err, kubeclienterrors.ErrLeaseNotFound):
		return nil, status.Error(codes.NotFound, "lease does not exist")
	case kubeErrors.IsNotFound(err):
		return nil, status.Error(codes.NotFound, "not found")
	case err != nil:
		return nil, fmt.Errorf("lease status: %w", err)
	}

	var resp leasev1.ServiceStatusResponse
	for _, svc := range m.Services {
		name := svc.Name
		ss, ok := serviceStatus[name]
		if !ok {
			continue
		}

		resp.Services = append(resp.Services, leasev1.ServiceStatus{
			Name:  name,
			Ports: ports[name],
			Ips:   ips[name],
			Status: leasev1.LeaseServiceStatus{
				Available:          ss.Available,
				Total:              ss.Total,
				Uris:               ss.URIs,
				ObservedGeneration: ss.ObservedGeneration,
				Replicas:           ss.Replicas,
				UpdatedReplicas:    ss.UpdatedReplicas,
				ReadyReplicas:      ss.ReadyReplicas,
				AvailableReplicas:  ss.AvailableReplicas,
			},
		})
	}

	return &resp, nil
}

type manifestGroupInfo struct {
	hasLeasedIPs      bool
	hasForwardedPorts bool
}

func getInfo(m crd.ManifestGroup) manifestGroupInfo {
	var i manifestGroupInfo

	for _, s := range m.Services {
		for _, e := range s.Expose {
			if len(e.IP) > 0 {
				i.hasLeasedIPs = true
			}
			if e.Global && e.ExternalPort != 80 {
				i.hasForwardedPorts = true
			}
		}
	}

	return i
}

func (s *server) StreamServiceLogs(r *leasev1.ServiceLogsRequest, strm leasev1.LeaseRPC_StreamServiceLogsServer) error {
	var (
		ctx      = strm.Context()
		id       = r.GetLeaseId()
		services = strings.Join(r.GetServices(), " ")
		lines    = int64(0)
	)

	logs, err := s.cc.LeaseLogs(ctx, id, services, true, &lines)
	if err != nil {
		return fmt.Errorf("lease logs: %w", err)
	}

	if len(logs) == 0 {
		return ErrNoRunningPods
	}

	var (
		errs   = make(chan error, len(logs))
		stream = &safeLogStream{s: strm}
	)

	for _, ll := range logs {
		go func(l *ctypes.ServiceLog) {
			defer l.Stream.Close()
			defer func() { errs <- nil }()

			for l.Scanner.Scan() {
				select {
				case <-ctx.Done():
					return
				default:
					resp := leasev1.ServiceLogsResponse{
						Services: []*leasev1.ServiceLogs{
							newLeaseV1ServiceLogs(l.Name, l.Scanner.Bytes()),
						},
					}

					if sendErr := stream.Send(&resp); sendErr != nil {
						errs <- fmt.Errorf("stream send: %w", sendErr)
						return
					}
				}

				if scanErr := l.Scanner.Err(); scanErr != nil {
					errs <- fmt.Errorf("scanner err: %w", err)
					return
				}
			}
		}(ll)
	}

	var allErr error
	for i := 0; i < len(logs); i++ {
		allErr = errors.Join(err, <-errs)
	}
	if allErr != nil {
		return fmt.Errorf("scan all logs: %w", err)
	}

	return nil
}

func newLeaseV1ServiceLogs(name string, buf []byte) *leasev1.ServiceLogs {
	logs := make([]byte, len(buf))
	copy(logs, buf)

	return &leasev1.ServiceLogs{
		Name: name,
		Logs: buf,
	}
}

type safeLogStream struct {
	s  leasev1.LeaseRPC_StreamServiceLogsServer
	mu sync.Mutex
}

func (s *safeLogStream) Send(r *leasev1.ServiceLogsResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.s.Send(r); err != nil {
		return fmt.Errorf("safe log stream send: %w", err)
	}

	return nil
}

const defaultServiceStatusInterval = 5 * time.Second

func (s *server) StreamServiceStatus(r *leasev1.ServiceStatusRequest, strm leasev1.LeaseRPC_StreamServiceStatusServer) error {
	var (
		ctx = strm.Context()
		id  = r.GetLeaseId()
	)

	interval := defaultServiceStatusInterval

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		i := md.Get("interval")[0]

		var err error
		if interval, err = time.ParseDuration(i); err != nil {
			return fmt.Errorf("parse duration %s: %w", i, err)
		}
	}

	t := time.NewTicker(interval)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			res, err := s.ServiceStatus(ctx, &leasev1.ServiceStatusRequest{LeaseId: id})
			if err != nil {
				return fmt.Errorf("service status: %w", err)
			}

			strm.Send(res)
		}
	}
}
