package evm

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
)

type MerchantExpectation struct {
	MerchantID         string
	PrincipalRecipient string
	RegistryVersion    uint64
}

type Gateway struct {
	client     *ethclient.Client
	chainID    *big.Int
	settlement common.Address
	contract   *bind.BoundContract
	abi        abi.ABI
	finalizer  *PrivateKeySigner
	refunder   *PrivateKeySigner
	events     map[common.Hash]string
}

func DialGateway(
	ctx context.Context,
	rpcURL, settlementAddress string,
	chainID uint64,
	finalizer *PrivateKeySigner,
	refunder *PrivateKeySigner,
	httpClient *http.Client,
) (*Gateway, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("dial EVM RPC: HTTP client is required")
	}
	rpcClient, err := rpc.DialOptions(
		ctx, strings.TrimSpace(rpcURL), rpc.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, fmt.Errorf("dial EVM RPC: %w", err)
	}
	client := ethclient.NewClient(rpcClient)
	parsedABI, err := abi.JSON(strings.NewReader(settlementABIJSON))
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("parse settlement ABI: %w", err)
	}
	address := common.HexToAddress(settlementAddress)
	gateway := &Gateway{
		client: client, chainID: new(big.Int).SetUint64(chainID), settlement: address,
		contract:  bind.NewBoundContract(address, parsedABI, client, client, client),
		abi:       parsedABI,
		finalizer: finalizer, refunder: refunder,
		events: map[common.Hash]string{},
	}
	for _, name := range []string{"PaymentEscrowed", "PaymentCompleted", "PaymentRefunded"} {
		gateway.events[parsedABI.Events[name].ID] = name
	}
	actualChainID, err := client.ChainID(ctx)
	if err != nil || actualChainID.Cmp(gateway.chainID) != 0 {
		client.Close()
		return nil, fmt.Errorf("EVM chain mismatch: got %v, expected %d", actualChainID, chainID)
	}
	code, err := client.CodeAt(ctx, address, nil)
	if err != nil || len(code) == 0 {
		client.Close()
		return nil, fmt.Errorf("settlement runtime code unavailable")
	}
	return gateway, nil
}

func (g *Gateway) Close() {
	g.client.Close()
}

func (g *Gateway) LatestBlock(ctx context.Context) (uint64, error) {
	return g.client.BlockNumber(ctx)
}

func (g *Gateway) Heads(ctx context.Context) (settlementapp.ChainHeads, error) {
	var safe, finalized *types.Header
	batch := []rpc.BatchElem{
		{Method: "eth_getBlockByNumber", Args: []any{"safe", false}, Result: &safe},
		{Method: "eth_getBlockByNumber", Args: []any{"finalized", false}, Result: &finalized},
	}
	if err := g.client.Client().BatchCallContext(ctx, batch); err != nil {
		return settlementapp.ChainHeads{}, err
	}
	if batch[0].Error != nil {
		return settlementapp.ChainHeads{}, fmt.Errorf("read safe head: %w", batch[0].Error)
	}
	if batch[1].Error != nil {
		return settlementapp.ChainHeads{}, fmt.Errorf("read finalized head: %w", batch[1].Error)
	}
	if safe == nil || finalized == nil || safe.Number == nil || finalized.Number == nil {
		return settlementapp.ChainHeads{}, fmt.Errorf("safe/finalized head unavailable")
	}
	return settlementapp.ChainHeads{
		Safe: safe.Number.Uint64(), Finalized: finalized.Number.Uint64(),
		SafeTimestamp: safe.Time, FinalizedTimestamp: finalized.Time,
	}, nil
}

