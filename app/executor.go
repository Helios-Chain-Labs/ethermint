package app

import (
	"context"

	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	evmtypes "github.com/Helios-Chain-Labs/ethermint/x/evm/types"
)

func DefaultTxExecutor(_ context.Context,
	blockSize int,
	ms storetypes.MultiStore,
	deliverTxWithMultiStore func(int, storetypes.MultiStore) *abci.ExecTxResult,
) ([]*abci.ExecTxResult, error) {
	results := make([]*abci.ExecTxResult, blockSize)
	for i := 0; i < blockSize; i++ {
		results[i] = deliverTxWithMultiStore(i, ms)
	}
	return evmtypes.PatchTxResponses(results), nil
}

// func STMTxExecutor(stores []storetypes.StoreKey, workers int) baseapp.TxExecutor {
// 	index := make(map[storetypes.StoreKey]int, len(stores))
// 	for i, k := range stores {
// 		index[k] = i
// 	}
// 	return func(
// 		ctx context.Context,
// 		blockSize int,
// 		ms storetypes.MultiStore,
// 		deliverTxWithMultiStore func(int, storetypes.MultiStore) *abci.ExecTxResult,
// 	) ([]*abci.ExecTxResult, error) {
// 		if blockSize == 0 {
// 			return nil, nil
// 		}
// 		results := make([]*abci.ExecTxResult, blockSize)
// 		if err := blockstm.ExecuteBlock(
// 			ctx,
// 			blockSize,
// 			index,
// 			stmMultiStoreWrapper{ms},
// 			workers,
// 			func(txn blockstm.TxnIndex, ms blockstm.MultiStore) {
// 				result := deliverTxWithMultiStore(int(txn), msWrapper{ms})
// 				results[txn] = result
// 			},
// 		); err != nil {
// 			return nil, err
// 		}

// 		return evmtypes.PatchTxResponses(results), nil
// 	}
// }

// type msWrapper struct {
// 	blockstm.MultiStore
// }

// var _ storetypes.MultiStore = msWrapper{}

// func (ms msWrapper) getCacheWrapper(key storetypes.StoreKey) storetypes.CacheWrapper {
// 	return ms.GetStore(key)
// }

// func (ms msWrapper) GetStore(key storetypes.StoreKey) storetypes.Store {
// 	return ms.MultiStore.GetStore(key)
// }

// func (ms msWrapper) GetKVStore(key storetypes.StoreKey) storetypes.KVStore {
// 	return ms.MultiStore.GetKVStore(key)
// }

// func (ms msWrapper) GetObjKVStore(key storetypes.StoreKey) storetypes.ObjKVStore {
// 	return ms.MultiStore.GetObjKVStore(key)
// }

// func (ms msWrapper) CacheMultiStore() storetypes.CacheMultiStore {
// 	return cachemulti.NewFromParent(ms.getCacheWrapper, nil, nil)
// }

// func (ms msWrapper) CacheMultiStoreWithVersion(version int64) (storetypes.CacheMultiStore, error) {
// 	return cachemulti.NewFromParent(ms.getCacheWrapper, nil, nil), nil
// }

// // Implements CacheWrapper.
// func (ms msWrapper) CacheWrap() storetypes.CacheWrap {
// 	return ms.CacheMultiStore().(storetypes.CacheWrap)
// }

// // Implements CacheWrapper.
// func (ms msWrapper) CacheWrapWithTrace(w io.Writer, tc types.TraceContext) types.CacheWrap {
// 	return ms.CacheMultiStore().(storetypes.CacheWrap)
// }

// // LatestVersion returns the branch version of the store
// func (ms msWrapper) LatestVersion() int64 {
// 	return ms.CacheMultiStore().LatestVersion()
// }

// // GetStoreType returns the type of the store.
// func (ms msWrapper) GetStoreType() storetypes.StoreType {
// 	return storetypes.StoreTypeMulti
// }

// // Implements interface MultiStore
// func (ms msWrapper) SetTracer(io.Writer) storetypes.MultiStore {
// 	return nil
// }

// // Implements interface MultiStore
// func (ms msWrapper) SetTracingContext(storetypes.TraceContext) storetypes.MultiStore {
// 	return nil
// }

// // Implements interface MultiStore
// func (ms msWrapper) TracingEnabled() bool {
// 	return false
// }

// type stmMultiStoreWrapper struct {
// 	storetypes.MultiStore
// }

// var _ blockstm.MultiStore = stmMultiStoreWrapper{}

// func (ms stmMultiStoreWrapper) GetStore(key storetypes.StoreKey) storetypes.Store {
// 	return ms.MultiStore.GetStore(key)
// }

// func (ms stmMultiStoreWrapper) GetKVStore(key storetypes.StoreKey) storetypes.KVStore {
// 	return ms.MultiStore.GetKVStore(key)
// }

// func (ms stmMultiStoreWrapper) GetObjKVStore(key storetypes.StoreKey) storetypes.ObjKVStore {
// 	return ms.MultiStore.GetObjKVStore(key)
// }
