// Copyright 2021 Evmos Foundation
// This file is part of Evmos' Ethermint library.
//
// The Ethermint library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The Ethermint library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the Ethermint library. If not, see https://github.com/Helios-Chain-Labs/ethermint/blob/main/LICENSE
package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	cosmostracing "github.com/Helios-Chain-Labs/ethermint/x/evm/tracing"
)

// BeginBlock sets the sdk Context and EIP155 chain id to the Keeper.
func (k *Keeper) BeginBlock(ctx sdk.Context) error {
	k.WithChainID(ctx)

	// cache parameters that's common for the whole block.
	evmBlockConfig, err := k.EVMBlockConfig(ctx, k.ChainID())
	if err != nil {
		return err
	}

	// In the case of BeginBlock hook, we can extract the tracer from the context
	if tracer := cosmostracing.GetTracingHooks(ctx); tracer != nil && tracer.OnCosmosBlockStart != nil {
		tracer.OnCosmosBlockStart(
			ToCosmosStartBlockEvent(
				k,
				ctx,
				evmBlockConfig.CoinBase,
				ctx.BlockHeader(),
			),
		)
	}

	return nil
}

// EndBlock also retrieves the bloom filter value from the transient store and commits it to the
// KVStore. The EVM end block logic doesn't update the validator set, thus it returns
// an empty slice.
func (k *Keeper) EndBlock(ctx sdk.Context) error {
	k.CollectTxBloom(ctx)
	k.RemoveParamsCache(ctx)

	// In the case of EndBlock hook, we can extract the tracer from the context
	if tracer := cosmostracing.GetTracingHooks(ctx); tracer != nil && tracer.OnCosmosBlockEnd != nil {
		tracer.OnCosmosBlockEnd(ToCosmosEndBlockEvent(k, ctx), nil)
	}

	return nil
}
