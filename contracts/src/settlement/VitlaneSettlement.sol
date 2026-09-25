// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { IVitlaneTestUSD } from "../interfaces/IVitlaneTestUSD.sol";
import { SettlementTypes } from "../libraries/SettlementTypes.sol";

/// @notice Vitlane Settlement v2 (ADR-0050) — PayPal capture/refund와 동형이 되도록
/// 수수료는 pay 시점에 즉시 수취하고, 환불은 수납 종결 여부와 무관하게 누적
/// 부분환불로 수행한다.
///
/// - `pay`: payer에게서 passThrough+fee를 받아 fee를 feeRecipient로 즉시 이체,
///   pass-through만 escrow (PayPal capture가 merchant 잔액에 총액을 넣는 것과
///   수취 시점이 같다).
/// - `complete`: 잔여 pass-through를 principalRecipient로 방출. 수수료 계산 없음.
/// - `refundPartial`: 누적 cap 아래 ESCROWED/COMPLETED 양쪽에서 실행. COMPLETED의
///   pass-through와 모든 fee는 수취 계좌의 사전 allowance-pull이며, 부족 revert는
///   PayPal의 INSUFFICIENT_FUNDS 환불 실패와 같은 클래스다.
/// - `refund`: refundAfter 이후 permissionless escape hatch. 컨트랙트가 쥔 escrow
///   잔여만 반환한다(이미 이체된 fee는 refunder의 refundPartial 경로).
contract VitlaneSettlement {
    string public constant name = "Vitlane Settlement";
    string public constant version = "2";
    uint16 public constant MAX_FEE_BPS = 10_000;
    // 서버 fee 정책은 최소 화폐 단위(cent) ceil이라 base-unit 정확값보다 커질 수
    // 있다. 6-decimals 토큰의 1 cent(10^4 base units)까지 반올림을 허용한다.
    uint256 public constant FEE_ROUNDING_SLACK = 10_000;

    bytes32 public constant DOMAIN_TYPEHASH = keccak256(
        "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
    );
    bytes32 public constant PAYMENT_AUTHORIZATION_TYPEHASH = keccak256(
        "PaymentAuthorization(address payer,address token,uint256 passThroughAmount,uint256 feeAmount,bytes32 orderHash,bytes32 merchantId,uint64 merchantRegistryVersion,uint16 feeBps,address feeRecipient,address principalRecipient,bytes32 assuranceLevel,uint256 nonce,uint64 payDeadline,uint64 refundAfter)"
    );
    uint256 private constant SECP256K1N_HALF =
        0x7fffffffffffffffffffffffffffffff5d576e7357a4501ddfe92f46681b20a0;

    IVitlaneTestUSD public immutable token;
    uint64 public immutable minimumEscrowWindow;
    bytes32 public immutable DOMAIN_SEPARATOR;

    address public admin;
    address public pauser;
    address public authorizer;
    address public finalizer;
    address public refunder;
    uint16 public feeBps;
    address public feeRecipient;
    bool public paused;

    mapping(bytes32 => SettlementTypes.MerchantPayout) public merchantPayouts;
    mapping(bytes32 => SettlementTypes.Payment) public payments;
    mapping(address => mapping(uint256 => bool)) public usedNonces;
    // refundKey는 서버 환불 시도의 replay 방지 identity다(PayPal Refund ID 대응).
    mapping(bytes32 => bool) public usedRefundKeys;

    uint256 private entered;

    error Unauthorized();
    error ZeroAddress();
    error InvalidConfiguration();
    error InvalidAuthorization();
    error AuthorizationExpired();
    error NonceUsed();
    error PaymentAlreadyExists();
    error PaymentNotEscrowed();
    error PaymentNotRefundable();
    error MerchantInactive();
    error RegistryMismatch();
    error FeeMismatch();
    error ExactTransferRequired();
    error Paused();
    error RefundNotAvailable();
    error RefundKeyUsed();
    error RefundExceedsGross();
    error ZeroRefund();
    error Reentrancy();

    event PaymentEscrowed(
        bytes32 indexed orderHash,
        address indexed payer,
        bytes32 indexed merchantId,
        uint256 passThrough,
        uint256 fee,
        uint64 refundAfter
    );
    event PaymentCompleted(
        bytes32 indexed orderHash, bytes32 indexed fulfillmentHash, uint256 netReleased
    );
    event PaymentRefunded(
        bytes32 indexed orderHash,
        address indexed payer,
        bytes32 refundKey,
        uint256 passThroughPart,
        uint256 feePart,
        uint256 passThroughRefunded,
        uint256 feeRefunded
    );
    event MerchantPayoutConfigured(
        bytes32 indexed merchantId, address indexed principalRecipient, uint64 version, bool active
    );
    event FeeConfigured(uint16 feeBps, address indexed feeRecipient);
    event RoleConfigured(bytes32 indexed role, address indexed account);
    event PauseChanged(bool paused);
    event AdminTransferred(address indexed previousAdmin, address indexed nextAdmin);

    constructor(
        address tokenAddress,
        address initialAdmin,
        address initialPauser,
        address initialAuthorizer,
        address initialFinalizer,
        address initialRefunder,
        uint16 initialFeeBps,
        address initialFeeRecipient,
        uint64 escrowWindow
    ) {
        if (
            tokenAddress == address(0) || initialAdmin == address(0) || initialPauser == address(0)
                || initialAuthorizer == address(0) || initialFinalizer == address(0)
                || initialRefunder == address(0) || initialFeeRecipient == address(0)
        ) revert ZeroAddress();
        if (initialFeeBps > MAX_FEE_BPS || escrowWindow == 0) revert InvalidConfiguration();
        token = IVitlaneTestUSD(tokenAddress);
        admin = initialAdmin;
        pauser = initialPauser;
        authorizer = initialAuthorizer;
        finalizer = initialFinalizer;
        refunder = initialRefunder;
        feeBps = initialFeeBps;
        feeRecipient = initialFeeRecipient;
        minimumEscrowWindow = escrowWindow;
        DOMAIN_SEPARATOR = keccak256(
            abi.encode(
                DOMAIN_TYPEHASH,
                keccak256(bytes(name)),
                keccak256(bytes(version)),
                block.chainid,
                address(this)
            )
        );
        emit AdminTransferred(address(0), initialAdmin);
        emit FeeConfigured(initialFeeBps, initialFeeRecipient);
    }

    modifier onlyAdmin() {
        if (msg.sender != admin) revert Unauthorized();
        _;
    }

    modifier nonReentrant() {
        if (entered == 1) revert Reentrancy();
        entered = 1;
        _;
        entered = 0;
    }

    function pay(
        SettlementTypes.PaymentAuthorization calldata authorization,
        bytes calldata signature
    ) external nonReentrant {
        if (paused) revert Paused();
        if (authorization.payer != msg.sender || authorization.token != address(token)) {
            revert InvalidAuthorization();
        }
        if (authorization.passThroughAmount == 0 || block.timestamp > authorization.payDeadline) {
            revert AuthorizationExpired();
        }
        if (
            authorization.refundAfter < authorization.payDeadline
                || authorization.refundAfter - authorization.payDeadline < minimumEscrowWindow
        ) revert InvalidAuthorization();
        if (usedNonces[authorization.payer][authorization.nonce]) revert NonceUsed();
        if (payments[authorization.orderHash].state != SettlementTypes.PaymentState.NONE) {
            revert PaymentAlreadyExists();
        }
        SettlementTypes.MerchantPayout memory payout = merchantPayouts[authorization.merchantId];
        if (!payout.active) revert MerchantInactive();
        if (
            payout.version != authorization.merchantRegistryVersion
                || payout.principalRecipient != authorization.principalRecipient
        ) revert RegistryMismatch();
        // 서버 정확값 fee의 상한 sanity bound: 정책 요율 + 반올림 슬랙 이내.
        if (
            feeBps != authorization.feeBps || feeRecipient != authorization.feeRecipient
                || authorization.feeBps > MAX_FEE_BPS
                || authorization.feeAmount
                    > authorization.passThroughAmount * authorization.feeBps / MAX_FEE_BPS
                        + FEE_ROUNDING_SLACK
        ) revert FeeMismatch();
        if (_recover(hashAuthorization(authorization), signature) != authorizer) {
            revert InvalidAuthorization();
        }

        usedNonces[authorization.payer][authorization.nonce] = true;
        payments[authorization.orderHash] = SettlementTypes.Payment({
            payer: authorization.payer,
            passThroughGross: authorization.passThroughAmount,
            feeGross: authorization.feeAmount,
            passThroughRefunded: 0,
            feeRefunded: 0,
            feeRecipient: authorization.feeRecipient,
            principalRecipient: authorization.principalRecipient,
            refundAfter: authorization.refundAfter,
            state: SettlementTypes.PaymentState.ESCROWED
        });

        uint256 total = authorization.passThroughAmount + authorization.feeAmount;
        uint256 beforeBalance = token.balanceOf(address(this));
        if (!token.transferFrom(authorization.payer, address(this), total)) {
            revert ExactTransferRequired();
        }
        if (token.balanceOf(address(this)) - beforeBalance != total) {
            revert ExactTransferRequired();
        }
        // 수수료 수취 시점 = 결제 시점 (PayPal capture와 동형, ADR-0050).
        if (authorization.feeAmount != 0) {
            if (!token.transfer(authorization.feeRecipient, authorization.feeAmount)) {
                revert ExactTransferRequired();
            }
        }
        emit PaymentEscrowed(
            authorization.orderHash,
            authorization.payer,
            authorization.merchantId,
            authorization.passThroughAmount,
            authorization.feeAmount,
            authorization.refundAfter
        );
    }

    /// @notice 잔여 pass-through를 principalRecipient로 방출한다. 수수료는 pay에서
    /// 이미 수취됐으므로 분배 계산이 없다.
    function complete(bytes32 orderHash, bytes32 fulfillmentHash) external nonReentrant {
        if (paused) revert Paused();
        if (msg.sender != finalizer) revert Unauthorized();
        if (fulfillmentHash == bytes32(0)) revert InvalidConfiguration();
        SettlementTypes.Payment storage payment = payments[orderHash];
        if (payment.state != SettlementTypes.PaymentState.ESCROWED) revert PaymentNotEscrowed();
        uint256 remaining = payment.passThroughGross - payment.passThroughRefunded;
        if (remaining == 0) revert PaymentNotEscrowed();
        payment.state = SettlementTypes.PaymentState.COMPLETED;
        if (!token.transfer(payment.principalRecipient, remaining)) revert ExactTransferRequired();
        emit PaymentCompleted(orderHash, fulfillmentHash, remaining);
    }

    /// @notice 누적 부분환불 (ADR-0050). ESCROWED와 COMPLETED 양쪽에서 실행되며
    /// pause는 환불을 막지 않는다. COMPLETED의 pass-through와 모든 fee는 수취
    /// 계좌의 사전 allowance-pull이다.
    function refundPartial(
        bytes32 orderHash,
        uint256 passThroughPart,
        uint256 feePart,
        bytes32 refundKey
    ) external nonReentrant {
        if (msg.sender != refunder) revert Unauthorized();
        if (refundKey == bytes32(0)) revert InvalidConfiguration();
        if (usedRefundKeys[refundKey]) revert RefundKeyUsed();
        SettlementTypes.Payment storage payment = payments[orderHash];
        if (
            payment.state != SettlementTypes.PaymentState.ESCROWED
                && payment.state != SettlementTypes.PaymentState.COMPLETED
        ) revert PaymentNotRefundable();
        if (passThroughPart == 0 && feePart == 0) revert ZeroRefund();
        if (
            payment.passThroughRefunded + passThroughPart > payment.passThroughGross
                || payment.feeRefunded + feePart > payment.feeGross
        ) revert RefundExceedsGross();

        usedRefundKeys[refundKey] = true;
        bool wasEscrowed = payment.state == SettlementTypes.PaymentState.ESCROWED;
        payment.passThroughRefunded += passThroughPart;
        payment.feeRefunded += feePart;
        if (
            wasEscrowed && payment.passThroughRefunded == payment.passThroughGross
                && payment.feeRefunded == payment.feeGross
        ) {
            payment.state = SettlementTypes.PaymentState.REFUNDED;
        }

        if (passThroughPart != 0) {
            if (wasEscrowed) {
                if (!token.transfer(payment.payer, passThroughPart)) {
                    revert ExactTransferRequired();
                }
            } else {
                if (!token.transferFrom(payment.principalRecipient, payment.payer, passThroughPart))
                {
                    revert ExactTransferRequired();
                }
            }
        }
        if (feePart != 0) {
            if (!token.transferFrom(payment.feeRecipient, payment.payer, feePart)) {
                revert ExactTransferRequired();
            }
        }
        emit PaymentRefunded(
            orderHash,
            payment.payer,
            refundKey,
            passThroughPart,
            feePart,
            payment.passThroughRefunded,
            payment.feeRefunded
        );
    }

    /// @notice escape hatch — refundAfter 이후에는 누구나 escrow 잔여 pass-through를
    /// payer에게 반환할 수 있다. 이미 이체된 fee의 반환은 refunder의 refundPartial
    /// 경로만 가능하다(컨트랙트가 fee를 쥐고 있지 않다).
    function refund(bytes32 orderHash) external nonReentrant {
        SettlementTypes.Payment storage payment = payments[orderHash];
        if (payment.state != SettlementTypes.PaymentState.ESCROWED) revert PaymentNotEscrowed();
        if (block.timestamp < payment.refundAfter && msg.sender != refunder) {
            revert RefundNotAvailable();
        }
        uint256 remaining = payment.passThroughGross - payment.passThroughRefunded;
        if (remaining == 0) revert RefundNotAvailable();
        payment.passThroughRefunded = payment.passThroughGross;
        if (payment.feeRefunded == payment.feeGross) {
            payment.state = SettlementTypes.PaymentState.REFUNDED;
        }
        if (!token.transfer(payment.payer, remaining)) revert ExactTransferRequired();
        emit PaymentRefunded(
            orderHash,
            payment.payer,
            bytes32(0),
            remaining,
            0,
            payment.passThroughRefunded,
            payment.feeRefunded
        );
    }

    function hashAuthorization(SettlementTypes.PaymentAuthorization calldata authorization)
        public
        view
        returns (bytes32)
    {
        // 필드가 모두 static 타입이라 abi.encode 두 조각의 연결은 단일 encode와
        // 동일한 EIP-712 인코딩이다(stack-too-deep 회피).
        bytes memory encodedHead = abi.encode(
            PAYMENT_AUTHORIZATION_TYPEHASH,
            authorization.payer,
            authorization.token,
            authorization.passThroughAmount,
            authorization.feeAmount,
            authorization.orderHash,
            authorization.merchantId,
            authorization.merchantRegistryVersion
        );
        bytes memory encodedTail = abi.encode(
            authorization.feeBps,
            authorization.feeRecipient,
            authorization.principalRecipient,
            authorization.assuranceLevel,
            authorization.nonce,
            authorization.payDeadline,
            authorization.refundAfter
        );
        bytes32 structHash = keccak256(bytes.concat(encodedHead, encodedTail));
        return keccak256(abi.encodePacked("\x19\x01", DOMAIN_SEPARATOR, structHash));
    }

    function configureMerchant(
        bytes32 merchantId,
        address principalRecipient,
        uint64 registryVersion,
        bool active
    ) external onlyAdmin {
        if (merchantId == bytes32(0) || principalRecipient == address(0) || registryVersion == 0) {
            revert InvalidConfiguration();
        }
        merchantPayouts[merchantId] = SettlementTypes.MerchantPayout({
            principalRecipient: principalRecipient, version: registryVersion, active: active
        });
        emit MerchantPayoutConfigured(merchantId, principalRecipient, registryVersion, active);
    }

    function configureFee(uint16 nextFeeBps, address nextFeeRecipient) external onlyAdmin {
        if (nextFeeBps > MAX_FEE_BPS || nextFeeRecipient == address(0)) {
            revert InvalidConfiguration();
        }
        feeBps = nextFeeBps;
        feeRecipient = nextFeeRecipient;
        emit FeeConfigured(nextFeeBps, nextFeeRecipient);
    }

    function setPaused(bool nextPaused) external {
        if (msg.sender != pauser && msg.sender != admin) revert Unauthorized();
        paused = nextPaused;
        emit PauseChanged(nextPaused);
    }

    function setAuthorizer(address account) external onlyAdmin {
        _requireAddress(account);
        authorizer = account;
        emit RoleConfigured(keccak256("AUTHORIZER"), account);
    }

    function setFinalizer(address account) external onlyAdmin {
        _requireAddress(account);
        finalizer = account;
        emit RoleConfigured(keccak256("FINALIZER"), account);
    }

    function setRefunder(address account) external onlyAdmin {
        _requireAddress(account);
        refunder = account;
        emit RoleConfigured(keccak256("REFUNDER"), account);
    }

    function setPauser(address account) external onlyAdmin {
        _requireAddress(account);
        pauser = account;
        emit RoleConfigured(keccak256("PAUSER"), account);
    }

    function transferAdmin(address nextAdmin) external onlyAdmin {
        _requireAddress(nextAdmin);
        emit AdminTransferred(admin, nextAdmin);
        admin = nextAdmin;
    }

    function _requireAddress(address account) private pure {
        if (account == address(0)) revert ZeroAddress();
    }

    function _recover(bytes32 digest, bytes calldata signature) private pure returns (address) {
        if (signature.length != 65) return address(0);
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(signature.offset)
            s := calldataload(add(signature.offset, 32))
            v := byte(0, calldataload(add(signature.offset, 64)))
        }
        if (uint256(s) > SECP256K1N_HALF || (v != 27 && v != 28)) return address(0);
        return ecrecover(digest, v, r, s);
    }
}
