package keeper_test

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	ethlogger "github.com/ethereum/go-ethereum/eth/tracers/logger"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/Helios-Chain-Labs/ethermint/app"
	"github.com/Helios-Chain-Labs/ethermint/server/config"
	"github.com/Helios-Chain-Labs/ethermint/tests"
	"github.com/Helios-Chain-Labs/ethermint/testutil"
	ethermint "github.com/Helios-Chain-Labs/ethermint/types"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/statedb"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/types"
	evmtypes "github.com/Helios-Chain-Labs/ethermint/x/evm/types"
	feemarkettypes "github.com/Helios-Chain-Labs/ethermint/x/feemarket/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// Not valid Ethereum address
const invalidAddress = "0x0000"

type GRPCServerTestSuiteSuite struct {
	testutil.EVMTestSuiteWithAccountAndQueryClient
	enableFeemarket bool
	enableLondonHF  bool
}

func (suite *GRPCServerTestSuiteSuite) SetupTest() {
	suite.EVMTestSuiteWithAccountAndQueryClient.SetupTestWithCb(suite.T(), func(app *app.EthermintApp, genesis app.GenesisState) app.GenesisState {
		feemarketGenesis := feemarkettypes.DefaultGenesisState()
		if suite.enableFeemarket {
			feemarketGenesis.Params.EnableHeight = 1
			feemarketGenesis.Params.NoBaseFee = false
		} else {
			feemarketGenesis.Params.NoBaseFee = true
		}
		genesis[feemarkettypes.ModuleName] = app.AppCodec().MustMarshalJSON(feemarketGenesis)
		if !suite.enableLondonHF {
			evmGenesis := types.DefaultGenesisState()
			maxInt := sdkmath.NewInt(math.MaxInt64)
			evmGenesis.Params.ChainConfig.LondonBlock = &maxInt
			evmGenesis.Params.ChainConfig.ArrowGlacierBlock = &maxInt
			evmGenesis.Params.ChainConfig.GrayGlacierBlock = &maxInt
			evmGenesis.Params.ChainConfig.MergeNetsplitBlock = &maxInt
			evmGenesis.Params.ChainConfig.ShanghaiTime = &maxInt
			genesis[types.ModuleName] = app.AppCodec().MustMarshalJSON(evmGenesis)
		}
		return genesis
	})
}

func TestGRPCServerTestSuite(t *testing.T) {
	s := new(GRPCServerTestSuiteSuite)
	s.enableFeemarket = false
	s.enableLondonHF = true
	suite.Run(t, s)
}

// deployTestContract deploy a test erc20 contract and returns the contract address
func (suite *GRPCServerTestSuiteSuite) deployTestContract(owner common.Address) common.Address {
	supply := sdkmath.NewIntWithDecimal(1000, 18).BigInt()
	return suite.EVMTestSuiteWithAccountAndQueryClient.DeployTestContract(
		suite.T(),
		owner,
		supply,
		suite.enableFeemarket,
	)
}

func (suite *GRPCServerTestSuiteSuite) transferERC20Token(t require.TestingT, contractAddr, from, to common.Address, amount *big.Int) *types.MsgEthereumTx {
	chainID := suite.App.EvmKeeper.ChainID()

	transferData, err := types.ERC20Contract.ABI.Pack("transfer", to, amount)
	require.NoError(t, err)
	args, err := json.Marshal(&types.TransactionArgs{To: &contractAddr, From: &from, Data: (*hexutil.Bytes)(&transferData)})
	require.NoError(t, err)
	res, err := suite.EvmQueryClient.EstimateGas(suite.Ctx, &types.EthCallRequest{
		Args:            args,
		GasCap:          25_000_000,
		ProposerAddress: suite.Ctx.BlockHeader().ProposerAddress,
	})
	require.NoError(t, err)

	nonce := suite.App.EvmKeeper.GetNonce(suite.Ctx, suite.Address)

	var ercTransferTx *types.MsgEthereumTx
	if suite.enableFeemarket {
		ercTransferTx = types.NewTx(
			chainID,
			nonce,
			&contractAddr,
			nil,
			res.Gas,
			nil,
			suite.App.FeeMarketKeeper.GetBaseFee(suite.Ctx),
			big.NewInt(1),
			transferData,
			&ethtypes.AccessList{}, // accesses
		)
	} else {
		ercTransferTx = types.NewTx(
			chainID,
			nonce,
			&contractAddr,
			nil,
			res.Gas,
			nil,
			nil, nil,
			transferData,
			nil,
		)
	}

	ercTransferTx.From = suite.Address.Bytes()
	err = ercTransferTx.Sign(ethtypes.LatestSignerForChainID(chainID), suite.Signer)
	require.NoError(t, err)
	rsp, err := suite.App.EvmKeeper.EthereumTx(suite.Ctx, ercTransferTx)
	require.NoError(t, err)
	require.Empty(t, rsp.VmError)
	return ercTransferTx
}

