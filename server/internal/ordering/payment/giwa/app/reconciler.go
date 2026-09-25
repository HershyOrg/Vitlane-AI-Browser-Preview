package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
)

type TransactionPurpose string

const (
	PurposeClaim    TransactionPurpose = "CLAIM"
	PurposeApprove  TransactionPurpose = "APPROVE"
	PurposePay      TransactionPurpose = "PAY"
	PurposeComplete TransactionPurpose = "COMPLETE"
	PurposeRefund   TransactionPurpose = "REFUND"
	// REFUND_PARTIAL tx도 contract의 PaymentRefunded 이벤트로 종결을 관측한다.
	PurposeRefundPartial TransactionPurpose = "REFUND_PARTIAL"
)

const (
	ReasonRPCObservationFailed          = "RPC_OBSERVATION_FAILED"
	ReasonTransactionObservationExpired = "TX_OBSERVATION_EXHAUSTED"
	ReasonPaymentSubmissionNotObserved  = "PAYMENT_SUBMISSION_NOT_OBSERVED"
	ReasonExpectedEventMismatch         = "EXPECTED_EVENT_MISMATCH"
	ReasonTransactionReverted           = "TX_REVERTED"
)

type ReconcileItem struct {
	Payment            settlementdomain.Payment
	TxHash             string
	Purpose            TransactionPurpose
	TxState            string
	TxBlock            *uint64
	TxBlockHash        string
	TxSubmittedAt      time.Time
	ObservationAttempt int
}

type SubmissionClosureCandidate struct {
	Payment     settlementdomain.Payment
	PayDeadline time.Time
}

type ChainHeads struct {
	Safe               uint64
	Finalized          uint64
	SafeTimestamp      uint64
	FinalizedTimestamp uint64
}

type FinalizedEvent struct {
	TxHash            string
	LogIndex          uint64
	BlockNumber       uint64
	BlockHash         string
	EventName         string
	OrderHash         string
	GasUsed           string
	EffectiveGasPrice string
	FromAddress       string
}

type TransactionObservation struct {
	Mined             bool
	Success           bool
	BlockNumber       uint64
	BlockHash         string
	EventName         string
	OrderHash         string
	GasUsed           string
	EffectiveGasPrice string
	FromAddress       string
}

type TransactionObservationResult struct {
	TxHash      string
	Observation TransactionObservation
	Err         error
}

type CanonicalBlockHashResult struct {
	BlockNumber uint64
	BlockHash   string
	Err         error
}

type ContractPaymentState uint8

const (
	ContractPaymentNone ContractPaymentState = iota
	ContractPaymentEscrowed
	ContractPaymentCompleted
	ContractPaymentRefunded
)

type ContractPaymentStateResult struct {
	OrderHash string
	State     ContractPaymentState
	Err       error
}

type ChainGateway interface {
	Heads(context.Context) (ChainHeads, error)
	ObserveTransactions(context.Context, []string) ([]TransactionObservationResult, error)
	CanonicalBlockHashes(context.Context, []uint64) ([]CanonicalBlockHashResult, error)
	FinalizedEvents(context.Context, uint64, uint64) ([]FinalizedEvent, error)
	ContractPaymentStates(context.Context, []string) ([]ContractPaymentStateResult, error)
}

type ReconcilerRepository interface {
	ListReconcileItems(context.Context, int, time.Time) ([]ReconcileItem, error)
	ScheduleTransactionObservation(context.Context, ReconcileItem, time.Time, string, time.Time) error
	MarkTransactionObservationUnknown(context.Context, ReconcileItem, string, time.Time) error
	ListSubmissionClosureCandidates(context.Context, uint64, int) ([]SubmissionClosureCandidate, error)
	MarkPaymentSubmissionUnknown(context.Context, SubmissionClosureCandidate, string, time.Time) error
	MarkTransactionSafe(context.Context, ReconcileItem, TransactionObservation, time.Time) error
	MarkAuxTransactionFinalized(context.Context, ReconcileItem, TransactionObservation, time.Time) error
	MarkTransactionFailed(context.Context, ReconcileItem, string, time.Time) error
	MarkTransactionReorged(context.Context, ReconcileItem, time.Time) error
	GetFinalizedCursor(context.Context, uint64, string) (uint64, bool, error)
	ProjectFinalizedEvents(context.Context, uint64, string, []FinalizedEvent, uint64, time.Time) error
}

