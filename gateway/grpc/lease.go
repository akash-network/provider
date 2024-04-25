package grpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"

	"github.com/akash-network/provider/cluster/types/v1beta3"
)

var ErrNoRunningPods = errors.New("no running pods")

func (*server) SendManifest(context.Context, *leasev1.SendManifestRequest) (*leasev1.SendManifestResponse, error) {
	panic("unimplemented")
}

func (*server) ServiceLogs(context.Context, *leasev1.ServiceLogsRequest) (*leasev1.ServiceLogsResponse, error) {
	panic("unimplemented")
}

func (*server) ServiceStatus(context.Context, *leasev1.ServiceStatusRequest) (*leasev1.ServiceStatusResponse, error) {
	panic("unimplemented")
}

func (s *server) StreamServiceLogs(r *leasev1.ServiceLogsRequest, strm leasev1.LeaseRPC_StreamServiceLogsServer) error {
	var (
		ctx = strm.Context()
		id  = r.GetLeaseId()

		// TODO(andrewhare): What is the format of services?
		services = strings.Join(r.GetServices(), " ")

		// TODO(andrewhare): Where does lines come from? Is it ignored in follow mode?
		lines = int64(111)
	)

	logs, err := s.rc.LeaseLogs(ctx, id, services, true, &lines)
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
		go func(l *v1beta3.ServiceLog) {
			defer l.Stream.Close()

			for l.Scanner.Scan() {
				if ctxErr := ctx.Err(); ctxErr != nil {
					if !isContextDoneErr(ctxErr) {
						errs <- fmt.Errorf("ctx err: %w", ctxErr)
					}
					return
				}

				var (
					buf  = l.Scanner.Bytes()
					logs = make([]byte, len(buf))
				)

				copy(logs, buf)

				resp := leasev1.ServiceLogsResponse{
					Services: []*leasev1.ServiceLogs{
						{
							Name: l.Name,
							Logs: buf,
						},
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

			errs <- nil
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

func (*server) StreamServiceStatus(*leasev1.ServiceStatusRequest, leasev1.LeaseRPC_StreamServiceStatusServer) error {
	panic("unimplemented")
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

func isContextDoneErr(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