func (suite *GRPCServerTestSuiteSuite) TestQueryAccount() {
	var (
		req        *types.QueryAccountRequest
		expAccount *types.QueryAccountResponse
	)

	testCases := []struct {
		msg      string
		malleate func()
		expPass  bool
	}{
		{
			"invalid address",
			func() {
				expAccount = &types.QueryAccountResponse{
					Balance:  "0",
					CodeHash: common.BytesToHash(crypto.Keccak256(nil)).Hex(),
					Nonce:    0,
				}
				req = &types.QueryAccountRequest{
					Address: invalidAddress,
				}
			},
			false,
		},
		{
			"success",
			func() {
				amt := sdk.Coins{ethermint.NewDefaultCoinInt64(100)}
				err := suite.App.BankKeeper.MintCoins(suite.Ctx, types.ModuleName, amt)
				suite.Require().NoError(err)
				err = suite.App.BankKeeper.SendCoinsFromModuleToAccount(suite.Ctx, types.ModuleName, suite.Address.Bytes(), amt)
				suite.Require().NoError(err)

				expAccount = &types.QueryAccountResponse{
					Balance:  "100",
					CodeHash: common.BytesToHash(crypto.Keccak256(nil)).Hex(),
					Nonce:    0,
				}
				req = &types.QueryAccountRequest{
					Address: suite.Address.String(),
				}
			},
			true,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.SetupTest() // reset
			tc.malleate()
			res, err := suite.EvmQueryClient.Account(suite.Ctx, req)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().NotNil(res)

				suite.Require().Equal(expAccount, res)
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestQueryCosmosAccount() {
	var (
		req        *types.QueryCosmosAccountRequest
		expAccount *types.QueryCosmosAccountResponse
	)

	testCases := []struct {
		msg      string
		malleate func()
		expPass  bool
	}{
		{
			"invalid address",
			func() {
				expAccount = &types.QueryCosmosAccountResponse{
					CosmosAddress: sdk.AccAddress(common.Address{}.Bytes()).String(),
				}
				req = &types.QueryCosmosAccountRequest{
					Address: invalidAddress,
				}
			},
			false,
		},
		{
			"success",
			func() {
				expAccount = &types.QueryCosmosAccountResponse{
					CosmosAddress: sdk.AccAddress(suite.Address.Bytes()).String(),
					Sequence:      0,
					AccountNumber: suite.App.AccountKeeper.NextAccountNumber(suite.Ctx) - 1,
				}
				req = &types.QueryCosmosAccountRequest{
					Address: suite.Address.String(),
				}
			},
			true,
		},
		{
			"success with seq and account number",
			func() {
				acc := suite.App.AccountKeeper.GetAccount(suite.Ctx, suite.Address.Bytes())
				suite.Require().NoError(acc.SetSequence(10))
				num := suite.App.AccountKeeper.NextAccountNumber(suite.Ctx)
				suite.Require().NoError(acc.SetAccountNumber(num))
				suite.App.AccountKeeper.SetAccount(suite.Ctx, acc)
				expAccount = &types.QueryCosmosAccountResponse{
					CosmosAddress: sdk.AccAddress(suite.Address.Bytes()).String(),
					Sequence:      10,
					AccountNumber: num,
				}
				req = &types.QueryCosmosAccountRequest{
					Address: suite.Address.String(),
				}
			},
			true,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.SetupTest() // reset
			tc.malleate()
			res, err := suite.EvmQueryClient.CosmosAccount(suite.Ctx, req)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().NotNil(res)

				suite.Require().Equal(expAccount, res)
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestQueryBalance() {
	var (
		req        *types.QueryBalanceRequest
		expBalance string
	)

	testCases := []struct {
		msg      string
		malleate func()
		expPass  bool
	}{
		{
			"invalid address",
			func() {
				expBalance = "0"
				req = &types.QueryBalanceRequest{
					Address: invalidAddress,
				}
			},
			false,
		},
		{
			"success",
			func() {
				amt := sdk.Coins{ethermint.NewDefaultCoinInt64(100)}
				err := suite.App.BankKeeper.MintCoins(suite.Ctx, types.ModuleName, amt)
				suite.Require().NoError(err)
				err = suite.App.BankKeeper.SendCoinsFromModuleToAccount(suite.Ctx, types.ModuleName, suite.Address.Bytes(), amt)
				suite.Require().NoError(err)

				expBalance = "100"
				req = &types.QueryBalanceRequest{
					Address: suite.Address.String(),
				}
			},
			true,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.SetupTest() // reset
			tc.malleate()
			res, err := suite.EvmQueryClient.Balance(suite.Ctx, req)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().NotNil(res)

				suite.Require().Equal(expBalance, res.Balance)
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestQueryStorage() {
	var (
		req      *types.QueryStorageRequest
		expValue string
	)

	testCases := []struct {
		msg      string
		malleate func(vm.StateDB)
		expPass  bool
	}{
		{
			"invalid address",
			func(vm.StateDB) {
				req = &types.QueryStorageRequest{
					Address: invalidAddress,
				}
			},
			false,
		},
		{
			"success",
			func(vmdb vm.StateDB) {
				key := common.BytesToHash([]byte("key"))
				value := common.BytesToHash([]byte("value"))
				expValue = value.String()
				vmdb.SetState(suite.Address, key, value)
				req = &types.QueryStorageRequest{
					Address: suite.Address.String(),
					Key:     key.String(),
				}
			},
			true,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.SetupTest() // reset

			vmdb := suite.StateDB()
			tc.malleate(vmdb)
			suite.Require().NoError(vmdb.Commit())
			res, err := suite.EvmQueryClient.Storage(suite.Ctx, req)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().NotNil(res)

				suite.Require().Equal(expValue, res.Value)
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestQueryCode() {
	var (
		req     *types.QueryCodeRequest
		expCode []byte
	)

	testCases := []struct {
		msg      string
		malleate func(vm.StateDB)
		expPass  bool
	}{
		{
			"invalid address",
			func(vm.StateDB) {
				req = &types.QueryCodeRequest{
					Address: invalidAddress,
				}
				exp := &types.QueryCodeResponse{}
				expCode = exp.Code
			},
			false,
		},
		{
			"success",
			func(vmdb vm.StateDB) {
				expCode = []byte("code")
				vmdb.SetCode(suite.Address, expCode)

				req = &types.QueryCodeRequest{
					Address: suite.Address.String(),
				}
			},
			true,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.SetupTest() // reset

			vmdb := suite.StateDB()
			tc.malleate(vmdb)
			suite.Require().NoError(vmdb.Commit())
			res, err := suite.EvmQueryClient.Code(suite.Ctx, req)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().NotNil(res)

				suite.Require().Equal(expCode, res.Code)
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestQueryTxLogs() {
	var expLogs []*types.Log
	txHash := common.BytesToHash([]byte("tx_hash"))
	txIndex := uint(1)
	logIndex := uint(1)

	testCases := []struct {
		msg      string
		malleate func(vm.StateDB)
	}{
		{
			"empty logs",
			func(vm.StateDB) {
				expLogs = nil
			},
		},
		{
			"success",
			func(vmdb vm.StateDB) {
				expLogs = []*types.Log{
					{
						Address:     evmtypes.HexAddress(suite.Address.Bytes()),
						Topics:      []string{common.BytesToHash([]byte("topic")).String()},
						Data:        []byte("data"),
						BlockNumber: 1,
						TxHash:      txHash.String(),
						TxIndex:     uint64(txIndex),
						BlockHash:   common.BytesToHash(suite.Ctx.HeaderHash()).Hex(),
						Index:       uint64(logIndex),
						Removed:     false,
					},
				}

				for _, log := range types.LogsToEthereum(expLogs) {
					vmdb.AddLog(log)
				}
			},
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.SetupTest() // reset

			vmdb := statedb.New(suite.Ctx, suite.App.EvmKeeper, statedb.NewTxConfig(common.BytesToHash(suite.Ctx.HeaderHash()), txHash, txIndex, logIndex))
			tc.malleate(vmdb)
			suite.Require().NoError(vmdb.Commit())

			logs := vmdb.Logs()
			suite.Require().Equal(expLogs, types.NewLogsFromEth(logs))
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestQueryParams() {
	expParams := types.DefaultParams()
	res, err := suite.EvmQueryClient.Params(suite.Ctx, &types.QueryParamsRequest{})
	suite.Require().NoError(err)
	suite.Require().Equal(expParams, res.Params)
}

func (suite *GRPCServerTestSuiteSuite) TestQueryValidatorAccount() {
	var (
		req        *types.QueryValidatorAccountRequest
		expAccount *types.QueryValidatorAccountResponse
	)

	testCases := []struct {
		msg      string
		malleate func()
		expPass  bool
	}{
		{
			"invalid address",
			func() {
				expAccount = &types.QueryValidatorAccountResponse{
					AccountAddress: sdk.AccAddress(common.Address{}.Bytes()).String(),
				}
				req = &types.QueryValidatorAccountRequest{
					ConsAddress: "",
				}
			},
			false,
		},
		{
			"success",
			func() {
				expAccount = &types.QueryValidatorAccountResponse{
					AccountAddress: sdk.AccAddress(suite.Address.Bytes()).String(),
					Sequence:       0,
					AccountNumber:  suite.App.AccountKeeper.NextAccountNumber(suite.Ctx) - 1,
				}
				req = &types.QueryValidatorAccountRequest{
					ConsAddress: suite.ConsAddress.String(),
				}
			},
			true,
		},
		{
			"success with seq and account number",
			func() {
				acc := suite.App.AccountKeeper.GetAccount(suite.Ctx, suite.Address.Bytes())
				suite.Require().NoError(acc.SetSequence(10))
				num := suite.App.AccountKeeper.NextAccountNumber(suite.Ctx)
				suite.Require().NoError(acc.SetAccountNumber(num))
				suite.App.AccountKeeper.SetAccount(suite.Ctx, acc)
				expAccount = &types.QueryValidatorAccountResponse{
					AccountAddress: sdk.AccAddress(suite.Address.Bytes()).String(),
					Sequence:       10,
					AccountNumber:  num,
				}
				req = &types.QueryValidatorAccountRequest{
					ConsAddress: suite.ConsAddress.String(),
				}
			},
			true,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.SetupTest() // reset
			tc.malleate()
			res, err := suite.EvmQueryClient.ValidatorAccount(suite.Ctx, req)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().NotNil(res)

				suite.Require().Equal(expAccount, res)
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestEstimateGas() {
	gasHelper := hexutil.Uint64(20000)
	higherGas := hexutil.Uint64(25000)
	hexBigInt := hexutil.Big(*big.NewInt(1))

	var (
		args   interface{}
		gasCap uint64
	)
	testCases := []struct {
		msg             string
		malleate        func()
		expPass         bool
		expGas          uint64
		enableFeemarket bool
	}{
		// should success, because transfer value is zero
		{
			"default args - special case for ErrIntrinsicGas on contract creation, raise gas limit",
			func() {
				args = types.TransactionArgs{}
			},
			true,
			ethparams.TxGasContractCreation,
			false,
		},
		// should success, because transfer value is zero
		{
			"default args with 'to' address",
			func() {
				args = types.TransactionArgs{To: &common.Address{}}
			},
			true,
			ethparams.TxGas,
			false,
		},
		// should fail, because the default From address(zero address) don't have fund
		{
			"not enough balance",
			func() {
				args = types.TransactionArgs{To: &common.Address{}, Value: (*hexutil.Big)(big.NewInt(100))}
			},
			false,
			0,
			false,
		},
		// should success, enough balance now
		{
			"enough balance",
			func() {
				args = types.TransactionArgs{To: &common.Address{}, From: &suite.Address, Value: (*hexutil.Big)(big.NewInt(100))}
			}, false, 0, false,
		},
		// should success, because gas limit lower than 21000 is ignored
		{
			"gas exceed allowance",
			func() {
				args = types.TransactionArgs{To: &common.Address{}, Gas: &gasHelper}
			},
			true,
			ethparams.TxGas,
			false,
		},
		// should fail, invalid gas cap
		{
			"gas exceed global allowance",
			func() {
				args = types.TransactionArgs{To: &common.Address{}}
				gasCap = 20000
			},
			false,
			0,
			false,
		},
		// estimate gas of an erc20 contract deployment, the exact gas number is checked with geth
		{
			"contract deployment",
			func() {
				ctorArgs, err := types.ERC20Contract.ABI.Pack("", &suite.Address, sdkmath.NewIntWithDecimal(1000, 18).BigInt())
				suite.Require().NoError(err)
				data := append(types.ERC20Contract.Bin, ctorArgs...)
				args = types.TransactionArgs{
					From: &suite.Address,
					Data: (*hexutil.Bytes)(&data),
				}
			},
			true,
			1187108,
			false,
		},
		// estimate gas of an erc20 transfer, the exact gas number is checked with geth
		{
			"erc20 transfer",
			func() {
				contractAddr := suite.deployTestContract(suite.Address)
				suite.Commit(suite.T())
				transferData, err := types.ERC20Contract.ABI.Pack("transfer", common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), big.NewInt(1000))
				suite.Require().NoError(err)
				args = types.TransactionArgs{To: &contractAddr, From: &suite.Address, Data: (*hexutil.Bytes)(&transferData)}
			},
			true,
			51880,
			false,
		},
		// repeated tests with enableFeemarket
		{
			"default args w/ enableFeemarket",
			func() {
				args = types.TransactionArgs{To: &common.Address{}}
			},
			true,
			ethparams.TxGas,
			true,
		},
		{
			"not enough balance w/ enableFeemarket",
			func() {
				args = types.TransactionArgs{To: &common.Address{}, Value: (*hexutil.Big)(big.NewInt(100))}
			},
			false,
			0,
			true,
		},
		{
			"enough balance w/ enableFeemarket",
			func() {
				args = types.TransactionArgs{To: &common.Address{}, From: &suite.Address, Value: (*hexutil.Big)(big.NewInt(100))}
			},
			false,
			0,
			true,
		},
		{
			"gas exceed allowance w/ enableFeemarket",
			func() {
				args = types.TransactionArgs{To: &common.Address{}, Gas: &gasHelper}
			},
			true,
			ethparams.TxGas,
			true,
		},
		{
			"gas exceed global allowance w/ enableFeemarket",
			func() {
				args = types.TransactionArgs{To: &common.Address{}}
				gasCap = 20000
			},
			false,
			0,
			true,
		},
		{
			"contract deployment w/ enableFeemarket",
			func() {
				ctorArgs, err := types.ERC20Contract.ABI.Pack("", &suite.Address, sdkmath.NewIntWithDecimal(1000, 18).BigInt())
				suite.Require().NoError(err)
				data := append(types.ERC20Contract.Bin, ctorArgs...)
				args = types.TransactionArgs{
					From: &suite.Address,
					Data: (*hexutil.Bytes)(&data),
				}
			},
			true,
			1187108,
			true,
		},
		{
			"erc20 transfer w/ enableFeemarket",
			func() {
				contractAddr := suite.deployTestContract(suite.Address)
				suite.Commit(suite.T())
				transferData, err := types.ERC20Contract.ABI.Pack("transfer", common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), big.NewInt(1000))
				suite.Require().NoError(err)
				args = types.TransactionArgs{To: &contractAddr, From: &suite.Address, Data: (*hexutil.Bytes)(&transferData)}
			},
			true,
			51880,
			true,
		},
		{
			"contract creation but 'create' param disabled",
			func() {
				ctorArgs, err := types.ERC20Contract.ABI.Pack("", &suite.Address, sdkmath.NewIntWithDecimal(1000, 18).BigInt())
				suite.Require().NoError(err)
				data := append(types.ERC20Contract.Bin, ctorArgs...)
				args = types.TransactionArgs{
					From: &suite.Address,
					Data: (*hexutil.Bytes)(&data),
				}
				params := suite.App.EvmKeeper.GetParams(suite.Ctx)
				params.EnableCreate = false
				suite.App.EvmKeeper.SetParams(suite.Ctx, params)
			},
			false,
			0,
			false,
		},
		{
			"specified gas in args higher than ethparams.TxGas (21,000)",
			func() {
				args = types.TransactionArgs{
					To:  &common.Address{},
					Gas: &higherGas,
				}
			},
			true,
			ethparams.TxGas,
			false,
		},
		{
			"specified gas in args higher than request gasCap",
			func() {
				gasCap = 22_000
				args = types.TransactionArgs{
					To:  &common.Address{},
					Gas: &higherGas,
				}
			},
			true,
			ethparams.TxGas,
			false,
		},
		{
			"invalid args - specified both gasPrice and maxFeePerGas",
			func() {
				args = types.TransactionArgs{
					To:           &common.Address{},
					GasPrice:     &hexBigInt,
					MaxFeePerGas: &hexBigInt,
				}
			},
			false,
			0,
			false,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.enableFeemarket = tc.enableFeemarket
			suite.SetupTest()
			gasCap = 25_000_000
			tc.malleate()

			args, err := json.Marshal(&args)
			suite.Require().NoError(err)
			req := types.EthCallRequest{
				Args:            args,
				GasCap:          gasCap,
				ProposerAddress: suite.Ctx.BlockHeader().ProposerAddress,
			}
			rsp, err := suite.EvmQueryClient.EstimateGas(suite.Ctx, &req)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().Equal(int64(tc.expGas), int64(rsp.Gas))
			} else {
				suite.Require().Error(err)
			}
		})
	}
	suite.enableFeemarket = false // reset flag
}

func (suite *GRPCServerTestSuiteSuite) TestTraceTx() {
	// TODO deploy contract that triggers internal transactions
	var (
		txMsg        *types.MsgEthereumTx
		traceConfig  *types.TraceConfig
		predecessors []*types.MsgEthereumTx
		chainID      *sdkmath.Int
	)

	testCases := []struct {
		msg             string
		malleate        func()
		expPass         bool
		traceResponse   string
		enableFeemarket bool
	}{
		{
			msg: "default trace",
			malleate: func() {
				traceConfig = nil
				predecessors = []*types.MsgEthereumTx{}
			},
			expPass:       true,
			traceResponse: "{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PUSH1\",\"gas\":",
		},
		{
			msg: "default trace with filtered response",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
				}
				predecessors = []*types.MsgEthereumTx{}
			},
			expPass:         true,
			traceResponse:   "{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PUSH1\",\"gas\":",
			enableFeemarket: false,
		},
		{
			msg: "javascript tracer",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					Tracer: "{data: [], fault: function(log) {}, step: function(log) { if(log.op.toString() == \"CALL\") this.data.push(log.stack.peek(0)); }, result: function() { return this.data; }}",
				}
				predecessors = []*types.MsgEthereumTx{}
			},
			expPass:       true,
			traceResponse: "[]",
		},
		{
			msg: "default trace with enableFeemarket",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
				}
				predecessors = []*types.MsgEthereumTx{}
			},
			expPass:         true,
			traceResponse:   "{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PUSH1\",\"gas\":",
			enableFeemarket: true,
		},
		{
			msg: "javascript tracer with enableFeemarket",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					Tracer: "{data: [], fault: function(log) {}, step: function(log) { if(log.op.toString() == \"CALL\") this.data.push(log.stack.peek(0)); }, result: function() { return this.data; }}",
				}
				predecessors = []*types.MsgEthereumTx{}
			},
			expPass:         true,
			traceResponse:   "[]",
			enableFeemarket: true,
		},
		{
			msg: "default tracer with predecessors",
			malleate: func() {
				traceConfig = nil

				// increase nonce to avoid address collision
				vmdb := suite.StateDB()
				vmdb.SetNonce(suite.Address, vmdb.GetNonce(suite.Address)+1)
				suite.Require().NoError(vmdb.Commit())
				contractAddr := suite.deployTestContract(suite.Address)
				suite.Commit(suite.T())
				// Generate token transfer transaction
				firstTx := suite.transferERC20Token(suite.T(), contractAddr, suite.Address, common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), sdkmath.NewIntWithDecimal(1, 18).BigInt())
				txMsg = suite.transferERC20Token(suite.T(), contractAddr, suite.Address, common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), sdkmath.NewIntWithDecimal(1, 18).BigInt())
				suite.Commit(suite.T())

				predecessors = append(predecessors, firstTx)
			},
			expPass:         true,
			traceResponse:   "{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PUSH1\",\"gas\":",
			enableFeemarket: false,
		},
		{
			msg: "invalid trace config - Negative Limit",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
					Limit:          -1,
				}
			},
			expPass: false,
		},
		{
			msg: "invalid trace config - Invalid Tracer",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
					Tracer:         "invalid_tracer",
				}
			},
			expPass: false,
		},
		{
			msg: "invalid trace config - Invalid Timeout",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
					Timeout:        "wrong_time",
				}
			},
			expPass: false,
		},
		{
			msg: "default tracer with contract creation tx as predecessor but 'create' param disabled",
			malleate: func() {
				traceConfig = nil

				// increase nonce to avoid address collision
				vmdb := suite.StateDB()
				vmdb.SetNonce(suite.Address, vmdb.GetNonce(suite.Address)+1)
				suite.Require().NoError(vmdb.Commit())

				chainID := suite.App.EvmKeeper.ChainID()
				nonce := suite.App.EvmKeeper.GetNonce(suite.Ctx, suite.Address)
				data := types.ERC20Contract.Bin
				contractTx := types.NewTxContract(
					chainID,
					nonce,
					nil,                             // amount
					ethparams.TxGasContractCreation, // gasLimit
					nil,                             // gasPrice
					nil, nil,
					data, // input
					nil,  // accesses
				)

				predecessors = append(predecessors, contractTx)
				suite.Commit(suite.T())

				params := suite.App.EvmKeeper.GetParams(suite.Ctx)
				params.EnableCreate = false
				suite.App.EvmKeeper.SetParams(suite.Ctx, params)
			},
			expPass:       true,
			traceResponse: "{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PUSH1\",\"gas\":",
		},
		{
			msg: "invalid chain id",
			malleate: func() {
				traceConfig = nil
				predecessors = []*types.MsgEthereumTx{}
				tmp := sdkmath.NewInt(1)
				chainID = &tmp
			},
			expPass: false,
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			suite.enableFeemarket = tc.enableFeemarket
			suite.SetupTest()
			// Deploy contract
			contractAddr := suite.deployTestContract(suite.Address)
			suite.Commit(suite.T())
			// Generate token transfer transaction
			txMsg = suite.transferERC20Token(suite.T(), contractAddr, suite.Address, common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), sdkmath.NewIntWithDecimal(1, 18).BigInt())
			suite.Commit(suite.T())

			tc.malleate()
			traceReq := types.QueryTraceTxRequest{
				Msg:          txMsg,
				TraceConfig:  traceConfig,
				Predecessors: predecessors,
			}

			if chainID != nil {
				traceReq.ChainId = chainID.Int64()
			}
			res, err := suite.EvmQueryClient.TraceTx(suite.Ctx, &traceReq)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().Contains(string(res.Data), tc.traceResponse)
				if traceConfig == nil || traceConfig.Tracer == "" {
					var result ethlogger.ExecutionResult
					suite.Require().NoError(json.Unmarshal(res.Data, &result))
				}
			} else {
				suite.Require().Error(err)
			}
			// Reset for next test case
			chainID = nil
		})
	}

	suite.enableFeemarket = false // reset flag
}