type ReconcilePolicy struct {
	BatchSize             int
	TickTimeout           time.Duration
	RPCTimeout            time.Duration
	EventChunkSize        uint64
	EventOverlapBlocks    uint64
	ObservationTimeout    time.Duration
	ObservationBackoffMin time.Duration
	ObservationBackoffMax time.Duration
}

func DefaultReconcilePolicy() ReconcilePolicy {
	return ReconcilePolicy{
		BatchSize:             50,
		TickTimeout:           30 * time.Second,
		RPCTimeout:            5 * time.Second,
		EventChunkSize:        1_000,
		EventOverlapBlocks:    32,
		ObservationTimeout:    6 * time.Hour,
		ObservationBackoffMin: 15 * time.Second,
		ObservationBackoffMax: 15 * time.Minute,
	}
}

func (p ReconcilePolicy) Validate() error {
	if p.BatchSize <= 0 || p.BatchSize > 500 {
		return fmt.Errorf("batch size must be between 1 and 500")
	}
	if p.TickTimeout <= 0 || p.RPCTimeout <= 0 || p.RPCTimeout >= p.TickTimeout {
		return fmt.Errorf("RPC timeout must be positive and shorter than tick timeout")
	}
	if p.EventChunkSize == 0 || p.EventChunkSize > 100_000 {
		return fmt.Errorf("event chunk size must be between 1 and 100000 blocks")
	}
	if p.EventOverlapBlocks >= p.EventChunkSize {
		return fmt.Errorf("event overlap must be smaller than event chunk size")
	}
	if p.ObservationTimeout <= 0 {
		return fmt.Errorf("observation timeout must be positive")
	}
	if p.ObservationBackoffMin <= 0 ||
		p.ObservationBackoffMax < p.ObservationBackoffMin {
		return fmt.Errorf("observation backoff max must be at least its positive minimum")
	}
	return nil
}

func (p ReconcilePolicy) normalized() ReconcilePolicy {
	defaults := DefaultReconcilePolicy()
	if p.BatchSize <= 0 {
		p.BatchSize = defaults.BatchSize
	}
	if p.TickTimeout <= 0 {
		p.TickTimeout = defaults.TickTimeout
	}
	if p.RPCTimeout <= 0 {
		p.RPCTimeout = defaults.RPCTimeout
	}
	if p.EventChunkSize == 0 {
		p.EventChunkSize = defaults.EventChunkSize
	}
	if p.EventOverlapBlocks >= p.EventChunkSize {
		p.EventOverlapBlocks = defaults.EventOverlapBlocks
		if p.EventOverlapBlocks >= p.EventChunkSize {
			p.EventOverlapBlocks = 0
		}
	}
	if p.ObservationTimeout <= 0 {
		p.ObservationTimeout = defaults.ObservationTimeout
	}
	if p.ObservationBackoffMin <= 0 {
		p.ObservationBackoffMin = defaults.ObservationBackoffMin
	}
	if p.ObservationBackoffMax < p.ObservationBackoffMin {
		p.ObservationBackoffMax = defaults.ObservationBackoffMax
	}
	return p
}

type Reconciler struct {
	repository      ReconcilerRepository
	chain           ChainGateway
	clock           interface{ Now() time.Time }
	chainID         uint64
	contractAddress string
	startBlock      uint64
	logger          *slog.Logger
	policy          ReconcilePolicy
}

func NewReconciler(
	repository ReconcilerRepository,
	chain ChainGateway,
	clock interface{ Now() time.Time },
	chainID uint64,
	contractAddress string,
	startBlock uint64,
	logger *slog.Logger,
	policies ...ReconcilePolicy,
) *Reconciler {
	policy := DefaultReconcilePolicy()
	if len(policies) > 0 {
		policy = policies[0].normalized()
	}
	return &Reconciler{
		repository: repository, chain: chain, clock: clock,
		chainID: chainID, contractAddress: contractAddress, startBlock: startBlock,
		logger: logger, policy: policy,
	}
}

