package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type CommandPurpose string

const (
	CommandComplete CommandPurpose = "COMPLETE"
	// REFUND_PARTIAL은 contract 메서드 이름이다. 애플리케이션 의미는 immutable
	// MO allocation 전체를 refundPartial 1회로 보상하는 것이다.
	CommandRefundPartial CommandPurpose = "REFUND_PARTIAL"
)

type CommandState string

const (
	CommandPlanned       CommandState = "PLANNED"
	CommandNonceReserved CommandState = "NONCE_RESERVED"
	CommandSigned        CommandState = "SIGNED"
	CommandBroadcast     CommandState = "BROADCAST"
	CommandFinalized     CommandState = "FINALIZED"
	CommandConflict      CommandState = "CONFLICT"
)

type SettlementCommand struct {
	// CommandID는 outbox 행의 surrogate id다. 한 GIWA payment에 MO별
	// REFUND_PARTIAL이 여러 행 존재할 수 있으므로 상태 전이는 이 id로 건다.
	CommandID       int64
	PaymentID       string
	ChainID         uint64
	Purpose         CommandPurpose
	SignerAddress   string
	OrderHash       string
	FulfillmentHash string
	// REFUND_PARTIAL: immutable MO allocation의 pass-through/fee(base units).
	PassThroughAmount string
	FeeAmount         string
	// REFUND_PARTIAL 전용: contract usedRefundKeys와 1:1인 replay 방지 키와
	// 이 Effect를 소유한 whole-MO compensation id.
	RefundKey        string
	MOCompensationID string
	State            CommandState
	SignerNonce      uint64
	TxHash           string
	RawTransaction   []byte
}

type CommandObservation struct {
	Found        bool
	Mined        bool
	LatestNonce  uint64
	PendingNonce uint64
}

type CommandRepository interface {
	EnsureCommands(
		context.Context,
		string,
		string,
		time.Time,
	) error
	ListCommandWork(context.Context, int, time.Time) ([]SettlementCommand, error)
	ReserveCommandNonce(
		context.Context,
		SettlementCommand,
		uint64,
		time.Time,
	) (SettlementCommand, error)
	RecordCommandSigned(
		context.Context,
		SettlementCommand,
		string,
		[]byte,
		time.Time,
	) error
	RecordCommandBroadcast(
		context.Context,
		SettlementCommand,
		bool,
		time.Time,
	) error
	RecordCommandConflict(
		context.Context,
		SettlementCommand,
		string,
		time.Time,
	) error
}

type CommandGateway interface {
	SignerAddress(CommandPurpose) (string, error)
	PendingNonce(context.Context, string) (uint64, error)
	SignCommand(context.Context, SettlementCommand) (string, []byte, error)
	ObserveCommand(context.Context, SettlementCommand) (CommandObservation, error)
	BroadcastCommand(context.Context, SettlementCommand) error
}

type CommandWorker struct {
	repository CommandRepository
	chain      CommandGateway
	clock      interface{ Now() time.Time }
	logger     *slog.Logger
	policy     ReconcilePolicy
}

func NewCommandWorker(
	repository CommandRepository,
	chain CommandGateway,
	clock interface{ Now() time.Time },
	logger *slog.Logger,
	policies ...ReconcilePolicy,
) *CommandWorker {
	policy := DefaultReconcilePolicy()
	if len(policies) > 0 {
		policy = policies[0].normalized()
	}
	return &CommandWorker{
		repository: repository, chain: chain, clock: clock, logger: logger,
		policy: policy,
	}
}

