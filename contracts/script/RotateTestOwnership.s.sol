// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { ScriptBase } from "./ScriptBase.sol";
import { VitlaneTestUSD } from "../src/token/VitlaneTestUSD.sol";
import { VitlaneFaucet } from "../src/faucet/VitlaneFaucet.sol";
import { VitlaneSettlement } from "../src/settlement/VitlaneSettlement.sol";

/// @notice Moves management and fee receipt from the initial bootstrap
/// EOAs to the owner-reviewed TestPhase EOAs. Operational signers stay
/// separated and are verified but never rotated by this script.
contract RotateTestOwnership is ScriptBase {
    function run() external {
        require(block.chainid == 91342, "unexpected chain");
        VitlaneTestUSD token = VitlaneTestUSD(vm.envAddress("TVITUSD_ADDRESS"));
        VitlaneFaucet faucet = VitlaneFaucet(vm.envAddress("FAUCET_ADDRESS"));
        VitlaneSettlement settlement = VitlaneSettlement(vm.envAddress("SETTLEMENT_ADDRESS"));
        address currentAdmin = vm.envAddress("CURRENT_ADMIN");
        address nextAdmin = vm.envAddress("NEXT_ADMIN");
        address currentFee = vm.envAddress("CURRENT_TEST_FEE");
        address nextFee = vm.envAddress("TEST_FEE");

        require(nextAdmin != address(0) && nextAdmin.code.length == 0, "next admin must be EOA");
        require(nextFee != address(0) && nextFee.code.length == 0, "next fee must be EOA");
        require(nextAdmin != nextFee, "admin and fee must differ");
        require(address(faucet.token()) == address(token), "faucet token mismatch");
        require(address(settlement.token()) == address(token), "settlement token mismatch");
        require(token.minter() == address(faucet), "faucet is not token minter");
        require(settlement.feeBps() == 100, "unexpected fee bps");
        require(settlement.authorizer() == vm.envAddress("QUOTE_SIGNER"), "authorizer mismatch");
        require(settlement.finalizer() == vm.envAddress("FINALIZER"), "finalizer mismatch");
        require(settlement.refunder() == vm.envAddress("REFUNDER"), "refunder mismatch");

        bool configureFee = _requiresAddressUpdate(settlement.feeRecipient(), currentFee, nextFee);
        bool configureSettlementPauser =
            _requiresAddressUpdate(settlement.pauser(), currentAdmin, nextAdmin);
        bool configureFaucetPauser =
            _requiresAddressUpdate(faucet.pauser(), currentAdmin, nextAdmin);
        bool transferTokenAdmin = _requiresAddressUpdate(token.admin(), currentAdmin, nextAdmin);
        bool transferFaucetAdmin = _requiresAddressUpdate(faucet.admin(), currentAdmin, nextAdmin);
        bool transferSettlementAdmin =
            _requiresAddressUpdate(settlement.admin(), currentAdmin, nextAdmin);
        if (configureFee || configureSettlementPauser || transferSettlementAdmin) {
            require(settlement.admin() == currentAdmin, "settlement already handed off");
        }
        if (configureFaucetPauser || transferFaucetAdmin) {
            require(faucet.admin() == currentAdmin, "faucet already handed off");
        }

        vm.startBroadcast();
        if (configureFee) settlement.configureFee(100, nextFee);
        if (configureSettlementPauser) settlement.setPauser(nextAdmin);
        if (configureFaucetPauser) faucet.setPauser(nextAdmin);
        if (transferTokenAdmin) token.transferAdmin(nextAdmin);
        if (transferFaucetAdmin) faucet.transferAdmin(nextAdmin);
        if (transferSettlementAdmin) settlement.transferAdmin(nextAdmin);
        vm.stopBroadcast();

        require(token.admin() == nextAdmin, "token admin mismatch");
        require(faucet.admin() == nextAdmin, "faucet admin mismatch");
        require(faucet.pauser() == nextAdmin, "faucet pauser mismatch");
        require(settlement.admin() == nextAdmin, "settlement admin mismatch");
        require(settlement.pauser() == nextAdmin, "settlement pauser mismatch");
        require(settlement.feeRecipient() == nextFee, "fee recipient mismatch");
    }

    function _requiresAddressUpdate(address current, address expectedCurrent, address target)
        private
        pure
        returns (bool)
    {
        if (current == target) return false;
        require(current == expectedCurrent, "unexpected current address");
        return true;
    }
}
