package dojang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

const dojangScrollABIJSON = `[
  {"type":"function","name":"isVerified","stateMutability":"view","inputs":[{"name":"addr","type":"address"},{"name":"attesterId","type":"bytes32"}],"outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"getVerifiedAddressAttestationUid","stateMutability":"view","inputs":[{"name":"addr","type":"address"},{"name":"attesterId","type":"bytes32"}],"outputs":[{"name":"","type":"bytes32"}]}
]`

const easABIJSON = `[
  {"type":"function","name":"getAttestation","stateMutability":"view","inputs":[{"name":"uid","type":"bytes32"}],"outputs":[{"name":"","type":"tuple","components":[
    {"name":"uid","type":"bytes32"},{"name":"schema","type":"bytes32"},{"name":"time","type":"uint64"},{"name":"expirationTime","type":"uint64"},{"name":"revocationTime","type":"uint64"},{"name":"refUID","type":"bytes32"},{"name":"recipient","type":"address"},{"name":"attester","type":"address"},{"name":"revocable","type":"bool"},{"name":"data","type":"bytes"}
  ]}]}
]`

type Config struct {
	Level           accountdomain.AssuranceLevel
	ScrollAddress   string
	EASAddress      string
	AttesterID      string
	AttesterAddress string
	SchemaUID       string
}

type Provider struct {
	client     *ethclient.Client
	scroll     *bind.BoundContract
	eas        *bind.BoundContract
	config     Config
	attesterID common.Hash
	schemaUID  common.Hash
}

type easAttestation struct {
	UID            [32]byte
	Schema         [32]byte
	Time           uint64
	ExpirationTime uint64
	RevocationTime uint64
	RefUID         [32]byte
	Recipient      common.Address
	Attester       common.Address
	Revocable      bool
	Data           []byte
}

func DialProvider(
	ctx context.Context,
	rpcURL string,
	config Config,
	httpClient *http.Client,
) (*Provider, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, fmt.Errorf("dial Dojang RPC: HTTP client is required")
	}
	rpcClient, err := rpc.DialOptions(
		ctx, strings.TrimSpace(rpcURL), rpc.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, fmt.Errorf("dial Dojang RPC: %w", err)
	}
	client := ethclient.NewClient(rpcClient)
	for name, address := range map[string]string{
		"DojangScroll": config.ScrollAddress,
		"EAS":          config.EASAddress,
	} {
		code, codeErr := client.CodeAt(ctx, common.HexToAddress(address), nil)
		if codeErr != nil || len(code) == 0 {
			client.Close()
			return nil, fmt.Errorf("%s runtime code unavailable", name)
		}
	}
	scrollABI, err := abi.JSON(strings.NewReader(dojangScrollABIJSON))
	if err != nil {
		client.Close()
		return nil, err
	}
	easABI, err := abi.JSON(strings.NewReader(easABIJSON))
	if err != nil {
		client.Close()
		return nil, err
	}
	return &Provider{
		client: client,
		scroll: bind.NewBoundContract(
			common.HexToAddress(config.ScrollAddress), scrollABI, client, client, client,
		),
		eas: bind.NewBoundContract(
			common.HexToAddress(config.EASAddress), easABI, client, client, client,
		),
		config: config, attesterID: common.HexToHash(config.AttesterID),
		schemaUID: common.HexToHash(config.SchemaUID),
	}, nil
}

func (p *Provider) Close() { p.client.Close() }

