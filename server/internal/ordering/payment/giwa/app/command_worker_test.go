package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type commandRepository struct {
	commands            []SettlementCommand
	ensureCalls         int
	reserved            int
	signed              int
	broadcast           int
	conflicts           int
	failBroadcastRecord int
	listLimit           int
}

func (r *commandRepository) EnsureCommands(
	context.Context,
	string,
	string,
	time.Time,
) error {
	r.ensureCalls++
	return nil
}

func (r *commandRepository) ListCommandWork(
	_ context.Context,
	limit int,
	_ time.Time,
) ([]SettlementCommand, error) {
	r.listLimit = limit
	result := make([]SettlementCommand, 0, len(r.commands))
	for _, command := range r.commands {
		if command.State == CommandPlanned ||
			command.State == CommandNonceReserved ||
			command.State == CommandSigned ||
			command.State == CommandBroadcast {
			result = append(result, command)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (r *commandRepository) ReserveCommandNonce(
	_ context.Context,
	command SettlementCommand,
	pendingNonce uint64,
	_ time.Time,
) (SettlementCommand, error) {
	r.reserved++
	command.State = CommandNonceReserved
	command.SignerNonce = pendingNonce
	r.replace(command)
	return command, nil
}

func (r *commandRepository) RecordCommandSigned(
	_ context.Context,
	command SettlementCommand,
	txHash string,
	rawTransaction []byte,
	_ time.Time,
) error {
	r.signed++
	command.State = CommandSigned
	command.TxHash = txHash
	command.RawTransaction = append([]byte(nil), rawTransaction...)
	r.replace(command)
	return nil
}

func (r *commandRepository) RecordCommandBroadcast(
	_ context.Context,
	command SettlementCommand,
	_ bool,
	_ time.Time,
) error {
	if r.failBroadcastRecord > 0 {
		r.failBroadcastRecord--
		return errors.New("injected DB commit failure")
	}
	r.broadcast++
	command.State = CommandBroadcast
	r.replace(command)
	return nil
}

func (r *commandRepository) RecordCommandConflict(
	_ context.Context,
	command SettlementCommand,
	_ string,
	_ time.Time,
) error {
	r.conflicts++
	command.State = CommandConflict
	r.replace(command)
	return nil
}

func (r *commandRepository) replace(command SettlementCommand) {
	for index := range r.commands {
		sameCommand := command.CommandID != 0 &&
			r.commands[index].CommandID == command.CommandID
		if command.CommandID == 0 && r.commands[index].CommandID == 0 {
			sameCommand = r.commands[index].PaymentID == command.PaymentID &&
				r.commands[index].Purpose == command.Purpose &&
				r.commands[index].MOCompensationID == command.MOCompensationID
		}
		if sameCommand {
			r.commands[index] = command
			return
		}
	}
	r.commands = append(r.commands, command)
}

func TestCommandWorkerKeepsSamePaymentMOCompensationsIndependent(t *testing.T) {
	repository := &commandRepository{commands: []SettlementCommand{
		{
			CommandID: 1, PaymentID: "payment-1", Purpose: CommandRefundPartial,
			MOCompensationID: "compensation-1", SignerAddress: "0xrefunder",
			State: CommandSigned, TxHash: "0xrefund-1", RawTransaction: []byte("raw-1"),
		},
		{
			CommandID: 2, PaymentID: "payment-1", Purpose: CommandRefundPartial,
			MOCompensationID: "compensation-2", SignerAddress: "0xrefunder",
			State: CommandSigned, TxHash: "0xrefund-2", RawTransaction: []byte("raw-2"),
		},
	}}
	gateway := &commandGateway{
		broadcasted: map[string]bool{}, observeErrors: map[string]error{},
	}
	worker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.broadcast != 2 || gateway.broadcast != 2 {
		t.Fatalf("broadcast counts repository=%d gateway=%d",
			repository.broadcast, gateway.broadcast)
	}
	for _, command := range repository.commands {
		if command.State != CommandBroadcast {
			t.Fatalf("command %d (%s) state=%s",
				command.CommandID, command.MOCompensationID, command.State)
		}
	}
}

type commandGateway struct {
	pendingNonce   uint64
	latestNonce    uint64
	signed         int
	broadcast      int
	broadcasted    map[string]bool
	ambiguousError bool
	observeErrors  map[string]error
}

func (g *commandGateway) SignerAddress(purpose CommandPurpose) (string, error) {
	if purpose == CommandComplete {
		return "0xfinalizer", nil
	}
	return "0xrefunder", nil
}

func (g *commandGateway) PendingNonce(context.Context, string) (uint64, error) {
	return g.pendingNonce, nil
}

func (g *commandGateway) SignCommand(
	_ context.Context,
	command SettlementCommand,
) (string, []byte, error) {
	g.signed++
	txHash := "0xcomplete"
	if command.Purpose == CommandRefundPartial {
		txHash = "0xrefund"
	}
	return txHash, []byte("raw-" + txHash), nil
}

func (g *commandGateway) ObserveCommand(
	_ context.Context,
	command SettlementCommand,
) (CommandObservation, error) {
	if err := g.observeErrors[command.PaymentID]; err != nil {
		return CommandObservation{}, err
	}
	return CommandObservation{
		Found:        g.broadcasted[command.TxHash],
		LatestNonce:  g.latestNonce,
		PendingNonce: g.pendingNonce,
	}, nil
}

func TestCommandWorkerBoundsWorkAndIsolatesCommandErrors(t *testing.T) {
	repository := &commandRepository{commands: []SettlementCommand{
		{
			PaymentID: "payment-failing", Purpose: CommandComplete,
			SignerAddress: "0xfinalizer", State: CommandSigned,
			TxHash: "0xfailing", RawTransaction: []byte("raw-failing"),
		},
		{
			PaymentID: "payment-healthy", Purpose: CommandRefundPartial,
			SignerAddress: "0xrefunder", State: CommandSigned,
			TxHash: "0xhealthy", RawTransaction: []byte("raw-healthy"),
		},
	}}
	gateway := &commandGateway{
		broadcasted:   map[string]bool{},
		observeErrors: map[string]error{"payment-failing": errors.New("provider timeout")},
	}
	worker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		ReconcilePolicy{BatchSize: 2, RPCTimeout: time.Second},
	)

	if err := worker.Tick(context.Background()); err == nil {
		t.Fatal("isolated command error must keep the tick degraded")
	}
	if repository.listLimit != 2 || gateway.broadcast != 1 || repository.broadcast != 1 {
		t.Fatalf("command isolation mismatch: limit=%d gateway=%d repository=%d",
			repository.listLimit, gateway.broadcast, repository.broadcast)
	}
}

func (g *commandGateway) BroadcastCommand(
	_ context.Context,
	command SettlementCommand,
) error {
	g.broadcast++
	if g.broadcasted == nil {
		g.broadcasted = map[string]bool{}
	}
	g.broadcasted[command.TxHash] = true
	if g.ambiguousError {
		return errors.New("ambiguous RPC timeout")
	}
	return nil
}

func TestCommandWorkerPersistsNonceSignedTransactionAndBroadcast(t *testing.T) {
	repository := &commandRepository{
		commands: []SettlementCommand{{
			PaymentID: "payment-complete", ChainID: 91342,
			Purpose: CommandComplete, SignerAddress: "0xfinalizer",
			OrderHash: "0xorder", FulfillmentHash: "0xfulfillment",
			State: CommandPlanned,
		}},
	}
	gateway := &commandGateway{pendingNonce: 7, broadcasted: map[string]bool{}}
	worker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	command := repository.commands[0]
	if repository.ensureCalls != 1 || repository.reserved != 1 ||
		repository.signed != 1 || repository.broadcast != 1 ||
		command.State != CommandBroadcast || command.SignerNonce != 7 ||
		command.TxHash != "0xcomplete" || string(command.RawTransaction) != "raw-0xcomplete" ||
		gateway.signed != 1 || gateway.broadcast != 1 {
		t.Fatalf("command pipeline mismatch: command=%#v repo=%#v gateway=%#v",
			command, repository, gateway)
	}
}

func TestCommandWorkerRecoversBroadcastAfterDBCommitFailureWithoutNewSideEffect(t *testing.T) {
	repository := &commandRepository{
		commands: []SettlementCommand{{
			PaymentID: "payment-complete", ChainID: 91342,
			Purpose: CommandComplete, SignerAddress: "0xfinalizer",
			OrderHash: "0xorder", FulfillmentHash: "0xfulfillment",
			State: CommandPlanned,
		}},
		failBroadcastRecord: 1,
	}
	gateway := &commandGateway{pendingNonce: 11, broadcasted: map[string]bool{}}
	firstWorker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := firstWorker.Tick(context.Background()); err == nil {
		t.Fatal("injected DB failure must surface")
	}
	if repository.commands[0].State != CommandSigned ||
		gateway.signed != 1 || gateway.broadcast != 1 {
		t.Fatalf("first crash boundary mismatch: command=%#v gateway=%#v",
			repository.commands[0], gateway)
	}

	// A process restart sees the durable SIGNED row, finds the exact hash on chain,
	// and records it without signing or broadcasting another transaction.
	restartedWorker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := restartedWorker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.commands[0].State != CommandBroadcast ||
		gateway.signed != 1 || gateway.broadcast != 1 ||
		repository.broadcast != 1 {
		t.Fatalf("restart must recover the original tx only: command=%#v repo=%#v gateway=%#v",
			repository.commands[0], repository, gateway)
	}
}

func TestCommandWorkerTreatsAcceptedRPCTimeoutAsBroadcast(t *testing.T) {
	repository := &commandRepository{
		commands: []SettlementCommand{{
			PaymentID: "payment-refund", ChainID: 91342,
			Purpose: CommandRefundPartial, SignerAddress: "0xrefunder",
			OrderHash: "0xorder", State: CommandPlanned,
		}},
	}
	gateway := &commandGateway{
		pendingNonce: 4, broadcasted: map[string]bool{}, ambiguousError: true,
	}
	worker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.commands[0].State != CommandBroadcast ||
		repository.commands[0].TxHash != "0xrefund" {
		t.Fatalf("ambiguous accepted transaction was not recovered: %#v", repository.commands[0])
	}
}

func TestCommandWorkerRebroadcastsSameRawTransactionAfterMempoolDrop(t *testing.T) {
	repository := &commandRepository{
		commands: []SettlementCommand{{
			PaymentID: "payment-complete", ChainID: 91342,
			Purpose: CommandComplete, SignerAddress: "0xfinalizer",
			OrderHash: "0xorder", FulfillmentHash: "0xfulfillment",
			State: CommandPlanned,
		}},
	}
	gateway := &commandGateway{pendingNonce: 9, broadcasted: map[string]bool{}}
	worker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstRaw := string(repository.commands[0].RawTransaction)
	delete(gateway.broadcasted, repository.commands[0].TxHash)

	if err := worker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.commands[0].State != CommandBroadcast ||
		repository.commands[0].SignerNonce != 9 ||
		string(repository.commands[0].RawTransaction) != firstRaw ||
		gateway.signed != 1 || gateway.broadcast != 2 {
		t.Fatalf(
			"mempool drop must rebroadcast the exact transaction: command=%#v gateway=%#v",
			repository.commands[0], gateway,
		)
	}
}

func TestCommandWorkerFailsClosedWhenReservedNonceWasConsumedElsewhere(t *testing.T) {
	repository := &commandRepository{
		commands: []SettlementCommand{{
			PaymentID: "payment-complete", ChainID: 91342,
			Purpose: CommandComplete, SignerAddress: "0xfinalizer",
			OrderHash: "0xorder", FulfillmentHash: "0xfulfillment",
			State: CommandSigned, SignerNonce: 5,
			TxHash: "0xmissing", RawTransaction: []byte("raw"),
		}},
	}
	gateway := &commandGateway{
		pendingNonce: 6, latestNonce: 6, broadcasted: map[string]bool{},
	}
	worker := NewCommandWorker(
		repository, gateway, workerClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := worker.Tick(context.Background()); err == nil {
		t.Fatal("nonce conflict must surface")
	}
	if repository.commands[0].State != CommandConflict ||
		repository.conflicts != 1 || gateway.broadcast != 0 {
		t.Fatalf("nonce conflict must fail closed: command=%#v repo=%#v gateway=%#v",
			repository.commands[0], repository, gateway)
	}
}
