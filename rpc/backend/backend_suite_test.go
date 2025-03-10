package backend

import (
	"bufio"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	dbm "github.com/cosmos/cosmos-db"

	tmrpctypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/suite"

	"github.com/Helios-Chain-Labs/ethermint/crypto/ethsecp256k1"
	"github.com/Helios-Chain-Labs/ethermint/crypto/hd"
	"github.com/Helios-Chain-Labs/ethermint/encoding"
	"github.com/Helios-Chain-Labs/ethermint/indexer"
	"github.com/Helios-Chain-Labs/ethermint/rpc/backend/mocks"
	rpctypes "github.com/Helios-Chain-Labs/ethermint/rpc/types"
	"github.com/Helios-Chain-Labs/ethermint/tests"
	"github.com/Helios-Chain-Labs/ethermint/testutil"
	"github.com/Helios-Chain-Labs/ethermint/testutil/config"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/types"
	evmtypes "github.com/Helios-Chain-Labs/ethermint/x/evm/types"
)

type BackendTestSuite struct {
	suite.Suite
	backend       *Backend
	acc           sdk.AccAddress
	signer        keyring.Signer
	signerAddress sdk.AccAddress
}

func TestBackendTestSuite(t *testing.T) {
	suite.Run(t, new(BackendTestSuite))
}

// SetupTest is executed before every BackendTestSuite test
func (suite *BackendTestSuite) SetupTest() {
	ctx := server.NewDefaultContext()
	ctx.Viper.Set("telemetry.global-labels", []interface{}{})

	baseDir := suite.T().TempDir()
	nodeDirName := "node"
	clientDir := filepath.Join(baseDir, nodeDirName, "evmoscli")
	keyRing, err := suite.generateTestKeyring(clientDir)
	if err != nil {
		panic(err)
	}

	// Create Account with set sequence
	suite.acc = sdk.AccAddress(tests.GenerateAddress().Bytes())
	accounts := map[string]client.TestAccount{}
	accounts[suite.acc.String()] = client.TestAccount{
		Address: suite.acc,
		Num:     uint64(1),
		Seq:     uint64(1),
	}

	priv, err := ethsecp256k1.GenerateKey()
	suite.signer = tests.NewSigner(priv)
	suite.Require().NoError(err)
	suite.signerAddress = sdk.AccAddress(priv.PubKey().Address().Bytes())

	encodingConfig := config.MakeConfigForTest(nil)
	clientCtx := client.Context{}.WithChainID(testutil.TestnetChainID).
		WithHeight(1).
		WithTxConfig(encodingConfig.TxConfig).
		WithKeyringDir(clientDir).
		WithKeyring(keyRing).
		WithAccountRetriever(client.TestAccountRetriever{Accounts: accounts})

	allowUnprotectedTxs := false
	idxer := indexer.NewKVIndexer(dbm.NewMemDB(), ctx.Logger, clientCtx)

	suite.backend = NewBackend(ctx, ctx.Logger, clientCtx, allowUnprotectedTxs, idxer)
	suite.backend.queryClient.QueryClient = mocks.NewEVMQueryClient(suite.T())
	suite.backend.clientCtx.Client = mocks.NewClient(suite.T())
	suite.backend.queryClient.FeeMarket = mocks.NewFeeMarketQueryClient(suite.T())
	suite.backend.ctx = rpctypes.ContextWithHeight(1)

	// Add codec
	suite.backend.clientCtx.Codec = encodingConfig.Codec
}

// buildEthereumTx returns an example legacy Ethereum transaction
func (suite *BackendTestSuite) buildEthereumTx() (*evmtypes.MsgEthereumTx, []byte) {
	msgEthereumTx := evmtypes.NewTx(
		suite.backend.chainID,
		uint64(0),
		&common.Address{},
		big.NewInt(0),
		100000,
		big.NewInt(1),
		nil,
		nil,
		nil,
		nil,
	)
	msgEthereumTx.From = suite.signerAddress
	err := msgEthereumTx.Sign(ethtypes.LatestSignerForChainID(suite.backend.chainID), suite.signer)
	suite.Require().NoError(err)

	tx, err := msgEthereumTx.BuildTx(suite.backend.clientCtx.TxConfig.NewTxBuilder(), types.DefaultEVMDenom)
	suite.Require().NoError(err)

	bz, err := suite.backend.clientCtx.TxConfig.TxEncoder()(tx)
	suite.Require().NoError(err)

	sdkTx, err := suite.backend.clientCtx.TxConfig.TxDecoder()(bz)
	suite.Require().NoError(err)

	return sdkTx.GetMsgs()[0].(*evmtypes.MsgEthereumTx), bz
}

// buildFormattedBlock returns a formatted block for testing
func (suite *BackendTestSuite) buildFormattedBlock(
	blockRes *tmrpctypes.ResultBlockResults,
	resBlock *tmrpctypes.ResultBlock,
	fullTx bool,
	tx *evmtypes.MsgEthereumTx,
	validator sdk.AccAddress,
	baseFee *big.Int,
) map[string]interface{} {
	header := resBlock.Block.Header
	gasLimit := int64(^uint32(0)) // for `MaxGas = -1` (DefaultConsensusParams)
	gasUsed := new(big.Int).SetUint64(uint64(blockRes.TxsResults[0].GasUsed))

	root := common.Hash{}.Bytes()
	receipt := ethtypes.NewReceipt(root, false, gasUsed.Uint64())
	bloom := ethtypes.CreateBloom(ethtypes.Receipts{receipt})

	ethRPCTxs := []interface{}{}
	if tx != nil {
		if fullTx {
			rpcTx, err := rpctypes.NewRPCTransaction(
				tx,
				common.BytesToHash(header.Hash()),
				uint64(header.Height),
				uint64(0),
				baseFee,
				suite.backend.chainID,
			)
			suite.Require().NoError(err)
			ethRPCTxs = []interface{}{rpcTx}
		} else {
			ethRPCTxs = []interface{}{tx.Hash()}
		}
	}

	return rpctypes.FormatBlock(
		header,
		resBlock.Block.Size(),
		gasLimit,
		gasUsed,
		ethRPCTxs,
		bloom,
		common.BytesToAddress(validator.Bytes()),
		baseFee,
	)
}

func (suite *BackendTestSuite) generateTestKeyring(clientDir string) (keyring.Keyring, error) {
	buf := bufio.NewReader(os.Stdin)
	encCfg := encoding.MakeConfig()
	return keyring.New(sdk.KeyringServiceName(), keyring.BackendTest, clientDir, buf, encCfg.Codec, []keyring.Option{hd.EthSecp256k1Option()}...)
}

func (suite *BackendTestSuite) signAndEncodeEthTx(msgEthereumTx *evmtypes.MsgEthereumTx) []byte {
	from, priv := tests.NewAddrKey()
	signer := tests.NewSigner(priv)

	queryClient := suite.backend.queryClient.QueryClient.(*mocks.EVMQueryClient)
	RegisterParamsWithoutHeader(queryClient, 1)

	ethSigner := ethtypes.LatestSigner(suite.backend.ChainConfig())
	msgEthereumTx.From = from.Bytes()
	err := msgEthereumTx.Sign(ethSigner, signer)
	suite.Require().NoError(err)

	tx, err := msgEthereumTx.BuildTx(suite.backend.clientCtx.TxConfig.NewTxBuilder(), types.DefaultEVMDenom)
	suite.Require().NoError(err)

	txBz, err := suite.backend.clientCtx.TxConfig.TxEncoder()(tx)
	suite.Require().NoError(err)

	return txBz
}
