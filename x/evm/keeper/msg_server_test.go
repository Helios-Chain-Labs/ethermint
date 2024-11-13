package keeper_test

import (
	"errors"
	"math/big"
	"testing"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/stretchr/testify/suite"

	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/Helios-Chain-Labs/ethermint/testutil"
	utiltx "github.com/Helios-Chain-Labs/ethermint/testutil/tx"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/statedb"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/types"
)

type MsgServerTestSuite struct {
	testutil.BaseTestSuiteWithAccount
}

func TestMsgServerTestSuite(t *testing.T) {
	suite.Run(t, new(MsgServerTestSuite))
}

func (suite *MsgServerTestSuite) TestEthereumTx() {
	var (
		err             error
		msg             *types.MsgEthereumTx
		signer          ethtypes.Signer
		vmdb            *statedb.StateDB
		expectedGasUsed uint64
	)

	testCases := []struct {
		name     string
		malleate func()
		expErr   error
	}{
		{
			"Deploy contract tx - insufficient gas",
			func() {
				msg, err = utiltx.CreateUnderpricedContractMsgTx(
					vmdb.GetNonce(suite.Address),
					signer,
					big.NewInt(1),
					suite.Address,
					suite.Signer,
				)
				suite.Require().NoError(err)
			},
			errors.New("intrinsic gas too low"),
		},
		{
			"Deploy contract tx - vm revert",
			func() {
				msg, err = utiltx.CreateRevertingContractMsgTx(
					vmdb.GetNonce(suite.Address),
					signer,
					big.NewInt(1),
					suite.Address,
					suite.Signer,
				)
				suite.Require().NoError(err)
				expectedGasUsed = msg.GetGas()
			},
			errors.New("invalid opcode: opcode 0xde not defined"),
		},
		{
			"Call no code tx - success",
			func() {
				msg, err = utiltx.CreateNoCodeCallMsgTx(
					vmdb.GetNonce(suite.Address),
					signer,
					big.NewInt(1),
					suite.Address,
					suite.Signer,
				)
				suite.Require().NoError(err)
				expectedGasUsed = msg.GetGas()
			},
			nil,
		},
		{
			"Transfer funds tx",
			func() {
				msg, _, err = newEthMsgTx(
					vmdb.GetNonce(suite.Address),
					suite.Address,
					suite.Signer,
					signer,
					ethtypes.AccessListTxType,
					nil,
					nil,
				)
				suite.Require().NoError(err)
				expectedGasUsed = msg.GetGas()
			},
			nil,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.SetupTest(suite.T())
			signer = ethtypes.LatestSignerForChainID(suite.App.EvmKeeper.ChainID())
			vmdb = suite.StateDB()

			tc.malleate()

			res, err := suite.App.EvmKeeper.EthereumTx(suite.Ctx, msg)
			if tc.expErr != nil {
				suite.Require().ErrorContains(err, tc.expErr.Error())
				return
			}

			suite.Require().NoError(err)
			suite.Require().Equal(expectedGasUsed, res.GasUsed)
			suite.Require().False(res.Failed())
		})
	}
}

func (suite *MsgServerTestSuite) TestUpdateParams() {
	testCases := []struct {
		name      string
		request   *types.MsgUpdateParams
		expectErr bool
	}{
		{
			name:      "fail - invalid authority",
			request:   &types.MsgUpdateParams{Authority: "foobar"},
			expectErr: true,
		},
		{
			name: "pass - valid Update msg",
			request: &types.MsgUpdateParams{
				Authority: authtypes.NewModuleAddress(govtypes.ModuleName).String(),
				Params:    types.DefaultParams(),
			},
			expectErr: false,
		},
	}

	for _, tc := range testCases {
		suite.Run("MsgUpdateParams", func() {
			suite.SetupTest(suite.T())
			_, err := suite.App.EvmKeeper.UpdateParams(suite.Ctx, tc.request)
			if tc.expectErr {
				suite.Require().Error(err)
			} else {
				suite.Require().NoError(err)
			}
		})
	}
}