func (suite *GRPCServerTestSuiteSuite) TestTraceBlock() {
	var (
		txs         []*types.MsgEthereumTx
		traceConfig *types.TraceConfig
		chainID     *sdkmath.Int
	)

	testCases := []struct {
		msg             string
		malleate        func()
		expPass         bool
		traceResponse   string
		enableFeemarket bool
	}{
		{
			msg: "default trace",
			malleate: func() {
				traceConfig = nil
			},
			expPass:       true,
			traceResponse: "[{\"result\":{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PU",
		},
		{
			msg: "filtered trace",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
				}
			},
			expPass:       true,
			traceResponse: "[{\"result\":{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PU",
		},
		{
			msg: "javascript tracer",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					Tracer: "{data: [], fault: function(log) {}, step: function(log) { if(log.op.toString() == \"CALL\") this.data.push(log.stack.peek(0)); }, result: function() { return this.data; }}",
				}
			},
			expPass:       true,
			traceResponse: "[{\"result\":[]}]",
		},
		{
			msg: "default trace with enableFeemarket and filtered return",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
				}
			},
			expPass:         true,
			traceResponse:   "[{\"result\":{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PU",
			enableFeemarket: true,
		},
		{
			msg: "javascript tracer with enableFeemarket",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					Tracer: "{data: [], fault: function(log) {}, step: function(log) { if(log.op.toString() == \"CALL\") this.data.push(log.stack.peek(0)); }, result: function() { return this.data; }}",
				}
			},
			expPass:         true,
			traceResponse:   "[{\"result\":[]}]",
			enableFeemarket: true,
		},
		{
			msg: "tracer with multiple transactions",
			malleate: func() {
				traceConfig = nil

				// increase nonce to avoid address collision
				vmdb := suite.StateDB()
				vmdb.SetNonce(suite.Address, vmdb.GetNonce(suite.Address)+1)
				suite.Require().NoError(vmdb.Commit())
				contractAddr := suite.deployTestContract(suite.Address)
				suite.Commit(suite.T())
				// create multiple transactions in the same block
				firstTx := suite.transferERC20Token(suite.T(), contractAddr, suite.Address, common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), sdkmath.NewIntWithDecimal(1, 18).BigInt())
				secondTx := suite.transferERC20Token(suite.T(), contractAddr, suite.Address, common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), sdkmath.NewIntWithDecimal(1, 18).BigInt())
				suite.Commit(suite.T())
				// overwrite txs to include only the ones on new block
				txs = append([]*types.MsgEthereumTx{}, firstTx, secondTx)
			},
			expPass:         true,
			traceResponse:   "[{\"result\":{\"gas\":0,\"failed\":false,\"returnValue\":\"0000000000000000000000000000000000000000000000000000000000000001\",\"structLogs\":[{\"pc\":0,\"op\":\"PU",
			enableFeemarket: false,
		},
		{
			msg: "invalid trace config - Negative Limit",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
					Limit:          -1,
				}
			},
			expPass: false,
		},
		{
			msg: "invalid trace config - Invalid Tracer",
			malleate: func() {
				traceConfig = &types.TraceConfig{
					DisableStack:   true,
					DisableStorage: true,
					EnableMemory:   false,
					Tracer:         "invalid_tracer",
				}
			},
			expPass:       true,
			traceResponse: "invalid_tracer is not defined",
		},
		{
			msg: "invalid chain id",
			malleate: func() {
				traceConfig = nil
				tmp := sdkmath.NewInt(1)
				chainID = &tmp
			},
			expPass:       true,
			traceResponse: "invalid chain id for signer",
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.msg), func() {
			txs = []*types.MsgEthereumTx{}
			suite.enableFeemarket = tc.enableFeemarket
			suite.SetupTest()
			// Deploy contract
			contractAddr := suite.deployTestContract(suite.Address)
			// set some balance to handle fees
			err := suite.App.EvmKeeper.SetBalance(suite.Ctx, suite.Address, big.NewInt(1000000000000000000), types.DefaultEVMDenom)
			suite.Require().NoError(err)
			suite.Commit(suite.T())
			// Generate token transfer transaction
			txMsg := suite.transferERC20Token(suite.T(), contractAddr, suite.Address, common.HexToAddress("0x378c50D9264C63F3F92B806d4ee56E9D86FfB3Ec"), sdkmath.NewIntWithDecimal(1, 18).BigInt())
			suite.Commit(suite.T())

			txs = append(txs, txMsg)

			tc.malleate()
			traceReq := types.QueryTraceBlockRequest{
				Txs:         txs,
				TraceConfig: traceConfig,
			}

			if chainID != nil {
				traceReq.ChainId = chainID.Int64()
			}
			res, err := suite.EvmQueryClient.TraceBlock(suite.Ctx, &traceReq)
			if tc.expPass {
				suite.Require().NoError(err)
				suite.Require().Contains(string(res.Data), tc.traceResponse)
			} else {
				suite.Require().Error(err)
			}
			// Reset for next case
			chainID = nil
		})
	}

	suite.enableFeemarket = false // reset flag
}

