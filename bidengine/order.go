package bidengine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/boz/go-lifecycle"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"cosmossdk.io/log"

	sdk "github.com/cosmos/cosmos-sdk/types"

	atypes "pkg.akt.dev/go/node/audit/v1"
	aclient "pkg.akt.dev/go/node/client/v1beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	deposit "pkg.akt.dev/go/node/types/deposit/v1"
	metricsutils "pkg.akt.dev/go/util/metrics"
	"pkg.akt.dev/go/util/pubsub"
	"pkg.akt.dev/go/util/runner"

	"github.com/akash-network/provider/cluster"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
)

// order manages bidding and general lifecycle handling of an order. The lifecycle includes:
type order struct {
	// orderID is the unique identifier for this order from the blockchain
	orderID mtypes.OrderID

	// cfg holds configuration parameters for bid engine (pricing, deposits, timeouts etc).
	cfg Config

	// session provides blockchain client and provider info.
	session session.Session

	// cluster interface to the provider's compute cluster for resource management.
	cluster cluster.Cluster

	// bus is the event bus for publishing lease events.
	bus pubsub.Bus

	// sub is the subscriber for receiving blockchain events.
	sub pubsub.Subscriber

	// reservationFulfilledNotify is the channel to notify when resources are reserved.
	reservationFulfilledNotify chan<- int

	// log is the logger instance
	log log.Logger

	// lc contains the lifecycle management for graceful startup/shutdown
	lc lifecycle.Lifecycle

	// pass is the service for validating provider attributes and signatures
	pass ProviderAttrSignatureService
}

var (
	pricingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:        "provider_bid_pricing_duration",
		Help:        "",
		ConstLabels: nil,
		Buckets:     prometheus.ExponentialBuckets(150000.0, 2.0, 10.0),
	})

	bidCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_bid",
		Help: "The total number of bids created",
	}, []string{"action", "result"})

	reservationDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:        "provider_reservation_duration",
		Help:        "",
		ConstLabels: nil,
		Buckets:     prometheus.ExponentialBuckets(150000.0, 2.0, 10.0),
	})

	reservationCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_reservation",
		Help: "",
	}, []string{"action", "result"})

	shouldBidCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_should_bid",
		Help: "",
	}, []string{"result"})

	orderCompleteCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_order_complete",
		Help: "",
	}, []string{"result"})
)

func newOrder(svc *service, oid mtypes.OrderID, cfg Config, pass ProviderAttrSignatureService, checkForExistingBid bool) (*order, error) {
	return newOrderInternal(svc, oid, cfg, pass, checkForExistingBid, nil)
}

func newOrderInternal(svc *service, oid mtypes.OrderID, cfg Config, pass ProviderAttrSignatureService, checkForExistingBid bool, reservationFulfilledNotify chan<- int) (*order, error) {
	// Create a subscription that will see all events that have not been read from e.sub.Events()
	sub, err := svc.sub.Clone()
	if err != nil {
		return nil, err
	}

	session := svc.session.ForModule("bidengine-order")

	log := session.Log().With("order", oid)

	order := &order{
		cfg:                        cfg,
		orderID:                    oid,
		session:                    session,
		cluster:                    svc.cluster,
		bus:                        svc.bus,
		sub:                        sub,
		log:                        log,
		lc:                         lifecycle.New(),
		reservationFulfilledNotify: reservationFulfilledNotify, // Normally nil in production
		pass:                       pass,
	}

	// Shut down when parent begins shutting down
	go order.lc.WatchChannel(svc.lc.ShuttingDown())

	// Run main loop in separate thread.
	go order.run(checkForExistingBid)

	// Notify parent of completion (allows drain).
	go func() {
		<-order.lc.Done()
		svc.drainch <- order
	}()

	return order, nil
}

func (o *order) bidTimeoutEnabled() bool {
	return o.cfg.BidTimeout > time.Duration(0)
}

func (o *order) getBidTimeout() <-chan time.Time {
	if o.bidTimeoutEnabled() {
		return time.After(o.cfg.BidTimeout)
	}

	return nil
}

func (o *order) isStaleBid(bid mvbeta.Bid) bool {
	if !o.bidTimeoutEnabled() {
		return false
	}

	// This bid could be very old, compute the minimum age of the bid
	// do not try anything clever here like asking the RPC node for the current height
	// just use the height from when the session is created
	createdAtBlock := bid.GetCreatedAt()
	blockAge := createdAtBlock - o.session.CreatedAtBlockHeight()
	const minTimePerBlock = 5 * time.Second
	atLeastThisOld := time.Duration(blockAge) * minTimePerBlock
	return atLeastThisOld > o.cfg.BidTimeout
}

