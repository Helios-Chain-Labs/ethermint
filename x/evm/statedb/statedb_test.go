package statedb_test

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdkaddress "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	paramstypes "github.com/cosmos/cosmos-sdk/x/params/types"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	ethtracing "github.com/ethereum/go-ethereum/core/tracing"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/Helios-Chain-Labs/ethermint/testutil/config"
	ethermint "github.com/Helios-Chain-Labs/ethermint/types"
	evmkeeper "github.com/Helios-Chain-Labs/ethermint/x/evm/keeper"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/statedb"
	evmtypes "github.com/Helios-Chain-Labs/ethermint/x/evm/types"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var (
	emptyCodeHash                  = crypto.Keccak256(nil)
	address       common.Address   = common.BigToAddress(big.NewInt(101))
	address2      common.Address   = common.BigToAddress(big.NewInt(102))
	blockHash     common.Hash      = common.BigToHash(big.NewInt(9999))
	emptyTxConfig statedb.TxConfig = statedb.NewEmptyTxConfig(blockHash)
)

type balanceChange struct {
	// We use string to avoid big.Int equality issues
	old    string
	new    string
	reason ethtracing.BalanceChangeReason
}

func balanceChangesValues(changes []balanceChange) string {
	out := make([]string, len(changes))
	for i, change := range changes {
		out[i] = fmt.Sprintf("{%q, %q, ethtracing.BalanceChangeReason(%d)}", change.old, change.new, change.reason)
	}

	return strings.Join(out, "\n")
}

type StateDBTestSuite struct {
	suite.Suite
}

func (suite *StateDBTestSuite) TestTracer_Balance() {
	testCases := []struct {
		name              string
		malleate          func(*statedb.StateDB)
		expBalance        *big.Int
		expBalanceChanges int
	}{
		{
			name: "1 balance change - Add",
			malleate: func(db *statedb.StateDB) {
				db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
			},
			expBalance:        big.NewInt(10),
			expBalanceChanges: 1,
		},
		{
			name: "2 balance changes - Add and Sub",
			malleate: func(db *statedb.StateDB) {
				db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
				// get dirty balance
				suite.Require().Equal(uint256.NewInt(10), db.GetBalance(address))
				db.SubBalance(address, uint256.NewInt(2), tracing.BalanceChangeUnspecified)
			},
			expBalance:        big.NewInt(8),
			expBalanceChanges: 2,
		},
		{
			name: "add zero balance",
			malleate: func(db *statedb.StateDB) {
				db.AddBalance(address, uint256.NewInt(0), tracing.BalanceChangeUnspecified)
			},
			expBalance:        big.NewInt(0),
			expBalanceChanges: 0,
		},
		{
			name: "sub zero balance",
			malleate: func(db *statedb.StateDB) {
				db.SubBalance(address, uint256.NewInt(0), tracing.BalanceChangeUnspecified)
			},
			expBalance:        big.NewInt(0),
			expBalanceChanges: 0,
		},
		{
			name: "transfer",
			malleate: func(db *statedb.StateDB) {
				db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
				db.Transfer(address, address2, big.NewInt(10))
				suite.Require().Equal(uint256.NewInt(10), db.GetBalance(address2))
			},
			expBalance:        big.NewInt(0),
			expBalanceChanges: 3,
		},
		{
			name: "multiple transfers",
			malleate: func(db *statedb.StateDB) {
				db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
				db.AddBalance(address2, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
				db.Transfer(address, address2, big.NewInt(10))
				db.Transfer(address2, address, big.NewInt(5))
				suite.Require().Equal(uint256.NewInt(15), db.GetBalance(address2))
			},
			expBalance:        big.NewInt(5),
			expBalanceChanges: 6,
		},
		{
			name: "set balance",
			malleate: func(db *statedb.StateDB) {
				db.SetBalance(address, uint256.NewInt(10).ToBig())
				suite.Require().Equal(uint256.NewInt(10), db.GetBalance(address))
			},
			expBalance:        big.NewInt(10),
			expBalanceChanges: 1,
		},
		{
			name: "multiple set balance",
			malleate: func(db *statedb.StateDB) {
				db.SetBalance(address, uint256.NewInt(10).ToBig())
				db.SetBalance(address2, uint256.NewInt(10).ToBig())
				suite.Require().Equal(uint256.NewInt(10), db.GetBalance(address))
				suite.Require().Equal(uint256.NewInt(10), db.GetBalance(address2))
			},
			expBalance:        big.NewInt(10),
			expBalanceChanges: 2,
		},
		{
			name: "multiple set balance and some transfers",
			malleate: func(db *statedb.StateDB) {
				db.SetBalance(address, uint256.NewInt(10).ToBig())
				db.SetBalance(address2, uint256.NewInt(10).ToBig())
				db.Transfer(address, address2, big.NewInt(10))
				db.Transfer(address2, address, big.NewInt(5))
				suite.Require().Equal(uint256.NewInt(5), db.GetBalance(address))
				suite.Require().Equal(uint256.NewInt(15), db.GetBalance(address2))
			},
			expBalance:        big.NewInt(5),
			expBalanceChanges: 6,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			var balanceChanges []balanceChange
			raw, ctx, keeper := setupTestEnv(suite.T())
			t, err := evmtypes.NewFirehoseCosmosLiveTracer()
			require.NoError(suite.T(), err)
			t.OnBalanceChange = func(addr common.Address, prev, new *big.Int, reason ethtracing.BalanceChangeReason) {
				balanceChanges = append(balanceChanges, balanceChange{prev.String(), new.String(), reason})
			}
			keeper.SetTracer(t)
			db := statedb.New(ctx, keeper, emptyTxConfig)
			db.SetTracer(t)
			tc.malleate(db)

			// check dirty state
			suite.Require().Equal(uint256.MustFromBig(tc.expBalance), db.GetBalance(address))
			suite.Require().NoError(db.Commit())

			ctx, keeper = newTestKeeper(suite.T(), raw)
			// check committed balance too
			suite.Require().Equal(tc.expBalance, keeper.GetEVMDenomBalance(ctx, address))
			suite.Require().Equal(tc.expBalanceChanges, len(balanceChanges))
		})
	}
}

