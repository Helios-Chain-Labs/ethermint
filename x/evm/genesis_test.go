package evm_test

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/Helios-Chain-Labs/ethermint/crypto/ethsecp256k1"
	"github.com/Helios-Chain-Labs/ethermint/testutil"
	ethermint "github.com/Helios-Chain-Labs/ethermint/types"
	"github.com/Helios-Chain-Labs/ethermint/x/evm"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/statedb"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/types"
	"github.com/stretchr/testify/suite"
)

type GenesisTestSuite struct {
	testutil.BaseTestSuite
}

func TestGenesisTestSuite(t *testing.T) {
	suite.Run(t, new(GenesisTestSuite))
}

func (suite *GenesisTestSuite) TestInitGenesis() {
	privkey, err := ethsecp256k1.GenerateKey()
	suite.Require().NoError(err)

	address := common.HexToAddress(privkey.PubKey().Address().String())

	var vmdb *statedb.StateDB

	testCases := []struct {
		name     string
		malleate func()
		genState *types.GenesisState
		expPanic bool
	}{
		{
			"default",
			func() {},
			types.DefaultGenesisState(),
			false,
		},
		{
			"valid account",
			func() {
				vmdb.AddBalance(address, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
			},
			&types.GenesisState{
				Params: types.DefaultParams(),
				Accounts: []types.GenesisAccount{
					{
						Address: address.String(),
						Storage: types.Storage{
							{Key: common.BytesToHash([]byte("key")).String(), Value: common.BytesToHash([]byte("value")).String()},
						},
					},
				},
			},
			false,
		},
		{
			"account not found",
			func() {},
			&types.GenesisState{
				Params: types.DefaultParams(),
				Accounts: []types.GenesisAccount{
					{
						Address: address.String(),
					},
				},
			},
			true,
		},
		{
			"invalid account type",
			func() {
				acc := authtypes.NewBaseAccountWithAddress(address.Bytes())
				acc.AccountNumber = suite.App.AccountKeeper.NextAccountNumber(suite.Ctx)
				suite.App.AccountKeeper.SetAccount(suite.Ctx, acc)
			},
			&types.GenesisState{
				Params: types.DefaultParams(),
				Accounts: []types.GenesisAccount{
					{
						Address: address.String(),
					},
				},
			},
			true,
		},
		{
			"invalid code hash",
			func() {
				acc := suite.App.AccountKeeper.NewAccountWithAddress(suite.Ctx, address.Bytes())
				suite.App.AccountKeeper.SetAccount(suite.Ctx, acc)
			},
			&types.GenesisState{
				Params: types.DefaultParams(),
				Accounts: []types.GenesisAccount{
					{
						Address: address.String(),
						Code:    "ffffffff",
					},
				},
			},
			true,
		},
		{
			"ignore empty account code checking",
			func() {
				acc := suite.App.AccountKeeper.NewAccountWithAddress(suite.Ctx, address.Bytes())

				suite.App.AccountKeeper.SetAccount(suite.Ctx, acc)
			},
			&types.GenesisState{
				Params: types.DefaultParams(),
				Accounts: []types.GenesisAccount{
					{
						Address: address.String(),
						Code:    "",
					},
				},
			},
			false,
		},
		{
			"ignore empty account code checking with non-empty codehash",
			func() {
				ethAcc := &ethermint.EthAccount{
					BaseAccount: authtypes.NewBaseAccount(address.Bytes(), nil, suite.App.AccountKeeper.NextAccountNumber(suite.Ctx), 0),
					CodeHash:    common.BytesToHash([]byte{1, 2, 3}).Hex(),
				}

				suite.App.AccountKeeper.SetAccount(suite.Ctx, ethAcc)
			},
			&types.GenesisState{
				Params: types.DefaultParams(),
				Accounts: []types.GenesisAccount{
					{
						Address: address.String(),
						Code:    "",
					},
				},
			},
			false,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.SetupTest()
			vmdb = suite.StateDB()

			tc.malleate()
			vmdb.Commit()

			if tc.expPanic {
				suite.Require().Panics(
					func() {
						_ = evm.InitGenesis(suite.Ctx, suite.App.EvmKeeper, suite.App.AccountKeeper, *tc.genState)
					},
				)
			} else {
				suite.Require().NotPanics(
					func() {
						_ = evm.InitGenesis(suite.Ctx, suite.App.EvmKeeper, suite.App.AccountKeeper, *tc.genState)
					},
				)
			}
		})
	}
}
