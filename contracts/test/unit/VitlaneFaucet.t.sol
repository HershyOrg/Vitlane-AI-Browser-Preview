// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { TestBase } from "../TestBase.sol";
import { VitlaneTestUSD } from "../../src/token/VitlaneTestUSD.sol";
import { VitlaneFaucet } from "../../src/faucet/VitlaneFaucet.sol";

contract VitlaneFaucetTest is TestBase {
    VitlaneTestUSD internal token;
    VitlaneFaucet internal faucet;
    address internal user = address(0x3001);

    function setUp() public {
        token = new VitlaneTestUSD(address(this));
        faucet = new VitlaneFaucet(
            address(token), address(this), address(this), 100_000_000, 1 hours, 1_000_000_000
        );
        token.setMinter(address(faucet));
    }

    function testClaimMintsFixedAmount() public {
        vm.prank(user);
        faucet.claim();
        assertEq(token.balanceOf(user), 100_000_000, "claim balance");
    }

    function testCooldownAndPause() public {
        vm.prank(user);
        faucet.claim();
        vm.expectPartialRevert(VitlaneFaucet.CooldownActive.selector);
        vm.prank(user);
        faucet.claim();

        faucet.setPaused(true);
        vm.expectRevert(VitlaneFaucet.Paused.selector);
        vm.prank(address(0x3002));
        faucet.claim();
    }
}