func (g *Gateway) CanonicalBlockHashes(
	ctx context.Context,
	blockNumbers []uint64,
) ([]settlementapp.CanonicalBlockHashResult, error) {
	headers := make([]*types.Header, len(blockNumbers))
	batch := make([]rpc.BatchElem, len(blockNumbers))
	for index, blockNumber := range blockNumbers {
		batch[index] = rpc.BatchElem{
			Method: "eth_getBlockByNumber",
			Args:   []any{hexutil.EncodeUint64(blockNumber), false},
			Result: &headers[index],
		}
	}
	if len(batch) > 0 {
		if err := g.client.Client().BatchCallContext(ctx, batch); err != nil {
			return nil, err
		}
	}
	results := make([]settlementapp.CanonicalBlockHashResult, len(blockNumbers))
	for index, blockNumber := range blockNumbers {
		results[index].BlockNumber = blockNumber
		if batch[index].Error != nil {
			results[index].Err = batch[index].Error
			continue
		}
		if headers[index] == nil {
			results[index].Err = fmt.Errorf("canonical block %d unavailable", blockNumber)
			continue
		}
		results[index].BlockHash = headers[index].Hash().Hex()
	}
	return results, nil
}

func (g *Gateway) FinalizedEvents(
	ctx context.Context,
	fromBlock, toBlock uint64,
) ([]settlementapp.FinalizedEvent, error) {
	topics := make([]common.Hash, 0, len(g.events))
	for topic := range g.events {
		topics = append(topics, topic)
	}
	logs, err := g.client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: []common.Address{g.settlement},
		Topics:    [][]common.Hash{topics},
	})
	if err != nil {
		return nil, err
	}
	events := make([]settlementapp.FinalizedEvent, 0, len(logs))
	for _, eventLog := range logs {
		if eventLog.Removed || eventLog.Address != g.settlement || len(eventLog.Topics) == 0 {
			continue
		}
		name, found := g.events[eventLog.Topics[0]]
		if !found {
			continue
		}
		if eventLog.BlockNumber < fromBlock || eventLog.BlockNumber > toBlock ||
			eventLog.BlockHash == (common.Hash{}) || eventLog.TxHash == (common.Hash{}) {
			return nil, fmt.Errorf("decode %s log: incomplete canonical location", name)
		}
		definition := g.abi.Events[name]
		expectedTopics := 1
		for _, input := range definition.Inputs {
			if input.Indexed {
				expectedTopics++
			}
		}
		if len(eventLog.Topics) != expectedTopics {
			return nil, fmt.Errorf("decode %s log %s: expected %d topics, got %d",
				name, eventLog.TxHash.Hex(), expectedTopics, len(eventLog.Topics))
		}
		if _, err := definition.Inputs.NonIndexed().Unpack(eventLog.Data); err != nil {
			return nil, fmt.Errorf("decode %s log %s data: %w", name, eventLog.TxHash.Hex(), err)
		}
		fromAddress := ""
		if (name == "PaymentEscrowed" || name == "PaymentRefunded") && len(eventLog.Topics) >= 3 {
			fromAddress = strings.ToLower(common.BytesToAddress(eventLog.Topics[2].Bytes()).Hex())
		}
		events = append(events, settlementapp.FinalizedEvent{
			TxHash: eventLog.TxHash.Hex(), LogIndex: uint64(eventLog.Index),
			BlockNumber: eventLog.BlockNumber, BlockHash: eventLog.BlockHash.Hex(),
			EventName: name, OrderHash: eventLog.Topics[1].Hex(),
			FromAddress: fromAddress,
		})
	}
	return events, nil
}