func (w *Reconciler) Run(ctx context.Context, interval time.Duration) {
	for {
		if err := w.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "settlement reconciler tick failed", "error", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *Reconciler) Tick(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, w.policy.TickTimeout)
	defer cancel()

	heads, err := w.readHeads(ctx)
	if err != nil {
		return err
	}
	now := w.clock.Now()
	var tickErrors []error
	if err := w.reconcileTransactions(ctx, heads, now); err != nil {
		tickErrors = append(tickErrors, err)
	}
	cursorCaughtUp, err := w.projectFinalizedChunk(ctx, heads.Finalized)
	if err != nil {
		tickErrors = append(tickErrors, err)
	} else if cursorCaughtUp {
		if err := w.closeExpiredSubmissions(ctx, heads, now); err != nil {
			tickErrors = append(tickErrors, err)
		}
	}
	return errors.Join(tickErrors...)
}

func (w *Reconciler) readHeads(ctx context.Context) (ChainHeads, error) {
	rpcContext, cancel := context.WithTimeout(ctx, w.policy.RPCTimeout)
	defer cancel()
	heads, err := w.chain.Heads(rpcContext)
	if err != nil {
		return ChainHeads{}, fmt.Errorf("read safe/finalized heads: %w", err)
	}
	w.logger.DebugContext(ctx, "settlement RPC batch completed",
		"rpc_method", "eth_getBlockByNumber", "rpc_http_requests", 1,
		"rpc_elements", 2, "purpose", "chain_heads")
	return heads, nil
}

func (w *Reconciler) reconcileTransactions(
	ctx context.Context,
	heads ChainHeads,
	now time.Time,
) error {
	items, err := w.repository.ListReconcileItems(ctx, w.policy.BatchSize, now)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	hashes := make([]string, len(items))
	for index := range items {
		hashes[index] = items[index].TxHash
	}
	results, batchErr := w.observeTransactionBatches(ctx, hashes)
	observations := make(map[string]TransactionObservationResult, len(results))
	blockNumbers := make(map[uint64]struct{})
	for _, result := range results {
		observations[result.TxHash] = result
		if result.Err == nil && result.Observation.Mined {
			blockNumbers[result.Observation.BlockNumber] = struct{}{}
		}
	}
	for _, item := range items {
		if item.TxState == "SAFE" && item.TxBlock != nil {
			blockNumbers[*item.TxBlock] = struct{}{}
		}
	}
	canonical, canonicalErr := w.readCanonicalBlocks(ctx, blockNumbers)
	var itemErrors []error
	if batchErr != nil {
		itemErrors = append(itemErrors, fmt.Errorf(
			"batch observe %d transactions: %w", len(items), batchErr,
		))
	}
	if canonicalErr != nil {
		itemErrors = append(itemErrors, canonicalErr)
	}
	for _, item := range items {
		result, found := observations[item.TxHash]
		if !found {
			result.Err = fmt.Errorf("missing batch result")
		}
		if result.Err != nil {
			if item.TxState != "SAFE" &&
				now.Sub(item.TxSubmittedAt) >= w.policy.ObservationTimeout {
				if err := w.markTransactionObservationUnknown(ctx, item, now); err != nil {
					itemErrors = append(itemErrors, err)
				}
			} else if err := w.scheduleObservation(
				ctx, item, ReasonRPCObservationFailed, now,
			); err != nil {
				itemErrors = append(itemErrors, err)
			}
			itemErrors = append(itemErrors, fmt.Errorf("observe %s: %w", item.TxHash, result.Err))
			continue
		}
		observation := result.Observation
		if !observation.Mined {
			if item.TxState == "SAFE" && item.TxBlock != nil {
				block := canonical[*item.TxBlock]
				if block.Err == nil && block.BlockHash != "" &&
					item.TxBlockHash != "" && block.BlockHash != item.TxBlockHash {
					if err := w.repository.MarkTransactionReorged(ctx, item, now); err != nil {
						itemErrors = append(itemErrors, err)
					}
					continue
				}
				// SAFE already has mined receipt evidence. A later null receipt can
				// be provider lag, and wall time cannot turn that evidence into a
				// transaction failure or an unknown submission.
				if err := w.scheduleObservation(ctx, item, "", now); err != nil {
					itemErrors = append(itemErrors, err)
				}
				continue
			}
			if item.TxState == "SAFE" {
				if err := w.scheduleObservation(ctx, item, "", now); err != nil {
					itemErrors = append(itemErrors, err)
				}
				continue
			}
			if now.Sub(item.TxSubmittedAt) >= w.policy.ObservationTimeout {
				if err := w.markTransactionObservationUnknown(ctx, item, now); err != nil {
					itemErrors = append(itemErrors, err)
				}
				continue
			}
			if err := w.scheduleObservation(ctx, item, "", now); err != nil {
				itemErrors = append(itemErrors, err)
			}
			continue
		}
		block := canonical[observation.BlockNumber]
		if block.Err != nil {
			if err := w.scheduleObservation(ctx, item, ReasonRPCObservationFailed, now); err != nil {
				itemErrors = append(itemErrors, err)
			}
			itemErrors = append(itemErrors, fmt.Errorf(
				"read canonical block %d: %w", observation.BlockNumber, block.Err,
			))
			continue
		}
		if block.BlockHash == "" || block.BlockHash != observation.BlockHash ||
			(item.TxState == "SAFE" && item.TxBlockHash != "" &&
				item.TxBlockHash != observation.BlockHash) {
			if item.TxState == "SAFE" {
				if err := w.repository.MarkTransactionReorged(ctx, item, now); err != nil {
					itemErrors = append(itemErrors, err)
				}
			} else {
				if err := w.scheduleObservation(ctx, item, "", now); err != nil {
					itemErrors = append(itemErrors, err)
				}
			}
			continue
		}
		if observation.FromAddress == "" &&
			(item.Purpose == PurposePay || isAuxiliaryPurpose(item.Purpose)) {
			observation.FromAddress = item.Payment.Payer
		}
		if !observation.Success {
			if err := w.repository.MarkTransactionFailed(
				ctx, item, ReasonTransactionReverted, now,
			); err != nil {
				itemErrors = append(itemErrors, err)
			}
			continue
		}
		if !observationMatches(item, observation) {
			if err := w.repository.MarkTransactionFailed(
				ctx, item, ReasonExpectedEventMismatch, now,
			); err != nil {
				itemErrors = append(itemErrors, err)
			}
			continue
		}
		if item.TxState == "SUBMITTED" && observation.BlockNumber > heads.Safe &&
			now.Sub(item.TxSubmittedAt) >= w.policy.ObservationTimeout {
			if err := w.markTransactionObservationUnknown(ctx, item, now); err != nil {
				itemErrors = append(itemErrors, err)
			}
			continue
		}
		if isAuxiliaryPurpose(item.Purpose) && observation.BlockNumber <= heads.Finalized {
			if err := w.repository.MarkAuxTransactionFinalized(ctx, item, observation, now); err != nil {
				itemErrors = append(itemErrors, err)
			}
		} else if observation.BlockNumber <= heads.Safe && item.TxState == "SUBMITTED" {
			if err := w.repository.MarkTransactionSafe(ctx, item, observation, now); err != nil {
				itemErrors = append(itemErrors, err)
			} else {
				item.TxState = "SAFE"
				if err := w.scheduleObservation(ctx, item, "", now); err != nil {
					itemErrors = append(itemErrors, err)
				}
			}
		} else {
			if err := w.scheduleObservation(ctx, item, "", now); err != nil {
				itemErrors = append(itemErrors, err)
			}
		}
	}
	return errors.Join(itemErrors...)
}

