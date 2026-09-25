// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

import { IVitlaneTestUSD } from "../interfaces/IVitlaneTestUSD.sol";

contract VitlaneFaucet {
    IVitlaneTestUSD public immutable token;
    address public admin;
    address public pauser;
    uint256 public claimAmount;
    uint256 public cooldown;
    uint256 public globalDailyCap;
    bool public paused;

    mapping(address => uint256) public lastClaimAt;
    mapping(uint256 => uint256) public claimedByDay;

    error Unauthorized();
    error ZeroAddress();
    error InvalidPolicy();
    error Paused();
    error CooldownActive(uint256 claimableAt);
    error DailyCapExceeded();

    event Claimed(address indexed account, uint256 amount, uint256 indexed day);
    event PolicyChanged(uint256 claimAmount, uint256 cooldown, uint256 globalDailyCap);
    event PauseChanged(bool paused);
    event AdminTransferred(address indexed previousAdmin, address indexed nextAdmin);
    event PauserChanged(address indexed previousPauser, address indexed nextPauser);

    constructor(
        address tokenAddress,
        address initialAdmin,
        address initialPauser,
        uint256 initialClaimAmount,
        uint256 initialCooldown,
        uint256 initialDailyCap
    ) {
        if (tokenAddress == address(0) || initialAdmin == address(0) || initialPauser == address(0))
        {
            revert ZeroAddress();
        }
        token = IVitlaneTestUSD(tokenAddress);
        admin = initialAdmin;
        pauser = initialPauser;
        _setPolicy(initialClaimAmount, initialCooldown, initialDailyCap);
        emit AdminTransferred(address(0), initialAdmin);
        emit PauserChanged(address(0), initialPauser);
    }

    modifier onlyAdmin() {
        if (msg.sender != admin) revert Unauthorized();
        _;
    }

    function claim() external {
        if (paused) revert Paused();
        uint256 previous = lastClaimAt[msg.sender];
        if (previous != 0 && block.timestamp < previous + cooldown) {
            revert CooldownActive(previous + cooldown);
        }
        uint256 day = block.timestamp / 1 days;
        uint256 nextDailyTotal = claimedByDay[day] + claimAmount;
        if (nextDailyTotal > globalDailyCap) revert DailyCapExceeded();
        lastClaimAt[msg.sender] = block.timestamp;
        claimedByDay[day] = nextDailyTotal;
        token.mint(msg.sender, claimAmount);
        emit Claimed(msg.sender, claimAmount, day);
    }

    function setPolicy(uint256 amount, uint256 cooldownSeconds, uint256 dailyCap)
        external
        onlyAdmin
    {
        _setPolicy(amount, cooldownSeconds, dailyCap);
    }

    function setPaused(bool nextPaused) external {
        if (msg.sender != pauser && msg.sender != admin) revert Unauthorized();
        paused = nextPaused;
        emit PauseChanged(nextPaused);
    }

    function transferAdmin(address nextAdmin) external onlyAdmin {
        if (nextAdmin == address(0)) revert ZeroAddress();
        emit AdminTransferred(admin, nextAdmin);
        admin = nextAdmin;
    }

    function setPauser(address nextPauser) external onlyAdmin {
        if (nextPauser == address(0)) revert ZeroAddress();
        emit PauserChanged(pauser, nextPauser);
        pauser = nextPauser;
    }

    function _setPolicy(uint256 amount, uint256 cooldownSeconds, uint256 dailyCap) private {
        if (amount == 0 || dailyCap < amount) revert InvalidPolicy();
        claimAmount = amount;
        cooldown = cooldownSeconds;
        globalDailyCap = dailyCap;
        emit PolicyChanged(amount, cooldownSeconds, dailyCap);
    }
}