func (g *Gateway) ValidateConfig(
	ctx context.Context,
	tokenAddress, faucetAddress, feeRecipient, authorizer, finalizer, refunder, pauser string,
	claimAmount string,
	feeBps uint16,
	merchants []MerchantExpectation,
) error {
	expected := map[string]common.Address{
		"token":        common.HexToAddress(tokenAddress),
		"feeRecipient": common.HexToAddress(feeRecipient),
		"authorizer":   common.HexToAddress(authorizer),
		"finalizer":    common.HexToAddress(finalizer),
		"refunder":     common.HexToAddress(refunder),
		"pauser":       common.HexToAddress(pauser),
	}
	for getter, expectedAddress := range expected {
		var output []any
		if err := g.contract.Call(&bind.CallOpts{Context: ctx}, &output, getter); err != nil {
			return fmt.Errorf("read settlement %s: %w", getter, err)
		}
		if len(output) != 1 {
			return fmt.Errorf("settlement %s returned no value", getter)
		}
		actual := *abi.ConvertType(output[0], new(common.Address)).(*common.Address)
		if actual != expectedAddress {
			return fmt.Errorf("settlement %s mismatch: got %s expected %s", getter, actual, expectedAddress)
		}
	}
	var feeOutput []any
	if err := g.contract.Call(&bind.CallOpts{Context: ctx}, &feeOutput, "feeBps"); err != nil {
		return fmt.Errorf("read settlement feeBps: %w", err)
	}
	actualFee := *abi.ConvertType(feeOutput[0], new(uint16)).(*uint16)
	if actualFee != feeBps {
		return fmt.Errorf("settlement fee mismatch: got %d expected %d", actualFee, feeBps)
	}
	parsedTokenABI, err := abi.JSON(strings.NewReader(tokenABIJSON))
	if err != nil {
		return err
	}
	tokenContract := bind.NewBoundContract(
		common.HexToAddress(tokenAddress), parsedTokenABI, g.client, g.client, g.client,
	)
	var decimalsOutput []any
	if err := tokenContract.Call(&bind.CallOpts{Context: ctx}, &decimalsOutput, "decimals"); err != nil {
		return fmt.Errorf("read token decimals: %w", err)
	}
	decimals := *abi.ConvertType(decimalsOutput[0], new(uint8)).(*uint8)
	if decimals != 6 {
		return fmt.Errorf("token decimals mismatch: got %d expected 6", decimals)
	}
	var minterOutput []any
	if err := tokenContract.Call(&bind.CallOpts{Context: ctx}, &minterOutput, "minter"); err != nil {
		return fmt.Errorf("read token minter: %w", err)
	}
	minter := *abi.ConvertType(minterOutput[0], new(common.Address)).(*common.Address)
	if minter != common.HexToAddress(faucetAddress) {
		return fmt.Errorf("token minter mismatch: got %s expected faucet %s", minter, faucetAddress)
	}
	parsedFaucetABI, err := abi.JSON(strings.NewReader(faucetABIJSON))
	if err != nil {
		return err
	}
	faucetContract := bind.NewBoundContract(
		common.HexToAddress(faucetAddress), parsedFaucetABI, g.client, g.client, g.client,
	)
	var faucetTokenOutput []any
	if err := faucetContract.Call(&bind.CallOpts{Context: ctx}, &faucetTokenOutput, "token"); err != nil {
		return fmt.Errorf("read faucet token: %w", err)
	}
	faucetToken := *abi.ConvertType(faucetTokenOutput[0], new(common.Address)).(*common.Address)
	if faucetToken != common.HexToAddress(tokenAddress) {
		return fmt.Errorf("faucet token mismatch: got %s expected %s", faucetToken, tokenAddress)
	}
	var claimAmountOutput []any
	if err := faucetContract.Call(&bind.CallOpts{Context: ctx}, &claimAmountOutput, "claimAmount"); err != nil {
		return fmt.Errorf("read faucet claim amount: %w", err)
	}
	actualClaimAmount := *abi.ConvertType(claimAmountOutput[0], new(*big.Int)).(**big.Int)
	expectedClaimAmount, ok := new(big.Int).SetString(claimAmount, 10)
	if !ok || expectedClaimAmount.Sign() <= 0 || actualClaimAmount.Cmp(expectedClaimAmount) != 0 {
		return fmt.Errorf("faucet claim amount mismatch: got %s expected %s", actualClaimAmount, claimAmount)
	}
	for _, merchant := range merchants {
		var output []any
		merchantHash := crypto.Keccak256Hash([]byte(merchant.MerchantID))
		if err := g.contract.Call(
			&bind.CallOpts{Context: ctx}, &output, "merchantPayouts", merchantHash,
		); err != nil {
			return fmt.Errorf("read merchant %s: %w", merchant.MerchantID, err)
		}
		if len(output) != 3 {
			return fmt.Errorf("merchant %s returned incomplete payout", merchant.MerchantID)
		}
		principal := *abi.ConvertType(output[0], new(common.Address)).(*common.Address)
		version := *abi.ConvertType(output[1], new(uint64)).(*uint64)
		active := *abi.ConvertType(output[2], new(bool)).(*bool)
		if principal != common.HexToAddress(merchant.PrincipalRecipient) ||
			version != merchant.RegistryVersion || !active {
			return fmt.Errorf("merchant %s registry mismatch", merchant.MerchantID)
		}
	}
	return nil
}