func (suite *GRPCServerTestSuiteSuite) TestNonceInQuery() {
	suite.SetupTest()
	address := tests.GenerateAddress()
	suite.Require().Equal(uint64(0), suite.App.EvmKeeper.GetNonce(suite.Ctx, address))
	supply := sdkmath.NewIntWithDecimal(1000, 18).BigInt()

	// accupy nonce 0
	_ = suite.deployTestContract(address)

	// do an EthCall/EstimateGas with nonce 0
	ctorArgs, err := types.ERC20Contract.ABI.Pack("", address, supply)
	suite.Require().NoError(err)

	data := append(types.ERC20Contract.Bin, ctorArgs...)
	args, err := json.Marshal(&types.TransactionArgs{
		From: &address,
		Data: (*hexutil.Bytes)(&data),
	})
	suite.Require().NoError(err)
	proposerAddress := suite.Ctx.BlockHeader().ProposerAddress
	_, err = suite.EvmQueryClient.EstimateGas(suite.Ctx, &types.EthCallRequest{
		Args:            args,
		GasCap:          uint64(config.DefaultGasCap),
		ProposerAddress: proposerAddress,
	})
	suite.Require().NoError(err)

	_, err = suite.EvmQueryClient.EthCall(suite.Ctx, &types.EthCallRequest{
		Args:            args,
		GasCap:          uint64(config.DefaultGasCap),
		ProposerAddress: proposerAddress,
	})
	suite.Require().NoError(err)
}

