// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { ScriptBase } from "./ScriptBase.sol";
import { VitlaneTestUSD } from "../src/token/VitlaneTestUSD.sol";
import { VitlaneFaucet } from "../src/faucet/VitlaneFaucet.sol";
import { VitlaneSettlement } from "../src/settlement/VitlaneSettlement.sol";

contract VerifyOwnedTestConfiguration is ScriptBase {
    function run() external returns (bool) {
        require(block.chainid == 91342, "unexpected chain");
        VitlaneTestUSD token = VitlaneTestUSD(vm.envAddress("TVITUSD_ADDRESS"));
        VitlaneFaucet faucet = VitlaneFaucet(vm.envAddress("FAUCET_ADDRESS"));
        VitlaneSettlement settlement = VitlaneSettlement(vm.envAddress("SETTLEMENT_ADDRESS"));
        address expectedAdmin = vm.envAddress("NEXT_ADMIN");
        address expectedPrincipal = vm.envAddress("GENERIC_WEB_TEST_PRINCIPAL");

        require(address(faucet.token()) == address(token), "faucet token mismatch");
        require(address(settlement.token()) == address(token), "settlement token mismatch");
        require(token.minter() == address(faucet), "faucet is not token minter");
        require(token.admin() == expectedAdmin, "token admin mismatch");
        require(faucet.admin() == expectedAdmin, "faucet admin mismatch");
        require(faucet.pauser() == expectedAdmin, "faucet pauser mismatch");
        require(settlement.admin() == expectedAdmin, "settlement admin mismatch");
        require(settlement.pauser() == expectedAdmin, "settlement pauser mismatch");
        require(settlement.authorizer() == vm.envAddress("QUOTE_SIGNER"), "authorizer mismatch");
        require(settlement.finalizer() == vm.envAddress("FINALIZER"), "finalizer mismatch");
        require(settlement.refunder() == vm.envAddress("REFUNDER"), "refunder mismatch");
        require(settlement.feeBps() == 100, "fee bps mismatch");
        require(settlement.feeRecipient() == vm.envAddress("TEST_FEE"), "fee recipient mismatch");
        require(!faucet.paused(), "faucet paused");
        require(!settlement.paused(), "settlement paused");

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
        _verifyMerchant(settlement, "AMAZON_US", expectedPrincipal, 2);
        _verifyMerchant(settlement, "WALMART_US", expectedPrincipal, 2);
        _verifyMerchant(settlement, "SHOPIFY_UCP", expectedPrincipal, 2);
        _verifyMerchant(settlement, "GENERIC_WEB_USD", expectedPrincipal, 1);
        return true;
    }

    function _verifyMerchant(
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