func (g *Gateway) RuntimeCodeHash(ctx context.Context, address string) (string, error) {
	code, err := g.client.CodeAt(ctx, common.HexToAddress(address), nil)
	if err != nil {
		return "", err
	}
	if len(code) == 0 {
		return "", fmt.Errorf("runtime code unavailable for %s", address)
	}
	return crypto.Keccak256Hash(code).Hex(), nil
}

func (g *Gateway) ObserveTransactions(
	ctx context.Context,
	txHashes []string,
) ([]settlementapp.TransactionObservationResult, error) {
	receipts := make([]*types.Receipt, len(txHashes))
	batch := make([]rpc.BatchElem, len(txHashes))
	for index, txHash := range txHashes {
		batch[index] = rpc.BatchElem{
			Method: "eth_getTransactionReceipt",
			Args:   []any{common.HexToHash(txHash)},
			Result: &receipts[index],
		}
	}
	if len(batch) > 0 {
		if err := g.client.Client().BatchCallContext(ctx, batch); err != nil {
			return nil, err
		}
	}
	results := make([]settlementapp.TransactionObservationResult, len(txHashes))
	for index, txHash := range txHashes {
		results[index].TxHash = txHash
		if batch[index].Error != nil {
			results[index].Err = batch[index].Error
			continue
		}
		receipt := receipts[index]
		if receipt == nil {
			continue
		}
		if receipt.BlockNumber == nil {
			results[index].Err = fmt.Errorf("mined receipt has no block number")
			continue
		}
		observation := settlementapp.TransactionObservation{
			Mined: true, Success: receipt.Status == types.ReceiptStatusSuccessful,
			BlockNumber: receipt.BlockNumber.Uint64(), BlockHash: receipt.BlockHash.Hex(),
			GasUsed: fmt.Sprintf("%d", receipt.GasUsed),
		}
		if receipt.EffectiveGasPrice != nil {
			observation.EffectiveGasPrice = receipt.EffectiveGasPrice.String()
		}
		for _, eventLog := range receipt.Logs {
			if eventLog.Address != g.settlement || len(eventLog.Topics) < 2 {
				continue
			}
			if name, found := g.events[eventLog.Topics[0]]; found {
				observation.EventName = name
				observation.OrderHash = eventLog.Topics[1].Hex()
				break
			}
		}
		results[index].Observation = observation
	}
	return results, nil
}