func (suite *GRPCServerTestSuiteSuite) TestQueryBaseFee() {
	var (
		aux    sdkmath.Int
		expRes *types.QueryBaseFeeResponse
	)

	testCases := []struct {
		name            string
		malleate        func()
		expPass         bool
		enableFeemarket bool
		enableLondonHF  bool
	}{
		{
			"pass - default Base Fee",
			func() {
				initialBaseFee := sdkmath.NewInt(ethparams.InitialBaseFee)
				expRes = &types.QueryBaseFeeResponse{BaseFee: &initialBaseFee}
			},
			true, true, true,
		},
		{
			"pass - non-nil Base Fee",
			func() {
				baseFee := sdkmath.OneInt().BigInt()
				suite.App.FeeMarketKeeper.SetBaseFee(suite.Ctx, baseFee)

				aux = sdkmath.NewIntFromBigInt(baseFee)
				expRes = &types.QueryBaseFeeResponse{BaseFee: &aux}
			},
			true, true, true,
		},
		{
			"pass - nil Base Fee when london hardfork not activated",
			func() {
				baseFee := sdkmath.OneInt().BigInt()
				suite.App.FeeMarketKeeper.SetBaseFee(suite.Ctx, baseFee)

				expRes = &types.QueryBaseFeeResponse{}
			},
			true, true, false,
		},
		{
			"pass - zero Base Fee when feemarket not activated",
			func() {
				baseFee := sdkmath.ZeroInt()
				expRes = &types.QueryBaseFeeResponse{BaseFee: &baseFee}
			},
			true, false, true,
		},
	}
	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.enableFeemarket = tc.enableFeemarket
			suite.enableLondonHF = tc.enableLondonHF
			suite.SetupTest()

			tc.malleate()

			res, err := suite.EvmQueryClient.BaseFee(suite.Ctx.Context(), &types.QueryBaseFeeRequest{})
			if tc.expPass {
				suite.Require().NotNil(res)
				suite.Require().Equal(expRes, res, tc.name)
				suite.Require().NoError(err)
			} else {
				suite.Require().Error(err)
			}
		})
	}
	suite.enableFeemarket = false
	suite.enableLondonHF = true
}