func (suite *StateDBTestSuite) TestAccount() {
	key1 := common.BigToHash(big.NewInt(1))
	value1 := common.BigToHash(big.NewInt(2))
	key2 := common.BigToHash(big.NewInt(3))
	value2 := common.BigToHash(big.NewInt(4))
	txConfig := emptyTxConfig
	txConfig.TxHash = common.BigToHash(big.NewInt(100))
	testCases := []struct {
		name     string
		malleate func(*statedb.StateDB, storetypes.MultiStore)
	}{
		{"non-exist account", func(db *statedb.StateDB, cms storetypes.MultiStore) {
			suite.Require().Equal(false, db.Exist(address))
			suite.Require().Equal(true, db.Empty(address))
			suite.Require().Equal(uint256.NewInt(0), db.GetBalance(address))
			suite.Require().Equal([]byte(nil), db.GetCode(address))
			suite.Require().Equal(common.Hash{}, db.GetCodeHash(address))
			suite.Require().Equal(uint64(0), db.GetNonce(address))
		}},
		{"empty account", func(db *statedb.StateDB, cms storetypes.MultiStore) {
			db.CreateAccount(address)
			suite.Require().NoError(db.Commit())

			ctx, keeper := newTestKeeper(suite.T(), cms)
			acct := keeper.GetAccount(ctx, address)
			suite.Require().Equal(statedb.NewEmptyAccount(), acct)
			suite.Require().False(acct.IsContract())

			db = statedb.New(ctx, keeper, txConfig)
			suite.Require().Equal(true, db.Exist(address))
			suite.Require().Equal(true, db.Empty(address))
			suite.Require().Equal(uint256.NewInt(0), db.GetBalance(address))
			suite.Require().Equal([]byte(nil), db.GetCode(address))
			suite.Require().Equal(common.BytesToHash(emptyCodeHash), db.GetCodeHash(address))
			suite.Require().Equal(uint64(0), db.GetNonce(address))
		}},
		{"suicide", func(db *statedb.StateDB, cms storetypes.MultiStore) {
			// non-exist account.
			db.SelfDestruct(address)
			suite.Require().False(db.HasSelfDestructed(address))

			// create a contract account
			db.CreateAccount(address)
			db.SetCode(address, []byte("hello world"))
			db.AddBalance(address, uint256.NewInt(100), tracing.BalanceChangeUnspecified)
			db.SetState(address, key1, value1)
			db.SetState(address, key2, value2)
			codeHash := db.GetCodeHash(address)
			suite.Require().NoError(db.Commit())

			ctx, keeper := newTestKeeper(suite.T(), cms)

			suite.Require().NotEmpty(keeper.GetCode(ctx, codeHash))

			// suicide
			db = statedb.New(ctx, keeper, txConfig)
			suite.Require().False(db.HasSelfDestructed(address))
			db.SelfDestruct(address)

			// check dirty state
			suite.Require().True(db.HasSelfDestructed(address))
			// balance is cleared
			suite.Require().Equal(uint256.NewInt(0), db.GetBalance(address))
			// but code and state are still accessible in dirty state
			suite.Require().Equal(value1, db.GetState(address, key1))
			suite.Require().Equal([]byte("hello world"), db.GetCode(address))

			suite.Require().NoError(db.Commit())

			ctx, keeper = newTestKeeper(suite.T(), cms)

			// not accessible from StateDB anymore
			db = statedb.New(ctx, keeper, txConfig)
			suite.Require().False(db.Exist(address))

			// and cleared in keeper too
			suite.Require().Nil(keeper.GetAccount(ctx, address))
			// code is not deleted when contract suicided.
			suite.Require().NotEmpty(keeper.GetCode(ctx, codeHash))
		}},
	}
	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			raw, ctx, keeper := setupTestEnv(suite.T())
			db := statedb.New(ctx, keeper, txConfig)
			tc.malleate(db, raw)
		})
	}
}

func (suite *StateDBTestSuite) TestAccountOverride() {
	_, ctx, keeper := setupTestEnv(suite.T())
	db := statedb.New(ctx, keeper, emptyTxConfig)
	// test balance carry over when overwritten
	amount := uint256.NewInt(1)

	// init an EOA account, account overriden only happens on EOA account.
	db.AddBalance(address, amount, tracing.BalanceChangeUnspecified)
	db.SetNonce(address, 1)

	// override
	db.CreateAccount(address)

	// check balance is not lost
	suite.Require().Equal(amount, db.GetBalance(address))
	// but nonce is reset
	suite.Require().Equal(uint64(0), db.GetNonce(address))
}