func (g *Gateway) ContractPaymentStates(
	ctx context.Context,
	orderHashes []string,
) ([]settlementapp.ContractPaymentStateResult, error) {
	responses := make([]hexutil.Bytes, len(orderHashes))
	batch := make([]rpc.BatchElem, len(orderHashes))
	for index, orderHash := range orderHashes {
		data, err := g.abi.Pack("payments", common.HexToHash(orderHash))
		if err != nil {
			return nil, err
		}
		batch[index] = rpc.BatchElem{
			Method: "eth_call",
			Args: []any{map[string]string{
				"to": g.settlement.Hex(), "data": hexutil.Encode(data),
			}, "finalized"},
			Result: &responses[index],
		}
	}
	if len(batch) > 0 {
		if err := g.client.Client().BatchCallContext(ctx, batch); err != nil {
			return nil, err
		}
	}
	results := make([]settlementapp.ContractPaymentStateResult, len(orderHashes))
	for index, orderHash := range orderHashes {
		results[index].OrderHash = orderHash
		if batch[index].Error != nil {
			results[index].Err = batch[index].Error
			continue
		}
		values, err := g.abi.Unpack("payments", responses[index])
		if err != nil || len(values) != 9 {
			if err == nil {
				err = fmt.Errorf("payments returned %d values", len(values))
			}
			results[index].Err = err
			continue
		}
		state, ok := values[8].(uint8)
		if !ok || state > uint8(settlementapp.ContractPaymentRefunded) {
			results[index].Err = fmt.Errorf("payments returned invalid state %v", values[6])
			continue
		}
		results[index].State = settlementapp.ContractPaymentState(state)
	}
	return results, nil
}

func (g *Gateway) SignerAddress(
	purpose settlementapp.CommandPurpose,
) (string, error) {
	signer, err := g.commandSigner(purpose)
	if err != nil {
		return "", err
	}
	return strings.ToLower(signer.Address()), nil
}

func (g *Gateway) PendingNonce(
	ctx context.Context,
	signerAddress string,
) (uint64, error) {
	return g.client.PendingNonceAt(ctx, common.HexToAddress(signerAddress))
}

func (g *Gateway) SignCommand(
	ctx context.Context,
	command settlementapp.SettlementCommand,
) (string, []byte, error) {
	signer, err := g.commandSigner(command.Purpose)
	if err != nil {
		return "", nil, err
	}
	if !strings.EqualFold(signer.Address(), command.SignerAddress) {
		return "", nil, fmt.Errorf(
			"%s signer mismatch: configured %s reserved %s",
			command.Purpose, signer.Address(), command.SignerAddress,
		)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(signer.PrivateKey(), g.chainID)
	if err != nil {
		return "", nil, err
	}
	auth.Context = ctx
	auth.NoSend = true
	auth.Nonce = new(big.Int).SetUint64(command.SignerNonce)
	var transaction *types.Transaction
	switch command.Purpose {
	case settlementapp.CommandComplete:
		transaction, err = g.contract.Transact(
			auth, "complete",
			common.HexToHash(command.OrderHash),
			common.HexToHash(command.FulfillmentHash),
		)
	case settlementapp.CommandRefundPartial:
		passPart, passOK := new(big.Int).SetString(command.PassThroughAmount, 10)
		feePart, feeOK := new(big.Int).SetString(command.FeeAmount, 10)
		if !passOK || passPart.Sign() < 0 || !feeOK || feePart.Sign() < 0 ||
			(passPart.Sign() == 0 && feePart.Sign() == 0) {
			return "", nil, fmt.Errorf(
				"partial refund command for payment %s has invalid parts",
				command.PaymentID,
			)
		}
		if !strings.HasPrefix(command.RefundKey, "0x") || len(command.RefundKey) != 66 {
			return "", nil, fmt.Errorf(
				"partial refund command for payment %s is missing its refund key",
				command.PaymentID,
			)
		}
		transaction, err = g.contract.Transact(
			auth, "refundPartial", common.HexToHash(command.OrderHash),
			passPart, feePart, common.HexToHash(command.RefundKey),
		)
	default:
		return "", nil, fmt.Errorf("unsupported command purpose %q", command.Purpose)
	}
	if err != nil {
		return "", nil, err
	}
	rawTransaction, err := transaction.MarshalBinary()
	if err != nil {
		return "", nil, fmt.Errorf("marshal signed transaction: %w", err)
	}
	return strings.ToLower(transaction.Hash().Hex()), rawTransaction, nil
}

func (g *Gateway) ObserveCommand(
	ctx context.Context,
	command settlementapp.SettlementCommand,
) (settlementapp.CommandObservation, error) {
	signer := common.HexToAddress(command.SignerAddress)
	var latestNonce, pendingNonce hexutil.Uint64
	var transaction json.RawMessage
	batch := []rpc.BatchElem{
		{Method: "eth_getTransactionCount", Args: []any{signer, "latest"}, Result: &latestNonce},
		{Method: "eth_getTransactionCount", Args: []any{signer, "pending"}, Result: &pendingNonce},
	}
	if strings.TrimSpace(command.TxHash) != "" {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getTransactionByHash",
			Args:   []any{common.HexToHash(command.TxHash)}, Result: &transaction,
		})
	}
	if err := g.client.Client().BatchCallContext(ctx, batch); err != nil {
		return settlementapp.CommandObservation{}, err
	}
	if batch[0].Error != nil {
		return settlementapp.CommandObservation{}, fmt.Errorf("read latest signer nonce: %w", batch[0].Error)
	}
	if batch[1].Error != nil {
		return settlementapp.CommandObservation{}, fmt.Errorf("read pending signer nonce: %w", batch[1].Error)
	}
	observation := settlementapp.CommandObservation{
		LatestNonce: uint64(latestNonce), PendingNonce: uint64(pendingNonce),
	}
	if len(batch) == 2 {
		return observation, nil
	}
	if batch[2].Error != nil {
		return settlementapp.CommandObservation{}, batch[2].Error
	}
	if len(transaction) == 0 || string(transaction) == "null" {
		return observation, nil
	}
	var envelope struct {
		BlockNumber *hexutil.Big `json:"blockNumber"`
	}
	if err := json.Unmarshal(transaction, &envelope); err != nil {
		return settlementapp.CommandObservation{}, fmt.Errorf("decode observed transaction: %w", err)
	}
	observation.Found = true
	observation.Mined = envelope.BlockNumber != nil
	return observation, nil
}

