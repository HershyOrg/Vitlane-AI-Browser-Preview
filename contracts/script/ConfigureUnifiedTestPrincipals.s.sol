// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { ScriptBase } from "./ScriptBase.sol";
import { VitlaneSettlement } from "../src/settlement/VitlaneSettlement.sol";

/// @notice One-time post-handoff configuration that moves every active TEST
/// settlement path to one reviewed receive-only principal. Existing merchant
/// entries advance to version 2; Generic Web is introduced at version 1.
contract ConfigureUnifiedTestPrincipals is ScriptBase {
    bytes32 private constant AMAZON_US = keccak256("AMAZON_US");
    bytes32 private constant WALMART_US = keccak256("WALMART_US");
    bytes32 private constant SHOPIFY_UCP = keccak256("SHOPIFY_UCP");
    bytes32 private constant GENERIC_WEB_USD = keccak256("GENERIC_WEB_USD");

    address private constant V1_AMAZON_PRINCIPAL = 0xF08606DA22deA6E5ccaA419f99fBC18F3B50F9f1;
    address private constant V1_WALMART_PRINCIPAL = 0xf82F010fE7d1b1BB3C230abB9bF3B55A6FE0FeD1;
    address private constant V1_SHOPIFY_PRINCIPAL = 0x2732c8F92E5DCF9E571F3adcD9c7a392ea975c1a;

    function run() external {
        require(block.chainid == 91342, "unexpected chain");
        VitlaneSettlement settlement = VitlaneSettlement(vm.envAddress("SETTLEMENT_ADDRESS"));
        address principal = vm.envAddress("GENERIC_WEB_TEST_PRINCIPAL");
        address expectedAdmin = vm.envAddress("SETTLEMENT_ADMIN");
        require(settlement.admin() == expectedAdmin, "unexpected settlement admin");
        require(principal != address(0), "zero test principal");
        require(principal.code.length == 0, "test principal must be EOA");
        require(vm.envAddress("AMAZON_TEST_PRINCIPAL") == principal, "Amazon principal differs");
        require(vm.envAddress("WALMART_TEST_PRINCIPAL") == principal, "Walmart principal differs");
        require(vm.envAddress("SHOPIFY_TEST_PRINCIPAL") == principal, "Shopify principal differs");

        bool configureAmazon =
            _requiresLegacyUpdate(settlement, AMAZON_US, V1_AMAZON_PRINCIPAL, principal);
        bool configureWalmart =
            _requiresLegacyUpdate(settlement, WALMART_US, V1_WALMART_PRINCIPAL, principal);
        bool configureShopify =
            _requiresLegacyUpdate(settlement, SHOPIFY_UCP, V1_SHOPIFY_PRINCIPAL, principal);
        bool configureGeneric = _requiresGenericUpdate(settlement, principal);

        vm.startBroadcast();
        if (configureAmazon) settlement.configureMerchant(AMAZON_US, principal, 2, true);
        if (configureWalmart) settlement.configureMerchant(WALMART_US, principal, 2, true);
        if (configureShopify) settlement.configureMerchant(SHOPIFY_UCP, principal, 2, true);
        if (configureGeneric) settlement.configureMerchant(GENERIC_WEB_USD, principal, 1, true);
        vm.stopBroadcast();

        _verify(settlement, AMAZON_US, principal, 2);
        _verify(settlement, WALMART_US, principal, 2);
        _verify(settlement, SHOPIFY_UCP, principal, 2);
        _verify(settlement, GENERIC_WEB_USD, principal, 1);
    }

    function _requiresLegacyUpdate(
        VitlaneSettlement settlement,
        bytes32 merchantId,
        address v1Principal,
        address targetPrincipal
    ) private view returns (bool) {
        (address currentPrincipal, uint64 currentVersion, bool currentActive) =
            settlement.merchantPayouts(merchantId);
        if (currentPrincipal == targetPrincipal && currentVersion == 2 && currentActive) {
            return false;
        }
        require(currentPrincipal == v1Principal, "unexpected v1 principal");
        require(currentVersion == 1, "unexpected v1 registry version");
        require(currentActive, "v1 merchant inactive");
        return true;
    }

    function _requiresGenericUpdate(VitlaneSettlement settlement, address targetPrincipal)
        private
        view
        returns (bool)
    {
        (address currentPrincipal, uint64 currentVersion, bool currentActive) =
            settlement.merchantPayouts(GENERIC_WEB_USD);
        if (currentPrincipal == targetPrincipal && currentVersion == 1 && currentActive) {
            return false;
        }
        require(
            currentPrincipal == address(0) && currentVersion == 0 && !currentActive,
            "generic merchant already configured differently"
        );
        return true;
    }

    function _verify(
        VitlaneSettlement settlement,
        bytes32 merchantId,
        address expectedPrincipal,
        uint64 expectedVersion
    ) private view {
        (address principal, uint64 registryVersion, bool active) =
            settlement.merchantPayouts(merchantId);
        require(principal == expectedPrincipal, "principal mismatch");
        require(registryVersion == expectedVersion, "registry version mismatch");
        require(active, "merchant inactive");
    }
}
