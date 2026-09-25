package evm

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
)

type rpcTestRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcTestResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
}

type recordingRPC struct {
	mu       sync.Mutex
	requests int
	methods  []string
	handle   func(rpcTestRequest) json.RawMessage
}

func (s *recordingRPC) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(request.Body).Decode(&raw); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	if len(raw) > 0 && raw[0] == '[' {
		var requests []rpcTestRequest
		if err := json.Unmarshal(raw, &requests); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		responses := make([]rpcTestResponse, 0, len(requests))
		for _, item := range requests {
			s.record(item.Method)
			responses = append(responses, rpcTestResponse{
				JSONRPC: "2.0", ID: item.ID, Result: s.handle(item),
			})
		}
		slices.Reverse(responses)
		_ = json.NewEncoder(writer).Encode(responses)
		return
	}
	var item rpcTestRequest
	if err := json.Unmarshal(raw, &item); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	s.record(item.Method)
	_ = json.NewEncoder(writer).Encode(rpcTestResponse{
		JSONRPC: "2.0", ID: item.ID, Result: s.handle(item),
	})
}

func (s *recordingRPC) record(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods = append(s.methods, method)
}

func newTestGateway(t *testing.T, server *httptest.Server) (*Gateway, abi.ABI) {
	t.Helper()
	parsedABI, err := abi.JSON(stringsNewReader(settlementABIJSON))
	if err != nil {
		t.Fatal(err)
	}
	rpcClient, err := rpc.DialHTTP(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := ethclient.NewClient(rpcClient)
	gateway := &Gateway{
		client: client, chainID: big.NewInt(91342),
		settlement: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		abi:        parsedABI, events: map[common.Hash]string{},
	}
	for _, name := range []string{"PaymentEscrowed", "PaymentCompleted", "PaymentRefunded"} {
		gateway.events[parsedABI.Events[name].ID] = name
	}
	t.Cleanup(gateway.Close)
	return gateway, parsedABI
}

func TestFinalizedEventsProjectsLogsWithoutReceiptOrTransactionRPC(t *testing.T) {
	orderHash := common.HexToHash("0x01")
	payer := common.HexToAddress("0x2222222222222222222222222222222222222222")
	merchantID := common.HexToHash("0x03")
	var logResult json.RawMessage
	rpcServer := &recordingRPC{handle: func(request rpcTestRequest) json.RawMessage {
		if request.Method != "eth_getLogs" {
			t.Fatalf("unexpected follow-up RPC %s", request.Method)
		}
		return logResult
	}}
	server := httptest.NewServer(rpcServer)
	defer server.Close()
	gateway, parsedABI := newTestGateway(t, server)
	event := parsedABI.Events["PaymentEscrowed"]
	data, err := event.Inputs.NonIndexed().Pack(
		big.NewInt(100), big.NewInt(1), uint64(1_700_000_000),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]types.Log{{
		Address: gateway.settlement,
		Topics: []common.Hash{
			event.ID, orderHash, common.BytesToHash(payer.Bytes()), merchantID,
		},
		Data: data, BlockNumber: 12, TxHash: common.HexToHash("0x04"),
		BlockHash: common.HexToHash("0x05"), Index: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	logResult = encoded

	events, err := gateway.FinalizedEvents(context.Background(), 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].OrderHash != orderHash.Hex() ||
		events[0].FromAddress != stringsToLower(payer.Hex()) {
		t.Fatalf("unexpected decoded events: %#v", events)
	}
	if rpcServer.requests != 1 || !slices.Equal(rpcServer.methods, []string{"eth_getLogs"}) {
		t.Fatalf("expected one eth_getLogs HTTP RPC, requests=%d methods=%v",
			rpcServer.requests, rpcServer.methods)
	}
}

func TestObserveTransactionsUsesOneJSONRPCBatch(t *testing.T) {
	firstHash := common.HexToHash("0x11")
	secondHash := common.HexToHash("0x12")
	blockHash := common.HexToHash("0x13")
	var receiptResult json.RawMessage
	rpcServer := &recordingRPC{handle: func(request rpcTestRequest) json.RawMessage {
		if request.Method != "eth_getTransactionReceipt" {
			t.Fatalf("unexpected RPC %s", request.Method)
		}
		if string(request.Params) == "[\""+secondHash.Hex()+"\"]" {
			return json.RawMessage("null")
		}
		return receiptResult
	}}
	server := httptest.NewServer(rpcServer)
	defer server.Close()
	gateway, parsedABI := newTestGateway(t, server)
	event := parsedABI.Events["PaymentCompleted"]
	receipt, err := json.Marshal(&types.Receipt{
		Type: types.LegacyTxType, Status: types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21_000, GasUsed: 21_000,
		EffectiveGasPrice: big.NewInt(2), TxHash: firstHash,
		BlockHash: blockHash, BlockNumber: big.NewInt(42),
		Logs: []*types.Log{{
			Address: gateway.settlement,
			Topics:  []common.Hash{event.ID, common.HexToHash("0x21"), common.HexToHash("0x22")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	receiptResult = receipt

	results, err := gateway.ObserveTransactions(
		context.Background(), []string{firstHash.Hex(), secondHash.Hex()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].Observation.Mined ||
		results[0].Observation.BlockNumber != 42 || results[1].Observation.Mined {
		t.Fatalf("unexpected observations: %#v", results)
	}
	if rpcServer.requests != 1 || len(rpcServer.methods) != 2 {
		t.Fatalf("expected one HTTP batch with two calls, requests=%d methods=%v",
			rpcServer.requests, rpcServer.methods)
	}
}

func TestHeadsUsesOneJSONRPCBatch(t *testing.T) {
	safeHeader, err := json.Marshal(&types.Header{
		Number: big.NewInt(41), Difficulty: big.NewInt(0), Time: 1_700_000_041,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizedHeader, err := json.Marshal(&types.Header{
		Number: big.NewInt(40), Difficulty: big.NewInt(0), Time: 1_700_000_040,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpcServer := &recordingRPC{handle: func(request rpcTestRequest) json.RawMessage {
		if request.Method != "eth_getBlockByNumber" {
			t.Fatalf("unexpected RPC %s", request.Method)
		}
		if bytes.Contains(request.Params, []byte(`"safe"`)) {
			return safeHeader
		}
		return finalizedHeader
	}}
	server := httptest.NewServer(rpcServer)
	defer server.Close()
	gateway, _ := newTestGateway(t, server)

	heads, err := gateway.Heads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if heads.Safe != 41 || heads.Finalized != 40 ||
		heads.SafeTimestamp != 1_700_000_041 || heads.FinalizedTimestamp != 1_700_000_040 {
		t.Fatalf("unexpected heads: %#v", heads)
	}
	if rpcServer.requests != 1 || len(rpcServer.methods) != 2 {
		t.Fatalf("expected one head HTTP batch, requests=%d methods=%v",
			rpcServer.requests, rpcServer.methods)
	}
}

func TestContractPaymentStatesUsesOneFinalizedBatch(t *testing.T) {
	encodedState := func(state uint8) json.RawMessage {
		output, err := newSettlementABI(t).Methods["payments"].Outputs.Pack(
			common.Address{}, big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0),
			common.Address{}, common.Address{}, uint64(0), state,
		)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(hexutil.Bytes(output))
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	rpcServer := &recordingRPC{handle: func(request rpcTestRequest) json.RawMessage {
		if request.Method != "eth_call" ||
			!bytes.Contains(request.Params, []byte(`"finalized"`)) {
			t.Fatalf("expected finalized eth_call, method=%s params=%s",
				request.Method, request.Params)
		}
		return encodedState(0)
	}}
	server := httptest.NewServer(rpcServer)
	defer server.Close()
	gateway, _ := newTestGateway(t, server)
	results, err := gateway.ContractPaymentStates(
		context.Background(), []string{common.HexToHash("0x31").Hex(), common.HexToHash("0x32").Hex()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].State != 0 || results[1].State != 0 {
		t.Fatalf("unexpected contract states: %#v", results)
	}
	if rpcServer.requests != 1 || len(rpcServer.methods) != 2 {
		t.Fatalf("expected one finalized eth_call batch, requests=%d methods=%v",
			rpcServer.requests, rpcServer.methods)
	}
}

func TestObserveCommandUsesOneJSONRPCBatch(t *testing.T) {
	txHash := common.HexToHash("0x41")
	rpcServer := &recordingRPC{handle: func(request rpcTestRequest) json.RawMessage {
		switch request.Method {
		case "eth_getTransactionCount":
			if bytes.Contains(request.Params, []byte(`"pending"`)) {
				return json.RawMessage(`"0x8"`)
			}
			return json.RawMessage(`"0x7"`)
		case "eth_getTransactionByHash":
			return json.RawMessage(`{"blockNumber":"0x2a"}`)
		default:
			t.Fatalf("unexpected RPC %s", request.Method)
			return nil
		}
	}}
	server := httptest.NewServer(rpcServer)
	defer server.Close()
	gateway, _ := newTestGateway(t, server)

	observation, err := gateway.ObserveCommand(context.Background(), settlementCommand(
		txHash.Hex(), "0x2222222222222222222222222222222222222222",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Found || !observation.Mined ||
		observation.LatestNonce != 7 || observation.PendingNonce != 8 {
		t.Fatalf("unexpected command observation: %#v", observation)
	}
	if rpcServer.requests != 1 || len(rpcServer.methods) != 3 {
		t.Fatalf("expected one HTTP batch with three calls, requests=%d methods=%v",
			rpcServer.requests, rpcServer.methods)
	}
}

func settlementCommand(txHash, signer string) settlementapp.SettlementCommand {
	return settlementapp.SettlementCommand{TxHash: txHash, SignerAddress: signer}
}

func newSettlementABI(t *testing.T) abi.ABI {
	t.Helper()
	parsedABI, err := abi.JSON(stringsNewReader(settlementABIJSON))
	if err != nil {
		t.Fatal(err)
	}
	return parsedABI
}

// Small wrappers keep test setup readable without shadowing imported packages.
func stringsNewReader(value string) *strings.Reader { return strings.NewReader(value) }
func stringsToLower(value string) string            { return strings.ToLower(value) }
