// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { TestBase, Vm } from "../TestBase.sol";
import { VitlaneTestUSD } from "../../src/token/VitlaneTestUSD.sol";
import { VitlaneSettlement } from "../../src/settlement/VitlaneSettlement.sol";
import { SettlementTypes } from "../../src/libraries/SettlementTypes.sol";

contract SettlementHandler {
    Vm private constant vm = Vm(address(uint160(uint256(keccak256("hevm cheat code")))));
    VitlaneSettlement public immutable settlement;
    bytes32 public immutable orderHash;
    uint64 public immutable refundAfter;
    uint256 private refundNonce;

    constructor(VitlaneSettlement target, bytes32 order, uint64 refundTimestamp) {
        settlement = target;
        orderHash = order;
        refundAfter = refundTimestamp;
    }

    function complete() external {
        try settlement.complete(orderHash, keccak256("INVARIANT_FULFILLMENT")) { } catch { }
    }

    function refund() external {
        vm.warp(refundAfter);
        try settlement.refund(orderHash) { } catch { }
    }

    function refundPartial(uint96 rawPassPart, uint96 rawFeePart) external {
        refundNonce += 1;
        bytes32 refundKey = keccak256(abi.encode("INVARIANT_REFUND", refundNonce));
        uint256 passPart = uint256(rawPassPart) % 20_000_001;
        uint256 feePart = uint256(rawFeePart) % 200_001;
        try settlement.refundPartial(orderHash, passPart, feePart, refundKey) { } catch { }
    }
}

contract SettlementInvariantTest is TestBase {
    uint256 private constant AUTHORIZER_KEY = 0xB0B;
    uint256 private constant PASS_THROUGH = 50_000_000;
    uint256 private constant FEE = 500_000;
    address private constant FEE_RECIPIENT = address(0x7001);
    address private constant PRINCIPAL_RECIPIENT = address(0x7002);
    bytes32 private constant MERCHANT_ID = keccak256("AMAZON_US");

    VitlaneTestUSD private token;
    VitlaneSettlement private settlement;
    SettlementHandler private handler;
    bytes32 private orderHash;

    function setUp() public {
        token = new VitlaneTestUSD(address(this));
        token.setMinter(address(this));
        token.mint(address(this), 1_000_000_000);
        settlement = new VitlaneSettlement(
            address(token),
            address(this),
            address(this),
            vm.addr(AUTHORIZER_KEY),
            address(this),
            address(this),
            100,
            FEE_RECIPIENT,
            1 hours
        );
        settlement.configureMerchant(MERCHANT_ID, PRINCIPAL_RECIPIENT, 1, true);
        orderHash = keccak256("INVARIANT_ORDER");
        uint64 payDeadline = uint64(block.timestamp + 1 days);
        SettlementTypes.PaymentAuthorization memory authorization =
            SettlementTypes.PaymentAuthorization({
                payer: address(this),
                token: address(token),
                passThroughAmount: PASS_THROUGH,
                feeAmount: FEE,
                orderHash: orderHash,
                merchantId: MERCHANT_ID,
                merchantRegistryVersion: 1,
                feeBps: 100,
                feeRecipient: FEE_RECIPIENT,
                principalRecipient: PRINCIPAL_RECIPIENT,
                assuranceLevel: keccak256("MOCK_DOJANG_VERIFIED"),
                nonce: 1,
                payDeadline: payDeadline,
                refundAfter: payDeadline + 1 hours
            });
        (uint8 v, bytes32 r, bytes32 s) =
            vm.sign(AUTHORIZER_KEY, settlement.hashAuthorization(authorization));
        token.approve(address(settlement), PASS_THROUGH + FEE);
        settlement.pay(authorization, abi.encodePacked(r, s, v));
        // 수취 계좌 allowance = COMPLETED pass-through·fee 환불의 pull 재원.
        vm.prank(FEE_RECIPIENT);
        token.approve(address(settlement), type(uint256).max);
        vm.prank(PRINCIPAL_RECIPIENT);
        token.approve(address(settlement), type(uint256).max);
        handler = new SettlementHandler(settlement, orderHash, authorization.refundAfter);
        settlement.setFinalizer(address(handler));
        settlement.setRefunder(address(handler));
    }

    function targetContracts() public view returns (address[] memory targets) {
        targets = new address[](1);
        targets[0] = address(handler);
    }

    /// @dev escrow 잔액은 항상 "미환불 pass-through 부채"와 일치해야 한다. fee는
    /// pay 시점에 이미 이체돼 escrow에 없다.
    function invariant_EscrowEqualsOutstandingPassThroughLiability() public view {
        (, uint256 passGross,, uint256 passRefunded,,,,, SettlementTypes.PaymentState state) =
            settlement.payments(orderHash);
        uint256 escrow = token.balanceOf(address(settlement));
        if (state == SettlementTypes.PaymentState.ESCROWED) {
            assertEq(escrow, passGross - passRefunded, "escrow liability");
        } else {
            assertEq(escrow, 0, "terminal escrow");
        }
    }

    /// @dev 환불 누적은 gross를 절대 넘지 못한다.
    function invariant_RefundNeverExceedsGross() public view {
        (, uint256 passGross, uint256 feeGross, uint256 passRefunded, uint256 feeRefunded,,,,) =
            settlement.payments(orderHash);
        assertTrue(passRefunded <= passGross, "pass-through cap");
        assertTrue(feeRefunded <= feeGross, "fee cap");
    }

    function invariant_TotalSupplyIsConserved() public view {
        uint256 accounted = token.balanceOf(address(this)) + token.balanceOf(address(settlement))
            + token.balanceOf(FEE_RECIPIENT) + token.balanceOf(PRINCIPAL_RECIPIENT);
        assertEq(accounted, token.totalSupply(), "supply conservation");
    }
}
