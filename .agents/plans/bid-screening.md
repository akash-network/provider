# Bid Screening Implementation Plan

## Overview
Implement the `BidScreening` gRPC endpoint that allows tenants to submit a `GroupSpec` and receive a response indicating whether the provider would bid on it and at what price. Uses the same code paths as real bidding but with dry-run inventory adjustment.

## Architecture
```
gRPC Server (gateway/grpc/server.go)
  → Provider Service (service.go)
    → Bid Engine (bidengine/service.go)
      → CheckBidEligibility (bidengine/order.go - extracted)
      → Cluster dry-run reserve (cluster/service.go → cluster/inventory.go)
        → inventoryService.Adjust(WithDryRun())
      → PricingStrategy.CalculatePrice()
    → Assemble BidScreeningResponse
```

## Files to Modify

### 1. `cluster/service.go` — Add `DryRunReserve` to `Cluster` interface

Add `DryRunReserve(dtypes.ResourceGroup) (ctypes.ReservationGroup, error)` to the `Cluster` interface.

Implement on `service` struct:
```go
func (s *service) DryRunReserve(resources dtypes.ResourceGroup) (ctypes.ReservationGroup, error) {
    return s.inventory.dryRunReserve(resources)
}
```

### 2. `cluster/inventory.go` — Add dry-run reserve support

Add a `dryRunReservech` channel to `inventoryService` struct.

Add `dryRunReserve` method:
```go
func (is *inventoryService) dryRunReserve(resources dtypes.ResourceGroup) (ctypes.ReservationGroup, error) {
    for idx, res := range resources.GetResourceUnits() {
        if res.CPU == nil {
            return nil, fmt.Errorf("%w: CPU resource at idx %d is nil", ErrInvalidResource, idx)
        }
        if res.GPU == nil {
            return nil, fmt.Errorf("%w: GPU resource at idx %d is nil", ErrInvalidResource, idx)
        }
        if res.Memory == nil {
            return nil, fmt.Errorf("%w: Memory resource at idx %d is nil", ErrInvalidResource, idx)
        }
    }

    ch := make(chan dryRunReserveResponse, 1)
    req := dryRunReserveRequest{
        resources: resources,
        ch:        ch,
    }

    select {
    case is.dryRunReservech <- req:
        response := <-ch
        return response.value, response.err
    case <-is.lc.ShuttingDown():
        return nil, ErrNotRunning
    }
}
```

Add new request/response types:
```go
type dryRunReserveRequest struct {
    resources dtypes.ResourceGroup
    ch        chan<- dryRunReserveResponse
}

type dryRunReserveResponse struct {
    value ctypes.ReservationGroup
    err   error
}
```

Add handler:
```go
func (is *inventoryService) handleDryRunRequest(req dryRunReserveRequest, state *inventoryServiceState) {
    resourcesToCommit := is.resourcesToCommit(req.resources)

    rg := &dryRunReservation{resources: resourcesToCommit}

    err := state.inventory.Adjust(rg, ctypes.WithDryRun())
    if err != nil {
        req.ch <- dryRunReserveResponse{err: err}
        return
    }

    req.ch <- dryRunReserveResponse{value: rg}
}
```

Add lightweight `dryRunReservation` type implementing `ctypes.ReservationGroup`:
```go
type dryRunReservation struct {
    resources         dtypes.ResourceGroup
    adjustedResources dtypes.ResourceUnits
    clusterParams     interface{}
}

func (r *dryRunReservation) Resources() dtypes.ResourceGroup         { return r.resources }
func (r *dryRunReservation) SetAllocatedResources(val dtypes.ResourceUnits) { r.adjustedResources = val }
func (r *dryRunReservation) GetAllocatedResources() dtypes.ResourceUnits    { return r.adjustedResources }
func (r *dryRunReservation) SetClusterParams(val interface{})               { r.clusterParams = val }
func (r *dryRunReservation) ClusterParams() interface{}                     { return r.clusterParams }
```

Wire `dryRunReservech` into `newInventoryService` and into the `run()` select loop (alongside `reservech`).

### 3. `bidengine/order.go` — Extract `shouldBid` to `CheckBidEligibility`

Extract into a standalone function that collects reasons:
```go
func CheckBidEligibility(
    gspec *dtypes.GroupSpec,
    providerAttrs atrtypes.Attributes,
    bidAttrs atrtypes.Attributes,
    maxGroupVolumes int,
    pass ProviderAttrSignatureService,
    providerOwner string,
) (bool, []string, error) {
    var reasons []string

    if !gspec.MatchAttributes(providerAttrs) {
        reasons = append(reasons, "incompatible provider attributes")
    }

    if !bidAttrs.SubsetOf(gspec.Requirements.Attributes) {
        reasons = append(reasons, "incompatible order attributes")
    }

    attr, err := pass.GetAttributes()
    if err != nil {
        return false, nil, err
    }

    if !gspec.MatchResourcesRequirements(attr) {
        reasons = append(reasons, "incompatible attributes for resources requirements")
    }

    for _, resources := range gspec.GetResourceUnits() {
        if len(resources.Storage) > maxGroupVolumes {
            reasons = append(reasons, fmt.Sprintf("group volumes count exceeds limit (%d > %d)", len(resources.Storage), maxGroupVolumes))
        }
    }

    signatureRequirements := gspec.Requirements.SignedBy
    if signatureRequirements.Size() != 0 {
        var provAttr atypes.AuditedProviders
        ownAttrs := atypes.AuditedProvider{
            Owner:      providerOwner,
            Auditor:    "",
            Attributes: providerAttrs,
        }
        provAttr = append(provAttr, ownAttrs)
        auditors := make([]string, 0)
        auditors = append(auditors, gspec.Requirements.SignedBy.AllOf...)
        auditors = append(auditors, gspec.Requirements.SignedBy.AnyOf...)

        gotten := make(map[string]struct{})
        for _, auditor := range auditors {
            if _, done := gotten[auditor]; done {
                continue
            }
            result, err := pass.GetAuditorAttributeSignatures(auditor)
            if err != nil {
                return false, nil, err
            }
            provAttr = append(provAttr, result...)
            gotten[auditor] = struct{}{}
        }

        if !gspec.MatchRequirements(provAttr) {
            reasons = append(reasons, "attribute signature requirements not met")
        }
    }

    if err := gspec.ValidateBasic(); err != nil {
        reasons = append(reasons, fmt.Sprintf("group validation error: %v", err))
    }

    if len(reasons) > 0 {
        return false, reasons, nil
    }
    return true, nil, nil
}
```