func (p *Provider) Verify(
	ctx context.Context,
	address string,
	now time.Time,
) (accountapp.IdentityAssuranceEvidence, error) {
	if !common.IsHexAddress(address) {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}
	recipient := common.HexToAddress(address)
	var verifiedOutput []any
	if err := p.scroll.Call(
		&bind.CallOpts{Context: ctx}, &verifiedOutput, "isVerified", recipient, p.attesterID,
	); err != nil {
		return accountapp.IdentityAssuranceEvidence{}, fmt.Errorf("query Dojang verification: %w", err)
	}
	if len(verifiedOutput) != 1 || !*abi.ConvertType(verifiedOutput[0], new(bool)).(*bool) {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}

	var uidOutput []any
	if err := p.scroll.Call(
		&bind.CallOpts{Context: ctx}, &uidOutput,
		"getVerifiedAddressAttestationUid", recipient, p.attesterID,
	); err != nil {
		return accountapp.IdentityAssuranceEvidence{}, fmt.Errorf("query Dojang attestation UID: %w", err)
	}
	if len(uidOutput) != 1 {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}
	uid := *abi.ConvertType(uidOutput[0], new([32]byte)).(*[32]byte)
	if uid == ([32]byte{}) {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}

	var attestationOutput []any
	if err := p.eas.Call(
		&bind.CallOpts{Context: ctx}, &attestationOutput, "getAttestation", uid,
	); err != nil {
		return accountapp.IdentityAssuranceEvidence{}, fmt.Errorf("query EAS attestation: %w", err)
	}
	if len(attestationOutput) != 1 {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}
	attestation := *abi.ConvertType(attestationOutput[0], new(easAttestation)).(*easAttestation)
	return validateAttestation(p.config, p.schemaUID, uid, recipient, attestation, now)
}

func validateConfig(config Config) error {
	if config.Level != accountdomain.AssuranceDojangVerifiedAddress &&
		config.Level != accountdomain.AssuranceDojangTestFaucet {
		return fmt.Errorf("unsupported Dojang assurance level")
	}
	for name, address := range map[string]string{
		"DojangScroll": config.ScrollAddress,
		"EAS":          config.EASAddress, "attester": config.AttesterAddress,
	} {
		if !common.IsHexAddress(address) {
			return fmt.Errorf("%s must be an EVM address", name)
		}
	}
	if !isHex32(config.AttesterID) || !isHex32(config.SchemaUID) {
		return fmt.Errorf("Dojang attester ID and schema UID must be bytes32")
	}
	return nil
}

func validateAttestation(
	config Config,
	schemaUID common.Hash,
	uid [32]byte,
	recipient common.Address,
	attestation easAttestation,
	now time.Time,
) (accountapp.IdentityAssuranceEvidence, error) {
	if attestation.UID != uid || common.Hash(attestation.Schema) != schemaUID ||
		attestation.Recipient != recipient ||
		attestation.Attester != common.HexToAddress(config.AttesterAddress) ||
		attestation.RevocationTime != 0 || attestation.Time == 0 {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}
	verified, err := decodeVerifiedAddressData(attestation.Data)
	if err != nil || !verified {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}
	verifiedAt := time.Unix(int64(attestation.Time), 0).UTC()
	if verifiedAt.After(now) {
		return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceInvalid
	}
	expiresAt := now.Add(15 * time.Minute)
	if attestation.ExpirationTime != 0 {
		expiresAt = time.Unix(int64(attestation.ExpirationTime), 0).UTC()
		if !expiresAt.After(now) {
			return accountapp.IdentityAssuranceEvidence{}, accountdomain.ErrAssuranceExpired
		}
	}
	hash := sha256.New()
	for _, value := range [][]byte{
		uid[:], attestation.Schema[:], recipient.Bytes(), attestation.Attester.Bytes(),
		attestation.Data,
	} {
		_, _ = hash.Write(value)
	}
	return accountapp.IdentityAssuranceEvidence{
		Level: config.Level, IssuerRef: strings.ToLower(config.AttesterAddress),
		SchemaRef:    strings.ToLower(config.SchemaUID),
		EvidenceHash: "0x" + hex.EncodeToString(hash.Sum(nil)),
		VerifiedAt:   verifiedAt, ExpiresAt: expiresAt,
	}, nil
}

func decodeVerifiedAddressData(data []byte) (bool, error) {
	boolType, err := abi.NewType("bool", "", nil)
	if err != nil {
		return false, err
	}
	values, err := (abi.Arguments{{Type: boolType}}).Unpack(data)
	if err != nil || len(values) != 1 {
		return false, errors.New("invalid Verified Address data")
	}
	verified, ok := values[0].(bool)
	if !ok {
		return false, errors.New("Verified Address data is not bool")
	}
	return verified, nil
}

func isHex32(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}