type bidAttempt struct {
	id          mtypes.BidID
	reservation ctypes.Reservation
	msg         *mvbeta.MsgCreateBid
	placed      bool
	existing    bool
}

type reservationResult struct {
	bidID       mtypes.BidID
	existing    bool
	reservation ctypes.Reservation
}

type priceResult struct {
	bidID mtypes.BidID
	price sdk.DecCoin
}

func bidIDWithSequence(orderID mtypes.OrderID, provider sdk.AccAddress, bseq uint32) mtypes.BidID {
	id := mtypes.MakeBidID(orderID, provider)
	id.BSeq = bseq

	return id
}

func groupHasGPUModelWildcard(group *dtypes.Group) bool {
	for _, resource := range group.GroupSpec.GetResourceUnits() {
		gpu := resource.GetGPU()
		if gpu == nil || gpu.GetUnits().Value() == 0 {
			continue
		}

		for _, attr := range gpu.GetAttributes() {
			tokens := strings.Split(attr.Key, "/")
			for idx := 0; idx+1 < len(tokens); idx += 2 {
				if tokens[idx] == "model" && tokens[idx+1] == "*" {
					return true
				}
			}
		}
	}

	return false
}

func findBidAttempt(attempts []bidAttempt, id mtypes.BidID) int {
	for idx := range attempts {
		if attempts[idx].id == id {
			return idx
		}
	}

	return -1
}

func placedBidCount(attempts []bidAttempt) int {
	count := 0
	for _, attempt := range attempts {
		if attempt.placed {
			count++
		}
	}

	return count
}