func (suite *StateDBTestSuite) TestTracer_Nonce() {
	testCases := []struct {
		name            string
		malleate        func(*statedb.StateDB)
		expNonceChanges int
	}{
		{
			name: "set nonce to 1",
			malleate: func(db *statedb.StateDB) {
				db.SetNonce(address, 1)
			},
			expNonceChanges: 1,
		},
		{
			name: "set nonce to 10",
			malleate: func(db *statedb.StateDB) {
				db.SetNonce(address, 10)
			},
			expNonceChanges: 1,
		},
		{
			name: "multiple set nonces",
			malleate: func(db *statedb.StateDB) {
				db.SetNonce(address, 1)
				db.SetNonce(address, 2)
			},
			expNonceChanges: 2,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			var nonceChanges []uint64
			_, ctx, keeper := setupTestEnv(suite.T())
			t, err := evmtypes.NewFirehoseCosmosLiveTracer()
			require.NoError(suite.T(), err)
			t.OnNonceChange = func(addr common.Address, prev, new uint64) {
				nonceChanges = append(nonceChanges, new)
			}
			db := statedb.New(ctx, keeper, emptyTxConfig)
			db.SetTracer(t)
			tc.malleate(db)

			suite.Require().Equal(tc.expNonceChanges, len(nonceChanges))
		})
	}
}

func (suite *StateDBTestSuite) TestDBError() {
	testCases := []struct {
		name     string
		malleate func(vm.StateDB)
	}{
		{"negative balance", func(db vm.StateDB) {
			db.SubBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
		}},
	}
	for _, tc := range testCases {
		_, ctx, keeper := setupTestEnv(suite.T())
		db := statedb.New(ctx, keeper, emptyTxConfig)
		tc.malleate(db)
		suite.Require().Error(db.Commit())
	}
}

func (suite *StateDBTestSuite) TestBalance() {
	// NOTE: no need to test overflow/underflow, that is guaranteed by evm implementation.
	testCases := []struct {
		name       string
		malleate   func(*statedb.StateDB)
		expBalance *big.Int
	}{
		{"add balance", func(db *statedb.StateDB) {
			db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
		}, big.NewInt(10)},
		{"sub balance", func(db *statedb.StateDB) {
			db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
			// get dirty balance
			suite.Require().Equal(uint256.NewInt(10), db.GetBalance(address))
			db.SubBalance(address, uint256.NewInt(2), tracing.BalanceChangeUnspecified)
		}, big.NewInt(8)},
		{"add zero balance", func(db *statedb.StateDB) {
			db.AddBalance(address, uint256.NewInt(0), tracing.BalanceChangeUnspecified)
		}, big.NewInt(0)},
		{"sub zero balance", func(db *statedb.StateDB) {
			db.SubBalance(address, uint256.NewInt(0), tracing.BalanceChangeUnspecified)
		}, big.NewInt(0)},
		{"transfer", func(db *statedb.StateDB) {
			db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
			db.Transfer(address, address2, big.NewInt(10))
			suite.Require().Equal(uint256.NewInt(10), db.GetBalance(address2))
		}, big.NewInt(0)},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			raw, ctx, keeper := setupTestEnv(suite.T())
			db := statedb.New(ctx, keeper, emptyTxConfig)
			tc.malleate(db)

			// check dirty state
			suite.Require().Equal(uint256.MustFromBig(tc.expBalance), db.GetBalance(address))
			suite.Require().NoError(db.Commit())

			ctx, keeper = newTestKeeper(suite.T(), raw)
			// check committed balance too
			suite.Require().Equal(tc.expBalance, keeper.GetEVMDenomBalance(ctx, address))
		})
	}
}

func (suite *StateDBTestSuite) TestState() {
	key1 := common.BigToHash(big.NewInt(1))
	value1 := common.BigToHash(big.NewInt(1))
	testCases := []struct {
		name      string
		malleate  func(*statedb.StateDB)
		expStates statedb.Storage
	}{
		{"empty state", func(db *statedb.StateDB) {
		}, nil},
		{"set empty value", func(db *statedb.StateDB) {
			db.SetState(address, key1, common.Hash{})
		}, statedb.Storage{}},
		{"noop state change", func(db *statedb.StateDB) {
			db.SetState(address, key1, value1)
			db.SetState(address, key1, common.Hash{})
		}, statedb.Storage{}},
		{"set state", func(db *statedb.StateDB) {
			// check empty initial state
			suite.Require().Equal(common.Hash{}, db.GetState(address, key1))
			suite.Require().Equal(common.Hash{}, db.GetCommittedState(address, key1))

			// set state
			db.SetState(address, key1, value1)
			// query dirty state
			suite.Require().Equal(value1, db.GetState(address, key1))
			// check committed state is still not exist
			suite.Require().Equal(common.Hash{}, db.GetCommittedState(address, key1))

			// set same value again, should be noop
			db.SetState(address, key1, value1)
			suite.Require().Equal(value1, db.GetState(address, key1))
		}, statedb.Storage{
			key1: value1,
		}},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			raw, ctx, keeper := setupTestEnv(suite.T())
			db := statedb.New(ctx, keeper, emptyTxConfig)
			tc.malleate(db)
			suite.Require().NoError(db.Commit())

			// check committed states in keeper
			ctx, keeper = newTestKeeper(suite.T(), raw)
			for k, v := range tc.expStates {
				suite.Require().Equal(v, keeper.GetState(ctx, address, k))
			}

			// check ForEachStorage
			db = statedb.New(ctx, keeper, emptyTxConfig)
			collected := CollectContractStorage(db, address)
			if len(tc.expStates) > 0 {
				suite.Require().Equal(tc.expStates, collected)
			} else {
				suite.Require().Empty(collected)
			}
		})
	}
}

