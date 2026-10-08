// SPDX-License-Identifier: MIT
pragma solidity ^0.8.30;

// Disposable, valueless local-EVM fixture. Never deploy as a payment asset.
contract LocalTestToken {
    mapping(address => uint256) public balanceOf;
    event Transfer(address indexed from, address indexed to, uint256 value);

    function decimals() external pure virtual returns (uint8) { return 6; }

    function mint(address to, uint256 value) external {
        balanceOf[to] += value;
        emit Transfer(address(0), to, value);
    }

    function transfer(address to, uint256 value) external returns (bool) {
        require(balanceOf[msg.sender] >= value, "balance");
        balanceOf[msg.sender] -= value;
        balanceOf[to] += value;
        emit Transfer(msg.sender, to, value);
        return true;
    }
}

// BSC assets in the production whitelist use 18 decimals.
contract LocalTestTokenBSC is LocalTestToken {
    function decimals() external pure override returns (uint8) { return 18; }
}