func (w *Reconciler) observeTransactionBatches(
	ctx context.Context,
	hashes []string,
) ([]TransactionObservationResult, error) {
	results := make([]TransactionObservationResult, 0, len(hashes))
	var batchErrors []error
	for start := 0; start < len(hashes); start += w.policy.BatchSize {
		end := start + w.policy.BatchSize
		if end > len(hashes) {
			end = len(hashes)
		}
		rpcContext, cancel := context.WithTimeout(ctx, w.policy.RPCTimeout)
		batchResults, err := w.chain.ObserveTransactions(rpcContext, hashes[start:end])
		cancel()
		if err != nil {
			batchErrors = append(batchErrors, err)
			for _, hash := range hashes[start:end] {
				results = append(results, TransactionObservationResult{TxHash: hash, Err: err})
			}
			continue
		}
		results = append(results, batchResults...)
	}
	if len(hashes) > 0 {
		httpRequests := (len(hashes) + w.policy.BatchSize - 1) / w.policy.BatchSize
		w.logger.DebugContext(ctx, "settlement RPC batch completed",
			"rpc_method", "eth_getTransactionReceipt",
			"rpc_http_requests", httpRequests, "rpc_elements", len(hashes))
	}
	return results, errors.Join(batchErrors...)
}

func (w *Reconciler) readCanonicalBlocks(
	ctx context.Context,
	blocks map[uint64]struct{},
) (map[uint64]CanonicalBlockHashResult, error) {
	resultMap := make(map[uint64]CanonicalBlockHashResult, len(blocks))
	if len(blocks) == 0 {
		return resultMap, nil
	}
	numbers := make([]uint64, 0, len(blocks))
	for block := range blocks {
		numbers = append(numbers, block)
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	rpcContext, cancel := context.WithTimeout(ctx, w.policy.RPCTimeout)
	results, err := w.chain.CanonicalBlockHashes(rpcContext, numbers)
	cancel()
	w.logger.DebugContext(ctx, "settlement RPC batch completed",
		"rpc_method", "eth_getBlockByNumber", "rpc_http_requests", 1,
		"rpc_elements", len(numbers), "purpose", "canonical_hash")
	if err != nil {
		for _, number := range numbers {
			resultMap[number] = CanonicalBlockHashResult{BlockNumber: number, Err: err}
		}
		return resultMap, fmt.Errorf("batch read %d canonical blocks: %w", len(numbers), err)
	}
	for _, result := range results {
		if result.Err == nil && result.BlockHash == "" {
			result.Err = fmt.Errorf("canonical block header unavailable")
		}
		resultMap[result.BlockNumber] = result
	}
	for _, number := range numbers {
		if _, found := resultMap[number]; !found {
			resultMap[number] = CanonicalBlockHashResult{
				BlockNumber: number, Err: fmt.Errorf("missing batch result"),
			}
		}
	}
	return resultMap, nil
}

func (w *Reconciler) scheduleObservation(
	ctx context.Context,
	item ReconcileItem,
	reason string,
	now time.Time,
) error {
	delay := w.policy.ObservationBackoffMin
	for attempt := 0; attempt < item.ObservationAttempt && delay < w.policy.ObservationBackoffMax; attempt++ {
		if delay > w.policy.ObservationBackoffMax/2 {
			delay = w.policy.ObservationBackoffMax
			break
		}
		delay *= 2
	}
	if delay > w.policy.ObservationBackoffMax {
		delay = w.policy.ObservationBackoffMax
	}
	if err := w.repository.ScheduleTransactionObservation(
		ctx, item, now.Add(delay), reason, now,
	); err != nil {
		return fmt.Errorf("schedule observation for %s: %w", item.TxHash, err)
	}
	w.logger.DebugContext(ctx, "settlement observation scheduled",
		"tx_hash", item.TxHash, "purpose", item.Purpose,
		"attempt", item.ObservationAttempt+1, "reason_code", reason,
		"next_observation_at", now.Add(delay))
	return nil
}

func (w *Reconciler) markTransactionObservationUnknown(
	ctx context.Context,
	item ReconcileItem,
	now time.Time,
) error {
	if err := w.repository.MarkTransactionObservationUnknown(
		ctx, item, ReasonTransactionObservationExpired, now,
	); err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "settlement transaction observation unknown",
		"tx_hash", item.TxHash, "purpose", item.Purpose,
		"reason_code", ReasonTransactionObservationExpired,
		"submitted_at", item.TxSubmittedAt, "observed_until", now)
	return nil
}