func (suite *StateDBTestSuite) TestCode() {
	code := []byte("hello world")
	codeHash := crypto.Keccak256Hash(code)

	testCases := []struct {
		name        string
		malleate    func(vm.StateDB)
		expCode     []byte
		expCodeHash common.Hash
	}{
		{"non-exist account", func(vm.StateDB) {}, nil, common.Hash{}},
		{"empty account", func(db vm.StateDB) {
			db.CreateAccount(address)
		}, nil, common.BytesToHash(emptyCodeHash)},
		{"set code", func(db vm.StateDB) {
			db.SetCode(address, code)
		}, code, codeHash},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			raw, ctx, keeper := setupTestEnv(suite.T())
			db := statedb.New(ctx, keeper, emptyTxConfig)
			tc.malleate(db)

			// check dirty state
			suite.Require().Equal(tc.expCode, db.GetCode(address))
			suite.Require().Equal(len(tc.expCode), db.GetCodeSize(address))
			suite.Require().Equal(tc.expCodeHash, db.GetCodeHash(address))

			suite.Require().NoError(db.Commit())

			// check the committed state
			ctx, keeper = newTestKeeper(suite.T(), raw)
			db = statedb.New(ctx, keeper, emptyTxConfig)
			suite.Require().Equal(tc.expCode, db.GetCode(address))
			suite.Require().Equal(len(tc.expCode), db.GetCodeSize(address))
			suite.Require().Equal(tc.expCodeHash, db.GetCodeHash(address))
		})
	}
}

type codeChange struct {
	addr    string
	oldCode string
	newCode string
}

func newCodeChange(addr, oldCode, newCode string) codeChange {
	return codeChange{
		addr:    addr,
		oldCode: oldCode,
		newCode: newCode,
	}
}

func (suite *StateDBTestSuite) TestTracer_Code() {
	code := []byte("hello world")
	code2 := []byte("hello world 2")
	codeHash := crypto.Keccak256Hash(code)
	code2Hash := crypto.Keccak256Hash(code2)

	testCases := []struct {
		name           string
		malleate       func(vm.StateDB)
		expCode        []byte
		expCodeHash    common.Hash
		expCodeChanges int
	}{
		{
			name:           "non-exist account",
			malleate:       func(vm.StateDB) {},
			expCode:        nil,
			expCodeHash:    common.Hash{},
			expCodeChanges: 0,
		},
		{
			name: "empty account",
			malleate: func(db vm.StateDB) {
				db.CreateAccount(address)
			},
			expCode:        nil,
			expCodeHash:    common.BytesToHash(emptyCodeHash),
			expCodeChanges: 0,
		},
		{
			name: "set code",
			malleate: func(db vm.StateDB) {
				db.SetCode(address, code)
			},
			expCode:        code,
			expCodeHash:    codeHash,
			expCodeChanges: 1,
		},
		{
			name: "set multiple code",
			malleate: func(db vm.StateDB) {

				db.SetCode(address, code)
				db.SetCode(address, code2)
			},
			expCode:        code2,
			expCodeHash:    code2Hash,
			expCodeChanges: 2,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			var codeChanges []codeChange
			raw, ctx, keeper := setupTestEnv(suite.T())
			db := statedb.New(ctx, keeper, emptyTxConfig)
			t, err := evmtypes.NewFirehoseCosmosLiveTracer()

			t.OnCodeChange = func(addr common.Address, prevCodeHash common.Hash, prevCode []byte, codeHash common.Hash, code []byte) {
				codeChanges = append(codeChanges, newCodeChange(addr.String(), string(prevCode), string(code)))
			}

			require.NoError(suite.T(), err)
			db.SetTracer(t)
			tc.malleate(db)

			// check dirty state
			suite.Require().Equal(tc.expCode, db.GetCode(address))
			suite.Require().Equal(len(tc.expCode), db.GetCodeSize(address))
			suite.Require().Equal(tc.expCodeHash, db.GetCodeHash(address))

			suite.Require().NoError(db.Commit())

			// check the committed state
			ctx, keeper = newTestKeeper(suite.T(), raw)
			db = statedb.New(ctx, keeper, emptyTxConfig)
			suite.Require().Equal(tc.expCode, db.GetCode(address))
			suite.Require().Equal(len(tc.expCode), db.GetCodeSize(address))
			suite.Require().Equal(tc.expCodeHash, db.GetCodeHash(address))

			// check code changes
			suite.Require().Equal(tc.expCodeChanges, len(codeChanges))
		})
	}
}

