// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { VitlaneSettlementTest } from "../unit/VitlaneSettlement.t.sol";
import { SettlementTypes } from "../../src/libraries/SettlementTypes.sol";

contract SettlementFuzzTest is VitlaneSettlementTest {
    function testFuzz_FeeAtPayAndPrincipalAtCompleteConserveFunds(uint96 rawAmount) public {
        // faucet 1회 claim(1_000_000_000)으로 pass-through + ceil 1% fee를 덮는 상한.
        uint256 passThrough = uint256(rawAmount) % 990_000_000 + 1;
        SettlementTypes.PaymentAuthorization memory authorization =
            _authorization(passThrough, passThrough);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);
        assertEq(token.balanceOf(FEE_RECIPIENT), _fee(passThrough), "fee at pay");
        vm.prank(FINALIZER);
        settlement.complete(authorization.orderHash, keccak256("FUZZ_SIMULATED"));
        assertEq(token.balanceOf(PRINCIPAL_RECIPIENT), passThrough, "principal at complete");
        assertEq(token.balanceOf(address(settlement)), 0, "no residue");
    }

    function testFuzz_CumulativePartialRefundConservesFunds(
        uint96 rawAmount,
        uint96 rawFirst,
        uint96 rawSecond
    ) public {
        uint256 passThrough = uint256(rawAmount) % 990_000_000 + 1;
        SettlementTypes.PaymentAuthorization memory authorization =
            _authorization(passThrough, passThrough);
        bytes memory signature = _sign(authorization);
        vm.prank(PAYER);
        settlement.pay(authorization, signature);

        // 잔여 ≥ 1을 유지하는 두 번의 누적 부분환불 뒤 complete가 정확히 잔여만
        // 방출하는지 검증한다.
        uint256 first = uint256(rawFirst) % passThrough;
        uint256 second = uint256(rawSecond) % (passThrough - first);
        if (first != 0) {
            vm.prank(REFUNDER);
            settlement.refundPartial(authorization.orderHash, first, 0, keccak256("fz-1"));
        }
        if (second != 0) {
            vm.prank(REFUNDER);
            settlement.refundPartial(authorization.orderHash, second, 0, keccak256("fz-2"));
        }
        vm.prank(FINALIZER);
        settlement.complete(authorization.orderHash, keccak256("FUZZ_SIMULATED"));

        uint256 fee = _fee(passThrough);
        assertEq(
            token.balanceOf(PAYER),
            1_000_000_000 - passThrough - fee + first + second,
            "payer net of refunds"
        );
        assertEq(
            token.balanceOf(PRINCIPAL_RECIPIENT), passThrough - first - second, "remainder only"
        );
        assertEq(token.balanceOf(FEE_RECIPIENT), fee, "fee untouched");
        assertEq(token.balanceOf(address(settlement)), 0, "no residue");
    }
}
