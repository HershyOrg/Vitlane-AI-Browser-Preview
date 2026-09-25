// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { ScriptBase } from "./ScriptBase.sol";
import { VitlaneTestUSD } from "../src/token/VitlaneTestUSD.sol";
import { VitlaneFaucet } from "../src/faucet/VitlaneFaucet.sol";
import { VitlaneSettlement } from "../src/settlement/VitlaneSettlement.sol";

contract VerifyDeployment is ScriptBase {
    function run() external returns (bool) {
        VitlaneTestUSD token = VitlaneTestUSD(vm.envAddress("TVITUSD_ADDRESS"));
        VitlaneFaucet faucet = VitlaneFaucet(vm.envAddress("FAUCET_ADDRESS"));
        VitlaneSettlement settlement = VitlaneSettlement(vm.envAddress("SETTLEMENT_ADDRESS"));
        address quoteSigner = vm.envAddress("QUOTE_SIGNER");
        address finalizer = vm.envAddress("FINALIZER");
        address refunder = vm.envAddress("REFUNDER");
        address pauser = vm.envAddress("PAUSER");
        address settlementAdmin = vm.envAddress("SETTLEMENT_ADMIN");
        address feeRecipient = vm.envAddress("TEST_FEE");
        uint256 claimAmount = vm.envUint("FAUCET_CLAIM_AMOUNT");
        uint256 cooldown = vm.envUint("FAUCET_COOLDOWN_SECONDS");
        uint256 dailyCap = vm.envUint("FAUCET_DAILY_CAP");
        require(block.chainid == 91342, "unexpected chain");
        require(token.decimals() == 6, "unexpected decimals");
        require(token.minter() == address(faucet), "faucet is not sole minter");
        require(address(faucet.token()) == address(token), "faucet token mismatch");
        require(faucet.claimAmount() == claimAmount, "unexpected claim amount");
        require(faucet.cooldown() == cooldown, "unexpected cooldown");
        require(faucet.globalDailyCap() == dailyCap, "unexpected daily cap");
        require(address(settlement.token()) == address(token), "settlement token mismatch");
        require(settlement.feeBps() == 100, "unexpected fee");
        require(settlement.authorizer() == quoteSigner, "unexpected authorizer");
        require(settlement.finalizer() == finalizer, "unexpected finalizer");
        require(settlement.refunder() == refunder, "unexpected refunder");
        require(settlement.pauser() == pauser, "unexpected pauser");
        require(token.admin() == settlementAdmin, "unexpected token admin");
        require(faucet.admin() == settlementAdmin, "unexpected faucet admin");
        require(faucet.pauser() == pauser, "unexpected faucet pauser");
        require(settlement.admin() == settlementAdmin, "unexpected settlement admin");
        require(settlement.feeRecipient() == feeRecipient, "unexpected fee recipient");
        address principal = vm.envAddress("GENERIC_WEB_TEST_PRINCIPAL");
        require(vm.envAddress("AMAZON_TEST_PRINCIPAL") == principal, "Amazon principal differs");
        require(vm.envAddress("WALMART_TEST_PRINCIPAL") == principal, "Walmart principal differs");
        require(vm.envAddress("SHOPIFY_TEST_PRINCIPAL") == principal, "Shopify principal differs");
        _verifyMerchant(
            settlement, "AMAZON_US", principal, vm.envUint("AMAZON_TEST_REGISTRY_VERSION")
        );
        _verifyMerchant(
            settlement, "WALMART_US", principal, vm.envUint("WALMART_TEST_REGISTRY_VERSION")
        );
        _verifyMerchant(
            settlement, "SHOPIFY_UCP", principal, vm.envUint("SHOPIFY_TEST_REGISTRY_VERSION")
        );
        _verifyMerchant(
            settlement,
            "GENERIC_WEB_USD",
            principal,
            vm.envUint("GENERIC_WEB_TEST_REGISTRY_VERSION")
        );
        return true;
    }

    function _verifyMerchant(
        VitlaneSettlement settlement,
        string memory merchant,
        address expectedPrincipal,
        uint256 expectedVersion
    ) private view {
        (address principal, uint64 registryVersion, bool active) =
            settlement.merchantPayouts(keccak256(bytes(merchant)));
        require(principal == expectedPrincipal, "unexpected merchant principal");
        require(registryVersion == uint64(expectedVersion), "unexpected merchant registry version");
        require(active, "merchant inactive");
    }
}