func (w *Reconciler) projectFinalizedChunk(
	ctx context.Context,
	finalizedHead uint64,
) (bool, error) {
	cursor, found, err := w.repository.GetFinalizedCursor(ctx, w.chainID, w.contractAddress)
	if err != nil {
		return false, err
	}
	if found && cursor >= finalizedHead {
		return true, nil
	}
	progressFrom := w.startBlock
	if found {
		progressFrom = cursor + 1
	}
	from := progressFrom
	if found && w.policy.EventOverlapBlocks > 0 {
		if progressFrom > w.policy.EventOverlapBlocks {
			from = progressFrom - w.policy.EventOverlapBlocks
		} else {
			from = w.startBlock
		}
		if from < w.startBlock {
			from = w.startBlock
		}
	}
	if from > finalizedHead {
		return true, nil
	}
	to := finalizedHead
	if to-from+1 > w.policy.EventChunkSize {
		to = from + w.policy.EventChunkSize - 1
	}
	rpcContext, cancel := context.WithTimeout(ctx, w.policy.RPCTimeout)
	events, err := w.chain.FinalizedEvents(rpcContext, from, to)
	cancel()
	if err != nil {
		return false, fmt.Errorf("scan finalized events %d..%d: %w", from, to, err)
	}
	sort.SliceStable(events, func(left, right int) bool {
		if events[left].BlockNumber != events[right].BlockNumber {
			return events[left].BlockNumber < events[right].BlockNumber
		}
		return events[left].LogIndex < events[right].LogIndex
	})
	if err := w.repository.ProjectFinalizedEvents(
		ctx, w.chainID, w.contractAddress, events, to, w.clock.Now(),
	); err != nil {
		return false, err
	}
	w.logger.InfoContext(ctx, "settlement finalized cursor advanced",
		"chain_id", w.chainID, "from_block", from, "to_block", to, "events", len(events),
		"rpc_method", "eth_getLogs", "rpc_http_requests", 1, "rpc_elements", 1)
	return to >= finalizedHead, nil
}