func (suite *GRPCServerTestSuiteSuite) TestEthCall() {
	var req *types.EthCallRequest

	address := tests.GenerateAddress()
	suite.Require().Equal(uint64(0), suite.App.EvmKeeper.GetNonce(suite.Ctx, address))
	supply := sdkmath.NewIntWithDecimal(1000, 18).BigInt()

	hexBigInt := hexutil.Big(*big.NewInt(1))
	ctorArgs, err := types.ERC20Contract.ABI.Pack("", address, supply)
	suite.Require().NoError(err)

	data := append(types.ERC20Contract.Bin, ctorArgs...)

	testCases := []struct {
		name     string
		malleate func()
		expPass  bool
	}{
		{
			"invalid args",
			func() {
				req = &types.EthCallRequest{Args: []byte("invalid args"), GasCap: uint64(config.DefaultGasCap)}
			},
			false,
		},
		{
			"invalid args - specified both gasPrice and maxFeePerGas",
			func() {
				args, err := json.Marshal(&types.TransactionArgs{
					From:         &address,
					Data:         (*hexutil.Bytes)(&data),
					GasPrice:     &hexBigInt,
					MaxFeePerGas: &hexBigInt,
				})

				suite.Require().NoError(err)
				req = &types.EthCallRequest{Args: args, GasCap: uint64(config.DefaultGasCap)}
			},
			false,
		},
		{
			"set param EnableCreate = false",
			func() {
				args, err := json.Marshal(&types.TransactionArgs{
					From: &address,
					Data: (*hexutil.Bytes)(&data),
				})

				suite.Require().NoError(err)
				req = &types.EthCallRequest{Args: args, GasCap: uint64(config.DefaultGasCap)}

				params := suite.App.EvmKeeper.GetParams(suite.Ctx)
				params.EnableCreate = false
				suite.App.EvmKeeper.SetParams(suite.Ctx, params)
			},
			false,
		},
	}
	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.SetupTest()
			tc.malleate()

			res, err := suite.EvmQueryClient.EthCall(suite.Ctx, req)
			if tc.expPass {
				suite.Require().NotNil(res)
				suite.Require().NoError(err)
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *GRPCServerTestSuiteSuite) TestEmptyRequest() {
	testCases := []struct {
		name      string
		queryFunc func() (interface{}, error)
	}{
		{
			"Account method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.Account(suite.Ctx, nil)
			},
		},
		{
			"CosmosAccount method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.CosmosAccount(suite.Ctx, nil)
			},
		},
		{
			"ValidatorAccount method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.ValidatorAccount(suite.Ctx, nil)
			},
		},
		{
			"Balance method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.Balance(suite.Ctx, nil)
			},
		},
		{
			"Storage method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.Storage(suite.Ctx, nil)
			},
		},
		{
			"Code method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.Code(suite.Ctx, nil)
			},
		},
		{
			"EthCall method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.EthCall(suite.Ctx, nil)
			},
		},
		{
			"EstimateGas method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.EstimateGas(suite.Ctx, nil)
			},
		},
		{
			"TraceTx method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.TraceTx(suite.Ctx, nil)
			},
		},
		{
			"TraceBlock method",
			func() (interface{}, error) {
				return suite.App.EvmKeeper.TraceBlock(suite.Ctx, nil)
			},
		},
	}

	for _, tc := range testCases {
		suite.Run(fmt.Sprintf("Case %s", tc.name), func() {
			suite.SetupTest()
			_, err := tc.queryFunc()
			suite.Require().Error(err)
		})
	}
}