func (o *order) run(checkForExistingBid bool) {
	defer o.lc.ShutdownCompleted()
	ctx, cancel := context.WithCancel(context.Background())
	providerAddr := o.session.Provider().Address()

	var (
		groupch       <-chan runner.Result
		storedGroupCh <-chan runner.Result
		clusterch     <-chan runner.Result
		bidch         <-chan runner.Result
		pricech       <-chan runner.Result
		queryBidsCh   <-chan runner.Result
		shouldBidCh   <-chan runner.Result
		bidTimeout    <-chan time.Time

		group *dtypes.Group

		attempts            []bidAttempt
		pendingExistingBids []mtypes.BidID
		nextBSeq            uint32
		maxBidAttempts      uint32 = 1
		reserveStarted      uint32
		broadcastingBid     *mtypes.BidID
		wonBid              *mtypes.BidID
		stopCreatingBids    bool
		cancelledBids       = make(map[mtypes.BidID]struct{})
	)

	groupch = runner.Do(func() runner.Result {
		res, err := o.session.Client().Query().Deployment().Group(ctx, &dtypes.QueryGroupRequest{ID: o.orderID.GroupID()})
		return runner.NewResult(res.GetGroup(), err)
	})

	if checkForExistingBid {
		queryBidsCh = runner.Do(func() runner.Result {
			return runner.NewResult(o.session.Client().Query().Market().Bids(
				ctx,
				&mvbeta.QueryBidsRequest{
					Filters: mvbeta.BidFilters{
						Owner:    o.orderID.Owner,
						DSeq:     o.orderID.DSeq,
						GSeq:     o.orderID.GSeq,
						OSeq:     o.orderID.OSeq,
						Provider: providerAddr.String(),
						State:    mvbeta.BidOpen.String(),
					},
				},
			))
		})
		storedGroupCh = groupch
		groupch = nil
	}

	startReservation := func(bidID mtypes.BidID, existing bool) {
		o.log.Info("requesting reservation", "bid", bidID, "bseq", bidID.BSeq)
		clusterch = runner.Do(metricsutils.ObserveRunner(func() runner.Result {
			reservation, err := o.cluster.ReserveBid(bidID, group)
			return runner.NewResult(reservationResult{
				bidID:       bidID,
				existing:    existing,
				reservation: reservation,
			}, err)
		}, reservationDuration))
	}

	startNextReservation := func() bool {
		if clusterch != nil || stopCreatingBids {
			return false
		}

		if len(pendingExistingBids) != 0 {
			bidID := pendingExistingBids[0]
			pendingExistingBids = pendingExistingBids[1:]
			startReservation(bidID, true)
			return true
		}

		if reserveStarted >= maxBidAttempts {
			return false
		}

		if nextBSeq == ^uint32(0) {
			o.log.Error("bid sequence exhausted")
			stopCreatingBids = true
			return false
		}

		bidID := bidIDWithSequence(o.orderID, providerAddr, nextBSeq)
		nextBSeq++
		reserveStarted++
		startReservation(bidID, false)
		return true
	}

	finishBidding := func() {
		if placedBidCount(attempts) != 0 {
			bidTimeout = o.getBidTimeout()
		}
	}

	unreserveAttempt := func(idx int) {
		if idx < 0 || idx >= len(attempts) || attempts[idx].reservation == nil {
			return
		}

		bidID := attempts[idx].id
		o.unreserveBid(bidID)
		attempts[idx].reservation = nil
	}

	removeAttempt := func(idx int) {
		if idx < 0 || idx >= len(attempts) {
			return
		}

		attempts = append(attempts[:idx], attempts[idx+1:]...)
	}

	isCancelledBid := func(bidID mtypes.BidID) bool {
		_, exists := cancelledBids[bidID]

		return exists
	}

	handleCancelledReservation := func(result reservationResult) bool {
		if !isCancelledBid(result.bidID) {
			return false
		}
		if result.reservation != nil {
			o.unreserveBid(result.bidID)
		}

		return true
	}

loop:
	for {
		select {
		case <-o.lc.ShutdownRequest():
			break loop

		case queryBids := <-queryBidsCh:
			queryBidsCh = nil
			if err := queryBids.Error(); err != nil {
				o.session.Log().Error("could not get existing bids", "err", err, "errtype", fmt.Sprintf("%T", err))
				break loop
			}

			bids := queryBids.Value().(*mvbeta.QueryBidsResponse).GetBids()
			sort.Slice(bids, func(i, j int) bool {
				return bids[i].GetBid().ID.BSeq < bids[j].GetBid().ID.BSeq
			})

			staleExistingBid := false
			for _, bidResponse := range bids {
				bid := bidResponse.GetBid()
				if bid.GetState() != mvbeta.BidOpen {
					o.session.Log().Error("bid in unexpected state", "bid-state", bid.GetState())
					break loop
				}
				if bid.ID.BSeq == ^uint32(0) {
					o.session.Log().Error("bid sequence exhausted", "bid", bid.ID)
					break loop
				}
				if bid.ID.BSeq >= nextBSeq {
					nextBSeq = bid.ID.BSeq + 1
				}
				attempts = append(attempts, bidAttempt{
					id:       bid.ID,
					placed:   true,
					existing: true,
				})
				pendingExistingBids = append(pendingExistingBids, bid.ID)
				if o.isStaleBid(bid) {
					o.session.Log().Info("found expired bid", "block-height", bid.GetCreatedAt())
					staleExistingBid = true
				}
			}

			if staleExistingBid {
				break loop
			}

			if len(bids) != 0 {
				o.session.Log().Info("found existing bids", "count", len(bids))
			}

			groupch = storedGroupCh
			storedGroupCh = nil

		case ev := <-o.sub.Events():
			switch ev := ev.(type) {
			case *mtypes.EventLeaseCreated:
				// different group
				if !o.orderID.GroupID().Equals(ev.ID.GroupID()) {
					o.log.Debug("ignoring group", "group", ev.ID.GroupID())
					break
				}

				// check winning provider
				if ev.ID.Provider != o.session.Provider().Address().String() {
					orderCompleteCounter.WithLabelValues("lease-lost").Inc()
					o.log.Info("lease lost", "lease", ev.ID)
					break loop
				}
				orderCompleteCounter.WithLabelValues("lease-won").Inc()

				// TODO: sanity check (price, state, etc...)
				o.log.Info("lease won", "lease", ev.ID)

				if err := o.bus.Publish(event.LeaseWon{
					LeaseID: ev.ID,
					Group:   group,
					Price:   ev.Price,
				}); err != nil {
					o.log.Error("failed to publish to event queue", err)
				}
				winner := ev.ID.BidID()
				wonBid = &winner

				break loop
			case *mtypes.EventOrderClosed:
				// different deployment
				if !ev.ID.Equals(o.orderID) {
					break
				}

				o.log.Info("order closed")
				orderCompleteCounter.WithLabelValues("order-closed").Inc()
				break loop
			case *mtypes.EventBidClosed:
				if wonBid != nil {
					// Ignore any event after LeaseCreated
					continue
				}

				// Ignore bid closed not for this group
				if !o.orderID.GroupID().Equals(ev.ID.GroupID()) {
					break
				}

				// Ignore bid closed not for this provider
				if ev.ID.GetProvider() != providerAddr.String() {
					break
				}

				cancelledBids[ev.ID] = struct{}{}
				idx := findBidAttempt(attempts, ev.ID)
				if idx == -1 {
					break
				}

				attempts[idx].placed = false
				unreserveAttempt(idx)
				removeAttempt(idx)
				orderCompleteCounter.WithLabelValues("bid-closed-external").Inc()
				if placedBidCount(attempts) == 0 && clusterch == nil && pricech == nil && bidch == nil {
					break loop
				}
			}
		case result := <-groupch:
			groupch = nil
			o.log.Info("group fetched")

			if result.Error() != nil {
				o.log.Error("fetching group", "err", result.Error())
				break loop
			}

			res := result.Value().(dtypes.Group)
			group = &res

			shouldBidCh = runner.Do(func() runner.Result {
				return runner.NewResult(o.shouldBid(group))
			})

		case result := <-shouldBidCh:
			shouldBidCh = nil

			if result.Error() != nil {
				shouldBidCounter.WithLabelValues(metricsutils.FailLabel).Inc()
				o.log.Error("failure during checking should bid", "err", result.Error())
				break loop
			}

			shouldBid := result.Value().(bool)
			if !shouldBid {
				shouldBidCounter.WithLabelValues("decline").Inc()
				o.log.Debug("declined to bid")
				break loop
			}

			shouldBidCounter.WithLabelValues("accept").Inc()
			if groupHasGPUModelWildcard(group) {
				maxBidAttempts = mvbeta.DefaultParams().OrderMaxBids
			}
			reserveStarted = uint32(len(attempts))
			if !startNextReservation() {
				finishBidding()
			}

		case result := <-clusterch:
			clusterch = nil

			if result.Error() != nil {
				reservationCounter.WithLabelValues(metricsutils.OpenLabel, metricsutils.FailLabel).Inc()
				o.log.Error("reserving resources", "err", result.Error())
				reservationResult := result.Value().(reservationResult)
				if reservationResult.existing {
					break loop
				}

				if placedBidCount(attempts) == 0 {
					break loop
				}

				stopCreatingBids = true
				finishBidding()
				continue
			}

			reservationCounter.WithLabelValues(metricsutils.OpenLabel, metricsutils.SuccessLabel).Inc()

			o.log.Info("Reservation fulfilled")

			// If the channel is assigned and there is capacity, write into the channel
			if o.reservationFulfilledNotify != nil {
				select {
				case o.reservationFulfilledNotify <- 0:
				default:
				}
			}

			reservationResult := result.Value().(reservationResult)
			if handleCancelledReservation(reservationResult) {
				if !startNextReservation() {
					if placedBidCount(attempts) == 0 && pricech == nil && bidch == nil {
						break loop
					}
					finishBidding()
				}
				continue
			}

			attempt := bidAttempt{
				id:          reservationResult.bidID,
				reservation: reservationResult.reservation,
				existing:    reservationResult.existing,
				placed:      reservationResult.existing,
			}

			if idx := findBidAttempt(attempts, attempt.id); idx == -1 {
				attempts = append(attempts, attempt)
			} else {
				attempts[idx].reservation = attempt.reservation
			}

			if reservationResult.existing {
				o.log.Info("Fulfillment already exists")
				if !startNextReservation() {
					finishBidding()
				}
				continue
			}

			pricech = runner.Do(metricsutils.ObserveRunner(func() runner.Result {
				priceReq := Request{
					Owner:              group.ID.Owner,
					GSpec:              &group.GroupSpec,
					PricePrecision:     DefaultPricePrecision,
					AllocatedResources: reservationResult.reservation.GetAllocatedResources(),
				}
				price, err := o.cfg.PricingStrategy.CalculatePrice(ctx, priceReq)
				return runner.NewResult(priceResult{
					bidID: reservationResult.bidID,
					price: price,
				}, err)
			}, pricingDuration))

		case result := <-pricech:
			pricech = nil
			if result.Error() != nil {
				o.log.Error("error calculating price", "err", result.Error())
				priceResult := result.Value().(priceResult)
				idx := findBidAttempt(attempts, priceResult.bidID)
				unreserveAttempt(idx)
				removeAttempt(idx)
				if placedBidCount(attempts) == 0 {
					break loop
				}
				stopCreatingBids = true
				finishBidding()
				continue
			}

			priceResult := result.Value().(priceResult)
			price := priceResult.price
			maxPrice := group.GroupSpec.Price()

			if maxPrice.GetDenom() != price.GetDenom() {
				o.log.Error("Unsupported Denomination", "calculated", price.String(), "max-price", maxPrice.String())
				idx := findBidAttempt(attempts, priceResult.bidID)
				unreserveAttempt(idx)
				removeAttempt(idx)
				if placedBidCount(attempts) == 0 {
					break loop
				}
				stopCreatingBids = true
				finishBidding()
				continue
			}

			if maxPrice.IsLT(price) {
				o.log.Info("Price too high, not bidding", "price", price.String(), "max-price", maxPrice.String())
				idx := findBidAttempt(attempts, priceResult.bidID)
				unreserveAttempt(idx)
				removeAttempt(idx)
				if placedBidCount(attempts) == 0 {
					break loop
				}
				stopCreatingBids = true
				finishBidding()
				continue
			}

			o.log.Debug("submitting fulfillment", "price", price)

			idx := findBidAttempt(attempts, priceResult.bidID)
			if idx == -1 || attempts[idx].reservation == nil {
				o.log.Error("priced bid without reservation", "bid", priceResult.bidID)
				break loop
			}

			offer := mvbeta.ResourceOfferFromRU(attempts[idx].reservation.GetAllocatedResources())

			msg := mvbeta.NewMsgCreateBid(priceResult.bidID, price, deposit.Deposit{
				Amount:  o.cfg.Deposit,
				Sources: deposit.Sources{deposit.SourceBalance},
			}, offer)
			msg.ReclamationWindow = o.cfg.ReclamationWindow
			attempts[idx].msg = msg
			bidID := priceResult.bidID
			broadcastingBid = &bidID
			bidch = runner.Do(func() runner.Result {
				_, err := o.session.Client().Tx().BroadcastMsgs(ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError())
				return runner.NewResult(bidID, err)
			})

		case result := <-bidch:
			bidch = nil
			if result.Error() != nil {
				bidCounter.WithLabelValues(metricsutils.OpenLabel, metricsutils.FailLabel).Inc()
				o.log.Error("bid failed", "err", result.Error())
				if broadcastingBid != nil {
					idx := findBidAttempt(attempts, *broadcastingBid)
					unreserveAttempt(idx)
					removeAttempt(idx)
				}
				broadcastingBid = nil
				if placedBidCount(attempts) == 0 {
					break loop
				}
				stopCreatingBids = true
				finishBidding()
				continue
			}

			bidID := result.Value().(mtypes.BidID)
			broadcastingBid = nil
			if idx := findBidAttempt(attempts, bidID); idx != -1 {
				attempts[idx].placed = true
			}

			o.log.Info("bid complete")
			bidCounter.WithLabelValues(metricsutils.OpenLabel, metricsutils.SuccessLabel).Inc()

			if !startNextReservation() {
				finishBidding()
			}
		case <-bidTimeout:
			o.log.Info("bid timeout, closing bid")
			orderCompleteCounter.WithLabelValues("bid-timeout").Inc()
			break loop
		}
	}

	o.log.Info("shutting down")
	o.lc.ShutdownInitiated(nil)
	o.sub.Close()

	if clusterch != nil {
		result := <-clusterch
		clusterch = nil
		if result.Error() == nil {
			reservationResult := result.Value().(reservationResult)
			if !handleCancelledReservation(reservationResult) {
				attempt := bidAttempt{
					id:          reservationResult.bidID,
					reservation: reservationResult.reservation,
					existing:    reservationResult.existing,
					placed:      reservationResult.existing,
				}
				if idx := findBidAttempt(attempts, attempt.id); idx == -1 {
					attempts = append(attempts, attempt)
				} else {
					attempts[idx].reservation = attempt.reservation
				}
			}
		}
	}

	if bidch != nil {
		result := <-bidch
		bidch = nil
		if result.Error() == nil {
			bidID := result.Value().(mtypes.BidID)
			if idx := findBidAttempt(attempts, bidID); idx != -1 {
				attempts[idx].placed = true
			}
		}
	}

	for idx := range attempts {
		if attempts[idx].reservation == nil {
			continue
		}
		if wonBid != nil && attempts[idx].id == *wonBid {
			continue
		}
		unreserveAttempt(idx)
	}

	if wonBid == nil {
		for _, attempt := range attempts {
			if !attempt.placed {
				continue
			}

			o.log.Debug("closing bid", "bid", attempt.id, "bseq", attempt.id.BSeq)
			msg := &mvbeta.MsgCloseBid{
				ID:     attempt.id,
				Reason: mtypes.LeaseClosedReasonUnspecified,
			}
			_, err := o.session.Client().Tx().BroadcastMsgs(ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError())
			if err != nil {
				o.log.Error("closing bid", "err", err)
				bidCounter.WithLabelValues("close", metricsutils.FailLabel).Inc()
			} else {
				o.log.Info("bid closed", "bid", attempt.id, "bseq", attempt.id.BSeq)
				bidCounter.WithLabelValues("close", metricsutils.SuccessLabel).Inc()
			}
		}
	}
	cancel()

	// Wait for all runners to complete.
	if groupch != nil {
		<-groupch
	}
	if storedGroupCh != nil {
		<-storedGroupCh
	}
	if clusterch != nil {
		<-clusterch
	}
	if bidch != nil {
		<-bidch
	}
	if pricech != nil {
		<-pricech
	}
	if queryBidsCh != nil {
		<-queryBidsCh
	}
	if shouldBidCh != nil {
		<-shouldBidCh
	}
}