func (w *Reconciler) closeExpiredSubmissions(
	ctx context.Context,
	heads ChainHeads,
	now time.Time,
) error {
	if heads.FinalizedTimestamp == 0 {
		return nil
	}
	candidates, err := w.repository.ListSubmissionClosureCandidates(
		ctx, heads.FinalizedTimestamp, w.policy.BatchSize,
	)
	if err != nil || len(candidates) == 0 {
		return err
	}
	orderHashes := make([]string, len(candidates))
	for index := range candidates {
		orderHashes[index] = candidates[index].Payment.OrderHash
	}
	rpcContext, cancel := context.WithTimeout(ctx, w.policy.RPCTimeout)
	results, batchErr := w.chain.ContractPaymentStates(rpcContext, orderHashes)
	cancel()
	w.logger.DebugContext(ctx, "settlement RPC batch completed",
		"rpc_method", "eth_call", "rpc_http_requests", 1,
		"rpc_elements", len(orderHashes), "purpose", "finalized_contract_state")
	if batchErr != nil {
		return fmt.Errorf("batch read %d contract payment states: %w", len(orderHashes), batchErr)
	}
	states := make(map[string]ContractPaymentStateResult, len(results))
	for _, result := range results {
		states[result.OrderHash] = result
	}
	var closeErrors []error
	for _, candidate := range candidates {
		result, found := states[candidate.Payment.OrderHash]
		if !found {
			closeErrors = append(closeErrors, fmt.Errorf(
				"contract payment state missing for order %s", candidate.Payment.OrderHash,
			))
			continue
		}
		if result.Err != nil {
			closeErrors = append(closeErrors, fmt.Errorf(
				"read contract payment %s: %w", candidate.Payment.OrderHash, result.Err,
			))
			continue
		}
		if result.State != ContractPaymentNone {
			closeErrors = append(closeErrors, fmt.Errorf(
				"contract payment %s is state %d without projected finalized event",
				candidate.Payment.OrderHash, result.State,
			))
			continue
		}
		if err := w.repository.MarkPaymentSubmissionUnknown(
			ctx, candidate, ReasonPaymentSubmissionNotObserved, now,
		); err != nil {
			closeErrors = append(closeErrors, err)
		} else {
			w.logger.InfoContext(ctx, "settlement payment submission unknown",
				"payment_id", candidate.Payment.ID,
				"agency_order_id", candidate.Payment.AgencyOrderID,
				"reason_code", ReasonPaymentSubmissionNotObserved,
				"pay_deadline", candidate.PayDeadline)
		}
	}
	return errors.Join(closeErrors...)
}

func eventMatches(purpose TransactionPurpose, event string) bool {
	return (purpose == PurposePay && event == "PaymentEscrowed") ||
		(purpose == PurposeComplete && event == "PaymentCompleted") ||
		(purpose == PurposeRefund && event == "PaymentRefunded") ||
		(purpose == PurposeRefundPartial && event == "PaymentRefunded")
}

func isAuxiliaryPurpose(purpose TransactionPurpose) bool {
	return purpose == PurposeClaim || purpose == PurposeApprove
}

func observationMatches(item ReconcileItem, observation TransactionObservation) bool {
	if isAuxiliaryPurpose(item.Purpose) {
		return true
	}
	return observation.OrderHash == item.Payment.OrderHash &&
		eventMatches(item.Purpose, observation.EventName)
}
