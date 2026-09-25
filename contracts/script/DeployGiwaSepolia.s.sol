// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { ScriptBase } from "./ScriptBase.sol";
import { VitlaneTestUSD } from "../src/token/VitlaneTestUSD.sol";
import { VitlaneFaucet } from "../src/faucet/VitlaneFaucet.sol";
import { VitlaneSettlement } from "../src/settlement/VitlaneSettlement.sol";

contract DeployGiwaSepolia is ScriptBase {
    struct Deployment {
        VitlaneTestUSD token;
        VitlaneFaucet faucet;
        VitlaneSettlement settlement;
    }

    function run() external returns (Deployment memory deployment) {
        address admin = vm.envAddress("DEPLOYER");
        address pauser = vm.envAddress("PAUSER");
        address authorizer = vm.envAddress("QUOTE_SIGNER");
        address finalizer = vm.envAddress("FINALIZER");
        address refunder = vm.envAddress("REFUNDER");
        address feeRecipient = vm.envAddress("TEST_FEE");
        uint256 claimAmount = vm.envUint("FAUCET_CLAIM_AMOUNT");
        uint256 cooldown = vm.envUint("FAUCET_COOLDOWN_SECONDS");
        uint256 dailyCap = vm.envUint("FAUCET_DAILY_CAP");
        uint256 escrowWindow = vm.envUint("MINIMUM_ESCROW_WINDOW_SECONDS");
        require(escrowWindow <= type(uint64).max, "escrow window exceeds uint64");

        vm.startBroadcast();
        deployment.token = new VitlaneTestUSD(admin);
        deployment.faucet = new VitlaneFaucet(
            address(deployment.token), admin, pauser, claimAmount, cooldown, dailyCap
        );
        deployment.settlement = new VitlaneSettlement(
            address(deployment.token),
            admin,
            pauser,
            authorizer,
            finalizer,
            refunder,
            100,
            feeRecipient,
            uint64(escrowWindow)
        );
        vm.stopBroadcast();
    }
}