func (suite *StateDBTestSuite) TestRevertSnapshot() {
	v1 := common.BigToHash(big.NewInt(1))
	v2 := common.BigToHash(big.NewInt(2))
	v3 := common.BigToHash(big.NewInt(3))
	testCases := []struct {
		name     string
		malleate func(vm.StateDB)
	}{
		{"set state", func(db vm.StateDB) {
			db.SetState(address, v1, v3)
		}},
		{"set nonce", func(db vm.StateDB) {
			db.SetNonce(address, 10)
		}},
		{"change balance", func(db vm.StateDB) {
			db.AddBalance(address, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
			db.SubBalance(address, uint256.NewInt(5), tracing.BalanceChangeUnspecified)
		}},
		{"override account", func(db vm.StateDB) {
			db.CreateAccount(address)
		}},
		{"set code", func(db vm.StateDB) {
			db.SetCode(address, []byte("hello world"))
		}},
		{"suicide", func(db vm.StateDB) {
			db.SetState(address, v1, v2)
			db.SetCode(address, []byte("hello world"))
			db.SelfDestruct(address)
		}},
		{"add log", func(db vm.StateDB) {
			db.AddLog(&ethtypes.Log{
				Address: address,
			})
		}},
		{"add refund", func(db vm.StateDB) {
			db.AddRefund(10)
			db.SubRefund(5)
		}},
		{"access list", func(db vm.StateDB) {
			db.AddAddressToAccessList(address)
			db.AddSlotToAccessList(address, v1)
		}},
	}
	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			raw, ctx, keeper := setupTestEnv(suite.T())
			ctx = ctx.WithEventManager(sdk.NewEventManager())

			{
				// do some arbitrary changes to the storage
				db := statedb.New(ctx, keeper, emptyTxConfig)
				db.SetNonce(address, 1)
				db.AddBalance(address, uint256.NewInt(100), tracing.BalanceChangeUnspecified)
				db.SetCode(address, []byte("hello world"))
				db.SetState(address, v1, v2)
				db.SetNonce(address2, 1)
				suite.Require().NoError(db.Commit())
			}

			originalState := cloneRawState(suite.T(), raw)
			ctx, keeper = newTestKeeper(suite.T(), raw)

			// run test
			db := statedb.New(ctx, keeper, emptyTxConfig)
			rev := db.Snapshot()
			tc.malleate(db)
			db.RevertToSnapshot(rev)

			// check empty states after revert
			suite.Require().Zero(db.GetRefund())
			suite.Require().Empty(db.Logs())

			suite.Require().NoError(db.Commit())

			newState := cloneRawState(suite.T(), raw)
			// check the committed state stays the same
			suite.Require().Equal(originalState, newState)
		})
	}
}

func (suite *StateDBTestSuite) TestNestedSnapshot() {
	key := common.BigToHash(big.NewInt(1))
	value1 := common.BigToHash(big.NewInt(1))
	value2 := common.BigToHash(big.NewInt(2))

	_, ctx, keeper := setupTestEnv(suite.T())
	db := statedb.New(ctx, keeper, emptyTxConfig)

	rev1 := db.Snapshot()
	db.SetState(address, key, value1)

	rev2 := db.Snapshot()
	db.SetState(address, key, value2)
	suite.Require().Equal(value2, db.GetState(address, key))

	db.RevertToSnapshot(rev2)
	suite.Require().Equal(value1, db.GetState(address, key))

	db.RevertToSnapshot(rev1)
	suite.Require().Equal(common.Hash{}, db.GetState(address, key))
}

func (suite *StateDBTestSuite) TestInvalidSnapshotId() {
	_, ctx, keeper := setupTestEnv(suite.T())
	db := statedb.New(ctx, keeper, emptyTxConfig)
	suite.Require().Panics(func() {
		db.RevertToSnapshot(1)
	})
}

func (suite *StateDBTestSuite) TestAccessList() {
	value1 := common.BigToHash(big.NewInt(1))
	value2 := common.BigToHash(big.NewInt(2))

	testCases := []struct {
		name     string
		malleate func(vm.StateDB)
	}{
		{"add address", func(db vm.StateDB) {
			suite.Require().False(db.AddressInAccessList(address))
			db.AddAddressToAccessList(address)
			suite.Require().True(db.AddressInAccessList(address))

			addrPresent, slotPresent := db.SlotInAccessList(address, value1)
			suite.Require().True(addrPresent)
			suite.Require().False(slotPresent)

			// add again, should be no-op
			db.AddAddressToAccessList(address)
			suite.Require().True(db.AddressInAccessList(address))
		}},
		{"add slot", func(db vm.StateDB) {
			addrPresent, slotPresent := db.SlotInAccessList(address, value1)
			suite.Require().False(addrPresent)
			suite.Require().False(slotPresent)
			db.AddSlotToAccessList(address, value1)
			addrPresent, slotPresent = db.SlotInAccessList(address, value1)
			suite.Require().True(addrPresent)
			suite.Require().True(slotPresent)

			// add another slot
			db.AddSlotToAccessList(address, value2)
			addrPresent, slotPresent = db.SlotInAccessList(address, value2)
			suite.Require().True(addrPresent)
			suite.Require().True(slotPresent)

			// add again, should be noop
			db.AddSlotToAccessList(address, value2)
			addrPresent, slotPresent = db.SlotInAccessList(address, value2)
			suite.Require().True(addrPresent)
			suite.Require().True(slotPresent)
		}},
	}

	for _, tc := range testCases {
		_, ctx, keeper := setupTestEnv(suite.T())
		db := statedb.New(ctx, keeper, emptyTxConfig)
		tc.malleate(db)
	}
}

func (suite *StateDBTestSuite) TestLog() {
	txHash := common.BytesToHash([]byte("tx"))
	// use a non-default tx config
	txConfig := statedb.NewTxConfig(
		blockHash,
		txHash,
		1, 1,
	)
	_, ctx, keeper := setupTestEnv(suite.T())
	db := statedb.New(ctx, keeper, txConfig)
	data := []byte("hello world")
	db.AddLog(&ethtypes.Log{
		Address:     address,
		Topics:      []common.Hash{},
		Data:        data,
		BlockNumber: 1,
	})
	suite.Require().Equal(1, len(db.Logs()))
	expecedLog := &ethtypes.Log{
		Address:     address,
		Topics:      []common.Hash{},
		Data:        data,
		BlockNumber: 1,
		TxIndex:     1,
		Index:       1,
	}
	suite.Require().Equal(expecedLog, db.Logs()[0])

	db.AddLog(&ethtypes.Log{
		Address:     address,
		Topics:      []common.Hash{},
		Data:        data,
		BlockNumber: 1,
	})
	suite.Require().Equal(2, len(db.Logs()))
	expecedLog.Index++
	suite.Require().Equal(expecedLog, db.Logs()[1])
}