func (w *CommandWorker) Run(ctx context.Context, interval time.Duration) {
	for {
		if err := w.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "phase5 settlement command worker tick failed", "error", err)
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

func (w *CommandWorker) Tick(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, w.policy.TickTimeout)
	defer cancel()

	finalizer, err := w.chain.SignerAddress(CommandComplete)
	if err != nil {
		return err
	}
	refunder, err := w.chain.SignerAddress(CommandRefundPartial)
	if err != nil {
		return err
	}
	if err := w.repository.EnsureCommands(
		ctx, finalizer, refunder, w.clock.Now(),
	); err != nil {
		return err
	}
	commands, err := w.repository.ListCommandWork(
		ctx, w.policy.BatchSize, w.clock.Now(),
	)
	if err != nil {
		return err
	}
	var commandErrors []error
	for _, command := range commands {
		commandContext, cancelCommand := context.WithTimeout(ctx, w.policy.RPCTimeout)
		err := w.process(commandContext, command)
		cancelCommand()
		if err != nil {
			commandErrors = append(commandErrors, fmt.Errorf(
				"process %s command for payment %s: %w",
				command.Purpose, command.PaymentID, err,
			))
		}
	}
	return errors.Join(commandErrors...)
}

func (w *CommandWorker) process(
	ctx context.Context,
	command SettlementCommand,
) error {
	var err error
	if command.State == CommandPlanned {
		pendingNonce, pendingErr := w.chain.PendingNonce(ctx, command.SignerAddress)
		if pendingErr != nil {
			return fmt.Errorf("read pending signer nonce: %w", pendingErr)
		}
		command, err = w.repository.ReserveCommandNonce(
			ctx, command, pendingNonce, w.clock.Now(),
		)
		if err != nil {
			return err
		}
	}

	if command.State == CommandNonceReserved {
		txHash, rawTransaction, signErr := w.chain.SignCommand(ctx, command)
		if signErr != nil {
			return fmt.Errorf("sign reserved transaction: %w", signErr)
		}
		if strings.TrimSpace(txHash) == "" || len(rawTransaction) == 0 {
			return errors.New("sign command returned an empty transaction")
		}
		if err := w.repository.RecordCommandSigned(
			ctx, command, txHash, rawTransaction, w.clock.Now(),
		); err != nil {
			return err
		}
		command.State = CommandSigned
		command.TxHash = txHash
		command.RawTransaction = append([]byte(nil), rawTransaction...)
	}

	if command.State != CommandSigned && command.State != CommandBroadcast {
		return nil
	}
	observation, err := w.chain.ObserveCommand(ctx, command)
	if err != nil {
		return fmt.Errorf("observe reserved transaction: %w", err)
	}
	if observation.Found {
		if err := w.repository.RecordCommandBroadcast(
			ctx, command, false, w.clock.Now(),
		); err != nil {
			return err
		}
		if command.State == CommandSigned {
			w.logger.InfoContext(
				ctx, "settlement command recovered by hash",
				"payment_id", command.PaymentID,
				"purpose", command.Purpose,
				"tx_hash", command.TxHash,
				"nonce", command.SignerNonce,
			)
		}
		return nil
	}
	if observation.LatestNonce > command.SignerNonce ||
		observation.PendingNonce > command.SignerNonce {
		const reason = "SIGNER_NONCE_CONFLICT"
		if err := w.repository.RecordCommandConflict(
			ctx, command, reason, w.clock.Now(),
		); err != nil {
			return err
		}
		return fmt.Errorf(
			"%s: signer nonce %d is already consumed (latest=%d pending=%d)",
			reason, command.SignerNonce,
			observation.LatestNonce, observation.PendingNonce,
		)
	}

	broadcastErr := w.chain.BroadcastCommand(ctx, command)
	if broadcastErr != nil {
		// RPC can return an ambiguous transport error after accepting a transaction.
		// Recover by its precomputed hash before deciding that the attempt failed.
		recovered, observeErr := w.chain.ObserveCommand(ctx, command)
		if observeErr != nil {
			return fmt.Errorf(
				"broadcast reserved transaction: %v; observe after error: %w",
				broadcastErr, observeErr,
			)
		}
		if !recovered.Found {
			return fmt.Errorf("broadcast reserved transaction: %w", broadcastErr)
		}
	}
	if err := w.repository.RecordCommandBroadcast(
		ctx, command, true, w.clock.Now(),
	); err != nil {
		return err
	}
	w.logger.InfoContext(
		ctx, "settlement command broadcast",
		"payment_id", command.PaymentID,
		"purpose", command.Purpose,
		"tx_hash", command.TxHash,
		"nonce", command.SignerNonce,
	)
	return nil
}
