// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { TestBase } from "../TestBase.sol";
import { VitlaneTestUSD } from "../../src/token/VitlaneTestUSD.sol";

contract VitlaneTestUSDTest is TestBase {
    VitlaneTestUSD internal token;
    address internal minter = address(0x1001);
    address internal user = address(0x1002);

    function setUp() public {
        token = new VitlaneTestUSD(address(this));
        token.setMinter(minter);
    }

    function testMetadataAndFaucetOnlyMint() public {
        assertEq(token.decimals(), 6, "decimals");
        vm.expectRevert(VitlaneTestUSD.Unauthorized.selector);
        token.mint(user, 1_000_000);

        vm.prank(minter);
        token.mint(user, 1_000_000);
        assertEq(token.balanceOf(user), 1_000_000, "minted balance");
    }

    function testExactAllowanceConsumption() public {
        vm.prank(minter);
        token.mint(user, 50_000_000);
        vm.prank(user);
        token.approve(address(this), 50_000_000);
        token.transferFrom(user, address(0x2001), 50_000_000);
        assertEq(token.allowance(user, address(this)), 0, "allowance consumed");
        assertEq(token.balanceOf(address(0x2001)), 50_000_000, "recipient balance");
    }
}
