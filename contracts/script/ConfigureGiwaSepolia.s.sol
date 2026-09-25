// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { ScriptBase } from "./ScriptBase.sol";
import { VitlaneTestUSD } from "../src/token/VitlaneTestUSD.sol";
import { VitlaneFaucet } from "../src/faucet/VitlaneFaucet.sol";
import { VitlaneSettlement } from "../src/settlement/VitlaneSettlement.sol";

contract ConfigureGiwaSepolia is ScriptBase {
    function run() external {
        VitlaneTestUSD token = VitlaneTestUSD(vm.envAddress("TVITUSD_ADDRESS"));
        VitlaneFaucet faucet = VitlaneFaucet(vm.envAddress("FAUCET_ADDRESS"));
        VitlaneSettlement settlement = VitlaneSettlement(vm.envAddress("SETTLEMENT_ADDRESS"));
        address settlementAdmin = vm.envAddress("SETTLEMENT_ADMIN");
        address principal = vm.envAddress("GENERIC_WEB_TEST_PRINCIPAL");
        require(vm.envAddress("AMAZON_TEST_PRINCIPAL") == principal, "Amazon principal differs");
        require(vm.envAddress("WALMART_TEST_PRINCIPAL") == principal, "Walmart principal differs");
        require(vm.envAddress("SHOPIFY_TEST_PRINCIPAL") == principal, "Shopify principal differs");

        // registry version은 merchant 갱신 이력의 몫이라 재배포에서도 응용 DB의
        // 현행 버전을 그대로 등록한다(신규 환경은 전부 1).
        vm.startBroadcast();
        token.setMinter(address(faucet));
        settlement.configureMerchant(
            keccak256("AMAZON_US"),
            principal,
            uint64(vm.envUint("AMAZON_TEST_REGISTRY_VERSION")),
            true
        );
        settlement.configureMerchant(
            keccak256("WALMART_US"),
            principal,
            uint64(vm.envUint("WALMART_TEST_REGISTRY_VERSION")),
            true
        );
        settlement.configureMerchant(
            keccak256("SHOPIFY_UCP"),
            principal,
            uint64(vm.envUint("SHOPIFY_TEST_REGISTRY_VERSION")),
            true
        );
        settlement.configureMerchant(
            keccak256("GENERIC_WEB_USD"),
            principal,
            uint64(vm.envUint("GENERIC_WEB_TEST_REGISTRY_VERSION")),
            true
        );
        token.transferAdmin(settlementAdmin);
        faucet.transferAdmin(settlementAdmin);
        settlement.transferAdmin(settlementAdmin);
        vm.stopBroadcast();
    }
}
