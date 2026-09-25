// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

library SettlementTypes {
    enum PaymentState {
        NONE,
        ESCROWED,
        COMPLETED,
        REFUNDED
    }

    /// @dev v2 (ADR-0050): 서버가 확정한 정확값 {passThroughAmount, feeAmount}를
    /// 서명에 고정한다. feeBps는 재계산이 아니라 feeAmount의 상한 sanity bound다.
    struct PaymentAuthorization {
        address payer;
        address token;
        uint256 passThroughAmount;
        uint256 feeAmount;
        bytes32 orderHash;
        bytes32 merchantId;
        uint64 merchantRegistryVersion;
        uint16 feeBps;
        address feeRecipient;
        address principalRecipient;
        bytes32 assuranceLevel;
        uint256 nonce;
        uint64 payDeadline;
        uint64 refundAfter;
    }

    /// @dev 수수료는 pay 시점에 feeRecipient로 이체되고(escrow는 pass-through만),
    /// 환불은 누적 카운터로 추적한다. PARTIALLY_REFUNDED는 onchain 상태가 아니라
    /// (refunded > 0 && < gross) 파생값이다.
    struct Payment {
        address payer;
        uint256 passThroughGross;
        uint256 feeGross;
        uint256 passThroughRefunded;
        uint256 feeRefunded;
        address feeRecipient;
        address principalRecipient;
        uint64 refundAfter;
        PaymentState state;
    }

    struct MerchantPayout {
        address principalRecipient;
        uint64 version;
        bool active;
    }
}
