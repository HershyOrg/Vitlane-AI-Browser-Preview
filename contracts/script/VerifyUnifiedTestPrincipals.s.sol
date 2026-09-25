// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { ScriptBase } from "./ScriptBase.sol";
import { VitlaneSettlement } from "../src/settlement/VitlaneSettlement.sol";

contract VerifyUnifiedTestPrincipals is ScriptBase {
    function run() external returns (bool) {
        require(block.chainid == 91342, "unexpected chain");
        VitlaneSettlement settlement = VitlaneSettlement(vm.envAddress("SETTLEMENT_ADDRESS"));
        address expectedPrincipal = vm.envAddress("GENERIC_WEB_TEST_PRINCIPAL");
        require(
            vm.envAddress("AMAZON_TEST_PRINCIPAL") == expectedPrincipal, "Amazon principal differs"
        );
        require(
            vm.envAddress("WALMART_TEST_PRINCIPAL") == expectedPrincipal,
            "Walmart principal differs"
        );
        require(
            vm.envAddress("SHOPIFY_TEST_PRINCIPAL") == expectedPrincipal,
            "Shopify principal differs"
        );
        _verify(settlement, "AMAZON_US", expectedPrincipal, 2);
        _verify(settlement, "WALMART_US", expectedPrincipal, 2);
        _verify(settlement, "SHOPIFY_UCP", expectedPrincipal, 2);
        _verify(settlement, "GENERIC_WEB_USD", expectedPrincipal, 1);
        return true;
    }

    function _verify(
        VitlaneSettlement settlement,
        string memory merchant,
        address expectedPrincipal,
        uint64 expectedVersion
    ) private view {
        (address principal, uint64 registryVersion, bool active) =
            settlement.merchantPayouts(keccak256(bytes(merchant)));
        require(principal == expectedPrincipal, "unexpected merchant principal");
        require(registryVersion == expectedVersion, "unexpected merchant registry version");
        require(active, "merchant inactive");
    }
}
