// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { TestBase } from "../TestBase.sol";
import { VitlaneTestUSD } from "../../src/token/VitlaneTestUSD.sol";
import { VitlaneFaucet } from "../../src/faucet/VitlaneFaucet.sol";
import { VitlaneSettlement } from "../../src/settlement/VitlaneSettlement.sol";
import { SettlementTypes } from "../../src/libraries/SettlementTypes.sol";

contract VitlaneSettlementTest is TestBase {
    uint256 internal constant AUTHORIZER_KEY = 0xA11CE;
    address internal constant PAYER = address(0x4001);
    address internal constant FINALIZER = address(0x4002);
    address internal constant REFUNDER = address(0x4003);
    address internal constant FEE_RECIPIENT = address(0x4004);
    address internal constant PRINCIPAL_RECIPIENT = address(0x4005);
    bytes32 internal constant MERCHANT_ID = keccak256("AMAZON_US");
    bytes32 internal constant ASSURANCE = keccak256("MOCK_DOJANG_VERIFIED");

    VitlaneTestUSD internal token;
    VitlaneFaucet internal faucet;
    VitlaneSettlement internal settlement;

    function setUp() public {
        token = new VitlaneTestUSD(address(this));
        faucet = new VitlaneFaucet(
            address(token), address(this), address(this), 1_000_000_000, 0, 10_000_000_000
        );
        token.setMinter(address(faucet));
        settlement = new VitlaneSettlement(
            address(token),
            address(this),
            address(this),
            vm.addr(AUTHORIZER_KEY),
            FINALIZER,
            REFUNDER,
            100,
            FEE_RECIPIENT,
            1 hours
        );
        settlement.configureMerchant(MERCHANT_ID, PRINCIPAL_RECIPIENT, 1, true);
        vm.prank(PAYER);
        faucet.claim();
        vm.prank(PAYER);
        token.approve(address(settlement), type(uint256).max);
        // 수취 계좌 allowance는 배포 절차의 일부다(ADR-0050 — PayPal merchant
        // 잔액 환불과 동형인 pull 재원).
        vm.prank(FEE_RECIPIENT);
        token.approve(address(settlement), type(uint256).max);
        vm.prank(PRINCIPAL_RECIPIENT);
        token.approve(address(settlement), type(uint256).max);
    }

    function testPayCollectsFeeImmediatelyAndCompleteReleasesPassThrough() public {
        uint256 passThrough = 49_500_000;
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(passThrough, 1);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
        // 수수료 수취 시점 = 결제 시점, escrow는 pass-through만.
        assertEq(token.balanceOf(FEE_RECIPIENT), _fee(passThrough), "fee at pay");
        assertEq(token.balanceOf(address(settlement)), passThrough, "escrow pass-through");

        vm.prank(FINALIZER);
        settlement.complete(authorization.orderHash, keccak256("SIMULATED"));
        assertEq(token.balanceOf(PRINCIPAL_RECIPIENT), passThrough, "principal full remainder");
        assertEq(token.balanceOf(address(settlement)), 0, "escrow released");
    }

    function testRefundPartialAccumulatesWithinCapsAndBlocksReplay() public {
        uint256 passThrough = 50_000_000;
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(passThrough, 2);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
        uint256 payerBefore = token.balanceOf(PAYER);

        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 10_000_000, 0, keccak256("r1"));
        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 15_000_000, 100_000, keccak256("r2"));
        assertEq(token.balanceOf(PAYER), payerBefore + 25_100_000, "cumulative refund");
        assertEq(token.balanceOf(address(settlement)), passThrough - 25_000_000, "escrow drained");

        // 누적 cap 초과·key replay·zero refund는 전부 거절된다.
        vm.expectRevert(VitlaneSettlement.RefundExceedsGross.selector);
        vm.prank(REFUNDER);
        settlement.refundPartial(
            authorization.orderHash, passThrough - 25_000_000 + 1, 0, keccak256("r3")
        );
        vm.expectRevert(VitlaneSettlement.RefundKeyUsed.selector);
        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 1, 0, keccak256("r1"));
        vm.expectRevert(VitlaneSettlement.ZeroRefund.selector);
        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 0, 0, keccak256("r4"));
        vm.expectRevert(VitlaneSettlement.Unauthorized.selector);
        vm.prank(address(0xBAD));
        settlement.refundPartial(authorization.orderHash, 1, 0, keccak256("r5"));
    }

    function testRefundPartialAfterCompletePullsFromRecipients() public {
        uint256 passThrough = 50_000_000;
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(passThrough, 3);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
        vm.prank(FINALIZER);
        settlement.complete(authorization.orderHash, keccak256("SIMULATED"));
        uint256 payerBefore = token.balanceOf(PAYER);

        // COMPLETED 이후 환불 = 수취 계좌 allowance-pull (PayPal merchant 잔액
        // 환불과 동형).
        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 20_000_000, 200_000, keccak256("pc1"));
        assertEq(token.balanceOf(PAYER), payerBefore + 20_200_000, "post-complete refund");
        assertEq(token.balanceOf(PRINCIPAL_RECIPIENT), passThrough - 20_000_000, "principal pulled");
        assertEq(token.balanceOf(FEE_RECIPIENT), _fee(passThrough) - 200_000, "fee pulled back");

        // 수취 계좌 allowance가 없으면 revert — 잔액 부족 환불 실패 클래스.
        vm.prank(PRINCIPAL_RECIPIENT);
        token.approve(address(settlement), 0);
        vm.expectRevert(VitlaneTestUSD.InsufficientAllowance.selector);
        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 1_000_000, 0, keccak256("pc2"));
    }

    function testFullRefundBeforeCompleteReachesRefundedTerminal() public {
        uint256 passThrough = 30_000_000;
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(passThrough, 4);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);

        vm.prank(REFUNDER);
        settlement.refundPartial(
            authorization.orderHash, passThrough, _fee(passThrough), keccak256("full")
        );
        (,,, uint256 passRefunded, uint256 feeRefunded,,,, SettlementTypes.PaymentState state) =
            settlement.payments(authorization.orderHash);
        assertEq(passRefunded, passThrough, "pass-through fully refunded");
        assertEq(feeRefunded, _fee(passThrough), "fee fully refunded");
        assertTrue(state == SettlementTypes.PaymentState.REFUNDED, "terminal refunded");

        vm.expectRevert(VitlaneSettlement.PaymentNotEscrowed.selector);
        vm.prank(FINALIZER);
        settlement.complete(authorization.orderHash, keccak256("SIMULATED"));
    }

    function testSelfRefundEscapeReturnsEscrowRemainderOnly() public {
        uint256 passThrough = 50_000_000;
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(passThrough, 5);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
        uint256 feeAtPay = token.balanceOf(FEE_RECIPIENT);
        uint256 payerBefore = token.balanceOf(PAYER);

        vm.warp(authorization.refundAfter);
        vm.prank(address(0xCAFE));
        settlement.refund(authorization.orderHash);
        // escape hatch는 컨트랙트가 쥔 escrow 잔여만 돌려준다. fee는 refunder의
        // refundPartial 경로다.
        assertEq(token.balanceOf(PAYER), payerBefore + passThrough, "escrow remainder");
        assertEq(token.balanceOf(FEE_RECIPIENT), feeAtPay, "fee untouched by escape hatch");
        vm.expectRevert(VitlaneSettlement.RefundNotAvailable.selector);
        vm.prank(address(0xCAFE));
        settlement.refund(authorization.orderHash);

        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 0, _fee(passThrough), keccak256("fee"));
        (,,,,,,,, SettlementTypes.PaymentState state) = settlement.payments(authorization.orderHash);
        assertTrue(state == SettlementTypes.PaymentState.REFUNDED, "refunded after fee return");
    }

    function testCompleteReleasesOnlyRemainderAfterPartialRefund() public {
        uint256 passThrough = 40_000_000;
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(passThrough, 6);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
        vm.prank(REFUNDER);
        settlement.refundPartial(authorization.orderHash, 15_000_000, 0, keccak256("part"));

        vm.prank(FINALIZER);
        settlement.complete(authorization.orderHash, keccak256("SIMULATED"));
        assertEq(
            token.balanceOf(PRINCIPAL_RECIPIENT), passThrough - 15_000_000, "remainder released"
        );
        assertEq(token.balanceOf(address(settlement)), 0, "escrow empty");
    }

    function testFeeBoundAllowsCentCeilAndRejectsOverstatedFee() public {
        // $0.99 pass-through의 cent-ceil fee(1 cent = 10_000 base units)는 반올림
        // 슬랙 안에서 수용된다.
        SettlementTypes.PaymentAuthorization memory centCeil = _authorization(990_000, 70);
        centCeil.feeAmount = 10_000;
        bytes memory centCeilSignature = _sign(centCeil);
        vm.prank(PAYER);
        settlement.pay(centCeil, centCeilSignature);

        // 요율 + 슬랙을 넘는 fee는 거절된다.
        SettlementTypes.PaymentAuthorization memory overstated = _authorization(50_000_000, 7);
        overstated.feeAmount = 50_000_000 * 100 / 10_000 + settlement.FEE_ROUNDING_SLACK() + 1;
        bytes memory overstatedSignature = _sign(overstated);
        vm.expectRevert(VitlaneSettlement.FeeMismatch.selector);
        vm.prank(PAYER);
        settlement.pay(overstated, overstatedSignature);
    }

    function testPauseBlocksPayButNeverRefund() public {
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(50_000_000, 8);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
        settlement.setPaused(true);
        SettlementTypes.PaymentAuthorization memory blocked = _authorization(50_000_000, 80);
        bytes memory blockedSignature = _sign(blocked);
        vm.expectRevert(VitlaneSettlement.Paused.selector);
        vm.prank(PAYER);
        settlement.pay(blocked, blockedSignature);
        vm.prank(REFUNDER);
        settlement.refund(authorization.orderHash);
        assertEq(token.balanceOf(address(settlement)), 0, "refund while paused");
        vm.prank(REFUNDER);
        settlement.refundPartial(
            authorization.orderHash, 0, _fee(50_000_000), keccak256("paused-fee")
        );
    }

    function testRejectsRegistryVersionMismatch() public {
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(50_000_000, 9);
        authorization.merchantRegistryVersion = 2;
        bytes memory signature = _sign(authorization);
        vm.expectRevert(VitlaneSettlement.RegistryMismatch.selector);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
    }

    function testRejectsWrongPayerAndSigner() public {
        SettlementTypes.PaymentAuthorization memory authorization = _authorization(50_000_000, 10);
        bytes memory signature = _sign(authorization);
        vm.expectRevert(VitlaneSettlement.InvalidAuthorization.selector);
        vm.prank(address(0xBAD));
        settlement.pay(authorization, signature);

        (uint8 v, bytes32 r, bytes32 s) =
            vm.sign(0xB0B, settlement.hashAuthorization(authorization));
        vm.expectRevert(VitlaneSettlement.InvalidAuthorization.selector);
        vm.prank(PAYER);
        settlement.pay(authorization, abi.encodePacked(r, s, v));
    }

    function testRejectsExpiredAuthorizationAndInsufficientAllowance() public {
        SettlementTypes.PaymentAuthorization memory expired = _authorization(50_000_000, 11);
        bytes memory expiredSignature = _sign(expired);
        vm.warp(expired.payDeadline + 1);
        vm.expectRevert(VitlaneSettlement.AuthorizationExpired.selector);
        vm.prank(PAYER);
        settlement.pay(expired, expiredSignature);

        SettlementTypes.PaymentAuthorization memory noAllowance = _authorization(50_000_000, 12);
        bytes memory noAllowanceSignature = _sign(noAllowance);
        vm.prank(PAYER);
        token.approve(address(settlement), 0);
        vm.expectRevert(VitlaneTestUSD.InsufficientAllowance.selector);
        vm.prank(PAYER);
        settlement.pay(noAllowance, noAllowanceSignature);
    }

    function testRejectsReplayAndOppositeTerminalTransition() public {
        SettlementTypes.PaymentAuthorization memory completed = _authorization(50_000_000, 13);
        bytes memory completedSignature = _sign(completed);
        vm.prank(PAYER);
        settlement.pay(completed, completedSignature);
        vm.expectRevert(VitlaneSettlement.NonceUsed.selector);
        vm.prank(PAYER);
        settlement.pay(completed, completedSignature);
        vm.prank(FINALIZER);
        settlement.complete(completed.orderHash, keccak256("SIMULATED"));
        // COMPLETED 뒤 escape hatch refund()는 없다 — 환불은 refundPartial 경로.
        vm.expectRevert(VitlaneSettlement.PaymentNotEscrowed.selector);
        vm.prank(REFUNDER);
        settlement.refund(completed.orderHash);

        SettlementTypes.PaymentAuthorization memory refunded = _authorization(50_000_000, 14);
        bytes memory refundedSignature = _sign(refunded);
        vm.prank(PAYER);
        settlement.pay(refunded, refundedSignature);
        vm.prank(REFUNDER);
        settlement.refund(refunded.orderHash);
        vm.expectRevert(VitlaneSettlement.PaymentNotEscrowed.selector);
        vm.prank(FINALIZER);
        settlement.complete(refunded.orderHash, keccak256("SIMULATED"));
    }

    function _fee(uint256 passThrough) internal pure returns (uint256) {
        return (passThrough * 100 + 9_999) / 10_000; // ceil 1%
    }

    function _authorization(uint256 passThrough, uint256 nonce)
        internal
        view
        returns (SettlementTypes.PaymentAuthorization memory)
    {
        uint64 payDeadline = uint64(block.timestamp + 1 days);
        return SettlementTypes.PaymentAuthorization({
            payer: PAYER,
            token: address(token),
            passThroughAmount: passThrough,
            feeAmount: _fee(passThrough),
            orderHash: keccak256(abi.encode("purchase", nonce)),
            merchantId: MERCHANT_ID,
            merchantRegistryVersion: 1,
            feeBps: 100,
            feeRecipient: FEE_RECIPIENT,
            principalRecipient: PRINCIPAL_RECIPIENT,
            assuranceLevel: ASSURANCE,
            nonce: nonce,
            payDeadline: payDeadline,
            refundAfter: payDeadline + 1 hours
        });
    }

    function _sign(SettlementTypes.PaymentAuthorization memory authorization)
        internal
        returns (bytes memory)
    {
        (uint8 v, bytes32 r, bytes32 s) =
            vm.sign(AUTHORIZER_KEY, settlement.hashAuthorization(authorization));
        return abi.encodePacked(r, s, v);
    }
}