func (o *order) unreserveBid(bidID mtypes.BidID) {
	o.log.Debug("unreserving reservation", "bid", bidID, "bseq", bidID.BSeq)
	if err := o.cluster.UnreserveBid(bidID); err != nil {
		o.log.Error("error unreserving reservation", "err", err, "bid", bidID, "bseq", bidID.BSeq)
		reservationCounter.WithLabelValues("close", metricsutils.FailLabel).Inc()
	} else {
		reservationCounter.WithLabelValues("close", metricsutils.SuccessLabel).Inc()
	}
}

func (o *order) shouldBid(group *dtypes.Group) (bool, error) {
	// does provider have required attributes?
	if !group.GroupSpec.MatchAttributes(o.session.Provider().Attributes) {
		o.log.Debug("unable to fulfill: incompatible provider attributes")
		return false, nil
	}

	// does order have required attributes?
	if !o.cfg.Attributes.SubsetOf(group.GroupSpec.Requirements.Attributes) {
		o.log.Debug("unable to fulfill: incompatible order attributes")
		return false, nil
	}

	attr, err := o.pass.GetAttributes()
	if err != nil {
		return false, err
	}

	// does provider have required capabilities?
	if !group.GroupSpec.MatchResourcesRequirements(attr) {
		o.log.Debug("unable to fulfill: incompatible attributes for resources requirements", "wanted", group.GroupSpec, "have", attr)
		return false, nil
	}

	for _, resources := range group.GroupSpec.GetResourceUnits() {
		if len(resources.Storage) > o.cfg.MaxGroupVolumes {
			o.log.Info(fmt.Sprintf("unable to fulfill: group volumes count exceeds (%d > %d)", len(resources.Storage), o.cfg.MaxGroupVolumes))
			return false, nil
		}
	}
	signatureRequirements := group.GroupSpec.Requirements.SignedBy
	if signatureRequirements.Size() != 0 {
		// Check that the signature requirements are met for each attribute
		var provAttr atypes.AuditedProviders
		ownAttrs := atypes.AuditedProvider{
			Owner:      o.session.Provider().Owner,
			Auditor:    "",
			Attributes: o.session.Provider().Attributes,
		}
		provAttr = append(provAttr, ownAttrs)
		auditors := make([]string, 0)
		auditors = append(auditors, group.GroupSpec.Requirements.SignedBy.AllOf...)
		auditors = append(auditors, group.GroupSpec.Requirements.SignedBy.AnyOf...)

		gotten := make(map[string]struct{})
		for _, auditor := range auditors {
			_, done := gotten[auditor]
			if done {
				continue
			}
			result, err := o.pass.GetAuditorAttributeSignatures(auditor)
			if err != nil {
				return false, err
			}
			provAttr = append(provAttr, result...)
			gotten[auditor] = struct{}{}
		}

		ok := group.GroupSpec.MatchRequirements(provAttr)
		if !ok {
			o.log.Debug("attribute signature requirements not met")
			return false, nil
		}
	}

	if err := group.GroupSpec.ValidateBasic(); err != nil {
		o.log.Error("unable to fulfill: group validation error",
			"err", err)
		return false, nil
	}
	return true, nil
}