func (suite *StateDBTestSuite) TestRefund() {
	testCases := []struct {
		name      string
		malleate  func(vm.StateDB)
		expRefund uint64
		expPanic  bool
	}{
		{"add refund", func(db vm.StateDB) {
			db.AddRefund(uint64(10))
		}, 10, false},
		{"sub refund", func(db vm.StateDB) {
			db.AddRefund(uint64(10))
			db.SubRefund(uint64(5))
		}, 5, false},
		{"negative refund counter", func(db vm.StateDB) {
			db.AddRefund(uint64(5))
			db.SubRefund(uint64(10))
		}, 0, true},
	}
	for _, tc := range testCases {
		_, ctx, keeper := setupTestEnv(suite.T())
		db := statedb.New(ctx, keeper, emptyTxConfig)
		if !tc.expPanic {
			tc.malleate(db)
			suite.Require().Equal(tc.expRefund, db.GetRefund())
		} else {
			suite.Require().Panics(func() {
				tc.malleate(db)
			})
		}
	}
}

func (suite *StateDBTestSuite) TestIterateStorage() {
	key1 := common.BigToHash(big.NewInt(1))
	value1 := common.BigToHash(big.NewInt(2))
	key2 := common.BigToHash(big.NewInt(3))
	value2 := common.BigToHash(big.NewInt(4))

	raw, ctx, keeper := setupTestEnv(suite.T())
	db := statedb.New(ctx, keeper, emptyTxConfig)
	db.SetState(address, key1, value1)
	db.SetState(address, key2, value2)

	// ForEachStorage only iterate committed state
	suite.Require().Empty(CollectContractStorage(db, address))

	suite.Require().NoError(db.Commit())

	storage := CollectContractStorage(db, address)
	suite.Require().Equal(2, len(storage))

	ctx, keeper = newTestKeeper(suite.T(), raw)
	for k, v := range storage {
		suite.Require().Equal(v, keeper.GetState(ctx, address, k))
	}

	// break early iteration
	storage = make(statedb.Storage)
	db.ForEachStorage(address, func(k, v common.Hash) bool {
		storage[k] = v
		// return false to break early
		return false
	})
	suite.Require().Equal(1, len(storage))
}

func (suite *StateDBTestSuite) TestNativeAction() {
	_, ctx, keeper := setupTestEnv(suite.T())
	storeKey := testStoreKeys["testnative"]
	objStoreKey := testObjKeys[evmtypes.ObjectStoreKey]
	memKey := testMemKeys[capabilitytypes.MemStoreKey]

	eventConverter := func(event sdk.Event) (*ethtypes.Log, error) {
		converters := map[string]statedb.EventConverter{
			"success1": func(event sdk.Event) (*ethtypes.Log, error) {
				return &ethtypes.Log{Data: []byte("success1")}, nil
			},
			"success2": func(event sdk.Event) (*ethtypes.Log, error) {
				return &ethtypes.Log{Data: []byte("success2")}, nil
			},
		}
		converter, ok := converters[event.Type]
		if !ok {
			return nil, nil
		}
		return converter(event)
	}

	stateDB := statedb.New(ctx, keeper, emptyTxConfig)
	contract := common.BigToAddress(big.NewInt(101))

	stateDB.ExecuteNativeAction(contract, eventConverter, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		store.Set([]byte("success1"), []byte("value"))
		ctx.EventManager().EmitEvent(sdk.NewEvent("success1"))

		objStore := ctx.ObjectStore(objStoreKey)
		objStore.Set([]byte("transient"), "value")

		mem := ctx.KVStore(memKey)
		mem.Set([]byte("mem"), []byte("value"))

		return nil
	})
	stateDB.ExecuteNativeAction(contract, eventConverter, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		store.Set([]byte("failure1"), []byte("value"))
		ctx.EventManager().EmitEvent(sdk.NewEvent("failure1"))

		objStore := ctx.ObjectStore(objStoreKey)
		suite.Require().Equal("value", objStore.Get([]byte("transient")).(string))

		mem := ctx.KVStore(memKey)
		suite.Require().Equal([]byte("value"), mem.Get([]byte("mem")))
		return errors.New("failure")
	})

	// check events
	suite.Require().Equal(sdk.Events{{Type: "success1"}}, stateDB.NativeEvents())
	suite.Require().Equal([]*ethtypes.Log{{
		Address: contract,
		Data:    []byte("success1"),
	}}, stateDB.Logs())

	// test query
	stateDB.ExecuteNativeAction(contract, nil, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		suite.Require().Equal([]byte("value"), store.Get([]byte("success1")))
		suite.Require().Nil(store.Get([]byte("failure1")))
		return nil
	})

	rev1 := stateDB.Snapshot()
	stateDB.ExecuteNativeAction(contract, eventConverter, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		store.Set([]byte("success2"), []byte("value"))
		ctx.EventManager().EmitEvent(sdk.NewEvent("success2"))
		return nil
	})
	stateDB.ExecuteNativeAction(contract, eventConverter, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		store.Set([]byte("failure2"), []byte("value"))
		ctx.EventManager().EmitEvent(sdk.NewEvent("failure2"))
		return errors.New("failure")
	})

	// check events
	suite.Require().Equal(sdk.Events{{Type: "success1"}, {Type: "success2"}}, stateDB.NativeEvents())
	suite.Require().Equal([]*ethtypes.Log{{
		Address: contract,
		Data:    []byte("success1"),
	}, {
		Index:   1,
		Address: contract,
		Data:    []byte("success2"),
	}}, stateDB.Logs())
	// test query
	stateDB.ExecuteNativeAction(contract, nil, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		suite.Require().Equal([]byte("value"), store.Get([]byte("success1")))
		suite.Require().Equal([]byte("value"), store.Get([]byte("success2")))
		suite.Require().Nil(store.Get([]byte("failure2")))
		return nil
	})

	stateDB.RevertToSnapshot(rev1)

	// check events
	suite.Require().Equal(sdk.Events{{Type: "success1"}}, stateDB.NativeEvents())
	suite.Require().Equal([]*ethtypes.Log{{
		Address: contract,
		Data:    []byte("success1"),
	}}, stateDB.Logs())

	_ = stateDB.Snapshot()
	stateDB.ExecuteNativeAction(contract, eventConverter, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		store.Set([]byte("success3"), []byte("value"))
		ctx.EventManager().EmitEvent(sdk.NewEvent("success3"))
		return nil
	})

	// check events
	suite.Require().Equal(sdk.Events{{Type: "success1"}, {Type: "success3"}}, stateDB.NativeEvents())

	// test query
	stateDB.ExecuteNativeAction(contract, eventConverter, func(ctx sdk.Context) error {
		store := ctx.KVStore(storeKey)
		suite.Require().Equal([]byte("value"), store.Get([]byte("success1")))
		suite.Require().Nil(store.Get([]byte("success2")))
		suite.Require().Equal([]byte("value"), store.Get([]byte("success3")))
		return nil
	})

	suite.Require().NoError(stateDB.Commit())

	// query committed state
	store := ctx.KVStore(storeKey)
	suite.Require().Equal([]byte("value"), store.Get([]byte("success1")))
	suite.Require().Nil(store.Get([]byte("success2")))
	suite.Require().Equal([]byte("value"), store.Get([]byte("success3")))
	suite.Require().Nil(store.Get([]byte("failure1")))
	suite.Require().Nil(store.Get([]byte("failure2")))

	// check events
	suite.Require().Equal(sdk.Events{{Type: "success1"}, {Type: "success3"}}, ctx.EventManager().Events())
}