func (g *Gateway) BroadcastCommand(
	ctx context.Context,
	command settlementapp.SettlementCommand,
) error {
	if len(command.RawTransaction) == 0 {
		return fmt.Errorf("raw signed transaction is unavailable")
	}
	var transaction types.Transaction
	if err := transaction.UnmarshalBinary(command.RawTransaction); err != nil {
		return fmt.Errorf("decode signed transaction: %w", err)
	}
	if !strings.EqualFold(transaction.Hash().Hex(), command.TxHash) {
		return fmt.Errorf("signed transaction hash mismatch")
	}
	if transaction.Nonce() != command.SignerNonce {
		return fmt.Errorf(
			"signed transaction nonce mismatch: got %d expected %d",
			transaction.Nonce(), command.SignerNonce,
		)
	}
	sender, err := types.Sender(
		types.LatestSignerForChainID(g.chainID), &transaction,
	)
	if err != nil {
		return fmt.Errorf("recover signed transaction sender: %w", err)
	}
	if !strings.EqualFold(sender.Hex(), command.SignerAddress) {
		return fmt.Errorf(
			"signed transaction sender mismatch: got %s expected %s",
			sender.Hex(), command.SignerAddress,
		)
	}
	return g.client.SendTransaction(ctx, &transaction)
}

func (g *Gateway) commandSigner(
	purpose settlementapp.CommandPurpose,
) (*PrivateKeySigner, error) {
	switch purpose {
	case settlementapp.CommandComplete:
		if g.finalizer == nil {
			return nil, fmt.Errorf("finalizer signer unavailable")
		}
		return g.finalizer, nil
	case settlementapp.CommandRefundPartial:
		if g.refunder == nil {
			return nil, fmt.Errorf("refunder signer unavailable")
		}
		return g.refunder, nil
	default:
		return nil, fmt.Errorf("unsupported command purpose %q", purpose)
	}
}