Refactor `order.shouldBid` to delegate:
```go
func (o *order) shouldBid(group *dtypes.Group) (bool, error) {
    passed, reasons, err := CheckBidEligibility(
        &group.GroupSpec,
        o.session.Provider().Attributes,
        o.cfg.Attributes,
        o.cfg.MaxGroupVolumes,
        o.pass,
        o.session.Provider().Owner,
    )
    if err != nil {
        return false, err
    }
    for _, reason := range reasons {
        o.log.Debug("unable to fulfill: " + reason)
    }
    return passed, nil
}
```

### 4. `bidengine/service.go` — Add `ScreenBid` to Service interface

Add to interface:
```go
type Service interface {
    StatusClient
    ScreenBid(ctx context.Context, gspec *dtypes.GroupSpec) (*ScreenBidResult, error)
    Close() error
    Done() <-chan struct{}
}
```

Add result type (in bidengine package, new file or in service.go):
```go
type ScreenBidResult struct {
    Passed             bool
    Reasons            []string
    AllocatedResources dtypes.ResourceUnits
    ClusterParams      interface{}
    Price              sdk.DecCoin
}
```

Implement on `service` struct:
```go
func (s *service) ScreenBid(ctx context.Context, gspec *dtypes.GroupSpec) (*ScreenBidResult, error) {
    passed, reasons, err := CheckBidEligibility(
        gspec,
        s.session.Provider().Attributes,
        s.cfg.Attributes,
        s.cfg.MaxGroupVolumes,
        s.pass,
        s.session.Provider().Owner,
    )
    if err != nil {
        return nil, err
    }
    if !passed {
        return &ScreenBidResult{Passed: false, Reasons: reasons}, nil
    }

    rg, err := s.cluster.DryRunReserve(gspec)
    if err != nil {
        return &ScreenBidResult{
            Passed:  false,
            Reasons: []string{fmt.Sprintf("insufficient capacity: %v", err)},
        }, nil
    }

    priceReq := Request{
        GSpec:              gspec,
        PricePrecision:     DefaultPricePrecision,
        AllocatedResources: rg.GetAllocatedResources(),
    }

    price, err := s.cfg.PricingStrategy.CalculatePrice(ctx, priceReq)
    if err != nil {
        return &ScreenBidResult{
            Passed:  false,
            Reasons: []string{fmt.Sprintf("pricing calculation failed: %v", err)},
        }, nil
    }

    return &ScreenBidResult{
        Passed:             true,
        AllocatedResources: rg.GetAllocatedResources(),
        ClusterParams:      rg.ClusterParams(),
        Price:              price,
    }, nil
}
```

### 5. `service.go` — Add `ScreenBid` to provider Client interface

Add to interface:
```go
type Client interface {
    StatusClient
    Manifest() manifest.Client
    Cluster() cluster.Client
    Hostname() ctypes.HostnameServiceClient
    ClusterService() cluster.Service
    ScreenBid(ctx context.Context, req *providerv1.BidScreeningRequest) (*providerv1.BidScreeningResponse, error)
}
```

Implement on `service` struct:
```go
func (s *service) ScreenBid(ctx context.Context, req *providerv1.BidScreeningRequest) (*providerv1.BidScreeningResponse, error) {
    result, err := s.bidengine.ScreenBid(ctx, &req.GroupSpec)
    if err != nil {
        return nil, err
    }

    resp := &providerv1.BidScreeningResponse{
        Passed:  result.Passed,
        Reasons: result.Reasons,
    }

    if result.Passed {
        offer := mvbeta.ResourceOfferFromRU(result.AllocatedResources)
        resp.ResourceOffer = &offer
        resp.Price = &result.Price
    }

    return resp, nil
}
```

Need to add `mvbeta` import for `ResourceOfferFromRU`.

### 6. `gateway/grpc/server.go` — Implement BidScreening

Change `grpcProviderV1.client` type and `NewServer` parameter:
```go
type grpcProviderV1 struct {
    ctx    context.Context
    client provider.Client // was provider.StatusClient
}

func NewServer(ctx context.Context, endpoint string, cquery gwutils.CertGetter, client provider.Client) error {
```

Implement BidScreening:
```go
func (gm *grpcProviderV1) BidScreening(ctx context.Context, request *providerv1.BidScreeningRequest) (*providerv1.BidScreeningResponse, error) {
    return gm.client.ScreenBid(ctx, request)
}
```

### 7. `mocks/cluster/Cluster_mock.go` — Regenerate

Regenerate mock to include `DryRunReserve` method.

### 8. Build and verify

Run `make bins` to verify compilation.