func (suite *StateDBTestSuite) TestSetStorage() {
	contract := common.BigToAddress(big.NewInt(101))

	_, ctx, keeper := setupTestEnv(suite.T())
	stateDB := statedb.New(ctx, keeper, emptyTxConfig)

	stateDB.SetState(contract, common.BigToHash(big.NewInt(0)), common.BigToHash(big.NewInt(0)))
	stateDB.SetState(contract, common.BigToHash(big.NewInt(1)), common.BigToHash(big.NewInt(1)))
	stateDB.SetState(contract, common.BigToHash(big.NewInt(2)), common.BigToHash(big.NewInt(2)))
	suite.Require().NoError(stateDB.Commit())

	suite.Require().Equal(common.BigToHash(big.NewInt(0)), stateDB.GetState(contract, common.BigToHash(big.NewInt(0))))
	suite.Require().Equal(common.BigToHash(big.NewInt(1)), stateDB.GetState(contract, common.BigToHash(big.NewInt(1))))
	suite.Require().Equal(common.BigToHash(big.NewInt(2)), stateDB.GetState(contract, common.BigToHash(big.NewInt(2))))

	stateDB.SetStorage(contract, map[common.Hash]common.Hash{
		common.BigToHash(big.NewInt(1)): common.BigToHash(big.NewInt(3)),
	})

	suite.Require().Equal(common.Hash{}, stateDB.GetState(contract, common.BigToHash(big.NewInt(0))))
	suite.Require().Equal(common.BigToHash(big.NewInt(3)), stateDB.GetState(contract, common.BigToHash(big.NewInt(1))))
	suite.Require().Equal(common.Hash{}, stateDB.GetState(contract, common.BigToHash(big.NewInt(2))))
}

type storageChanges struct {
	address string
	key     string
	old     string
	new     string
}

func newStorageChange(addr, key, old, new string) storageChanges {
	return storageChanges{
		address: addr,
		key:     key,
		old:     old,
		new:     new,
	}
}

func (suite *StateDBTestSuite) TestTracer_SetStorage() {
	contract := common.BigToAddress(big.NewInt(101))

	var sChanges []storageChanges

	_, ctx, keeper := setupTestEnv(suite.T())
	t, err := evmtypes.NewFirehoseCosmosLiveTracer()
	require.NoError(suite.T(), err)
	t.OnStorageChange = func(addr common.Address, slot common.Hash, prev, new common.Hash) {
		sChanges = append(sChanges, newStorageChange(addr.String(), slot.String(), prev.String(), new.String()))
	}
	stateDB := statedb.New(ctx, keeper, emptyTxConfig)
	stateDB.SetTracer(t)

	stateDB.SetState(contract, common.BigToHash(big.NewInt(0)), common.BigToHash(big.NewInt(0)))
	stateDB.SetState(contract, common.BigToHash(big.NewInt(1)), common.BigToHash(big.NewInt(1)))
	stateDB.SetState(contract, common.BigToHash(big.NewInt(2)), common.BigToHash(big.NewInt(2)))
	suite.Require().NoError(stateDB.Commit())
	suite.Require().Equal(3, len(sChanges))

	suite.Require().Equal(common.BigToHash(big.NewInt(0)), stateDB.GetState(contract, common.BigToHash(big.NewInt(0))))
	suite.Require().Equal(common.BigToHash(big.NewInt(1)), stateDB.GetState(contract, common.BigToHash(big.NewInt(1))))
	suite.Require().Equal(common.BigToHash(big.NewInt(2)), stateDB.GetState(contract, common.BigToHash(big.NewInt(2))))

	// single change to the storage
	stateDB.SetStorage(contract, map[common.Hash]common.Hash{
		common.BigToHash(big.NewInt(1)): common.BigToHash(big.NewInt(3)),
	})
	suite.Require().Equal(4, len(sChanges))

	suite.Require().Equal(common.Hash{}, stateDB.GetState(contract, common.BigToHash(big.NewInt(0))))
	suite.Require().Equal(common.BigToHash(big.NewInt(3)), stateDB.GetState(contract, common.BigToHash(big.NewInt(1))))
	suite.Require().Equal(common.Hash{}, stateDB.GetState(contract, common.BigToHash(big.NewInt(2))))

	// multiple changes to the storage
	stateDB.SetStorage(contract, map[common.Hash]common.Hash{
		common.BigToHash(big.NewInt(2)): common.BigToHash(big.NewInt(3)),
		common.BigToHash(big.NewInt(4)): common.BigToHash(big.NewInt(5)),
		common.BigToHash(big.NewInt(6)): common.BigToHash(big.NewInt(7)),
		common.BigToHash(big.NewInt(8)): common.BigToHash(big.NewInt(9)),
	})
	suite.Require().Equal(8, len(sChanges))
}

type StateDBWithForEachStorage interface {
	ForEachStorage(common.Address, func(common.Hash, common.Hash) bool) error
}

func CollectContractStorage(db vm.StateDB, address common.Address) statedb.Storage {
	storage := make(statedb.Storage)
	if d, ok := db.(StateDBWithForEachStorage); ok {
		d.ForEachStorage(address, func(k, v common.Hash) bool {
			storage[k] = v
			return true
		})
	}
	return storage
}

var (
	testStoreKeys          = storetypes.NewKVStoreKeys(authtypes.StoreKey, banktypes.StoreKey, evmtypes.StoreKey, "testnative")
	testTransientStoreKeys = storetypes.NewTransientStoreKeys(banktypes.TStoreKey)
	testObjKeys            = storetypes.NewObjectStoreKeys(banktypes.ObjectStoreKey, evmtypes.ObjectStoreKey)
	testMemKeys            = storetypes.NewMemoryStoreKeys(capabilitytypes.MemStoreKey)
)

func cloneRawState(t *testing.T, cms storetypes.MultiStore) map[string]map[string][]byte {
	result := make(map[string]map[string][]byte)

	for name, key := range testStoreKeys {
		store := cms.GetKVStore(key)
		itr := store.Iterator(nil, nil)
		defer itr.Close()

		state := make(map[string][]byte)
		for ; itr.Valid(); itr.Next() {
			state[string(itr.Key())] = itr.Value()
		}

		result[name] = state
	}

	return result
}

func newTestKeeper(t *testing.T, cms storetypes.MultiStore) (sdk.Context, *evmkeeper.Keeper) {
	appCodec := config.MakeConfigForTest(nil).Codec
	authAddr := authtypes.NewModuleAddress(govtypes.ModuleName).String()
	accountKeeper := authkeeper.NewAccountKeeper(
		appCodec,
		runtime.NewKVStoreService(testStoreKeys[authtypes.StoreKey]),
		ethermint.ProtoAccount,
		map[string][]string{
			evmtypes.ModuleName: {authtypes.Minter, authtypes.Burner},
		},
		sdkaddress.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		sdk.GetConfig().GetBech32AccountAddrPrefix(),
		authAddr,
	)
	bankKeeper := bankkeeper.NewBaseKeeper(
		appCodec,
		runtime.NewKVStoreService(testStoreKeys[banktypes.StoreKey]),
		runtime.NewTransientKVStoreService(testTransientStoreKeys[banktypes.TStoreKey]),
		testObjKeys[banktypes.ObjectStoreKey],
		accountKeeper,
		map[string]bool{},
		authAddr,
		log.NewNopLogger(),
	)
	evmKeeper := evmkeeper.NewKeeper(
		appCodec,
		testStoreKeys[evmtypes.StoreKey], testObjKeys[evmtypes.ObjectStoreKey], authtypes.NewModuleAddress(govtypes.ModuleName),
		accountKeeper, bankKeeper, nil, nil,
		paramstypes.Subspace{},
		nil,
	)

	ctx := sdk.NewContext(cms, tmproto.Header{}, false, log.NewNopLogger())
	return ctx, evmKeeper
}

func setupTestEnv(t *testing.T) (storetypes.MultiStore, sdk.Context, *evmkeeper.Keeper) {
	db := dbm.NewMemDB()
	cms := rootmulti.NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	for _, key := range testStoreKeys {
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
	}
	for _, key := range testMemKeys {
		cms.MountStoreWithDB(key, storetypes.StoreTypeMemory, nil)
	}
	for _, key := range testObjKeys {
		cms.MountStoreWithDB(key, storetypes.StoreTypeObject, nil)
	}
	for _, key := range testTransientStoreKeys {
		cms.MountStoreWithDB(key, storetypes.StoreTypeTransient, nil)
	}
	require.NoError(t, cms.LoadLatestVersion())

	ctx, keeper := newTestKeeper(t, cms)
	require.NoError(t, keeper.SetParams(ctx, evmtypes.Params{
		EvmDenom: evmtypes.DefaultEVMDenom,
	}))
	return cms, ctx, keeper
}

func TestStateDBTestSuite(t *testing.T) {
	suite.Run(t, &StateDBTestSuite{})
}
