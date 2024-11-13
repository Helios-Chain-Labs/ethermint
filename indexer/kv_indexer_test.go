package indexer_test

import (
	"math/big"
	"testing"

	tmlog "cosmossdk.io/log"
	abci "github.com/cometbft/cometbft/abci/types"
	tmtypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/Helios-Chain-Labs/ethermint/crypto/ethsecp256k1"
	"github.com/Helios-Chain-Labs/ethermint/indexer"
	"github.com/Helios-Chain-Labs/ethermint/tests"
	"github.com/Helios-Chain-Labs/ethermint/testutil/config"
	"github.com/Helios-Chain-Labs/ethermint/x/evm/types"
	"github.com/stretchr/testify/require"
)

func TestKVIndexer(t *testing.T) {
	priv, err := ethsecp256k1.GenerateKey()
	require.NoError(t, err)
	from := common.BytesToAddress(priv.PubKey().Address().Bytes())
	signer := tests.NewSigner(priv)
	ethSigner := ethtypes.LatestSignerForChainID(nil)

	to := common.BigToAddress(big.NewInt(1))
	tx := types.NewTx(
		nil, 0, &to, big.NewInt(1000), 21000, nil, nil, nil, nil, nil,
	)
	tx.From = from.Bytes()
	require.NoError(t, tx.Sign(ethSigner, signer))
	txHash := tx.AsTransaction().Hash()

	encodingConfig := config.MakeConfigForTest(nil)
	clientCtx := client.Context{}.WithTxConfig(encodingConfig.TxConfig).WithCodec(encodingConfig.Codec)

	// build cosmos-sdk wrapper tx
	tmTx, err := tx.BuildTx(clientCtx.TxConfig.NewTxBuilder(), types.DefaultEVMDenom)
	require.NoError(t, err)
	txBz, err := clientCtx.TxConfig.TxEncoder()(tmTx)
	require.NoError(t, err)

	// build an invalid wrapper tx
	builder := clientCtx.TxConfig.NewTxBuilder()
	require.NoError(t, builder.SetMsgs(tx))
	tmTx2 := builder.GetTx()
	txBz2, err := clientCtx.TxConfig.TxEncoder()(tmTx2)
	require.NoError(t, err)

	testCases := []struct {
		name        string
		block       *tmtypes.Block
		blockResult []*abci.ExecTxResult
		expSuccess  bool
	}{
		{
			"success",
			&tmtypes.Block{Header: tmtypes.Header{Height: 1}, Data: tmtypes.Data{Txs: []tmtypes.Tx{txBz}}},
			[]*abci.ExecTxResult{
				{
					Code: 0,
					Events: []abci.Event{
						{Type: types.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
							{Key: "ethereumTxHash", Value: txHash.Hex()},
							{Key: "txIndex", Value: "0"},
							{Key: "amount", Value: "1000"},
							{Key: "txGasUsed", Value: "21000"},
							{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
							{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
						}},
					},
				},
			},
			true,
		},
		{
			"success, exceed block gas limit",
			&tmtypes.Block{Header: tmtypes.Header{Height: 1}, Data: tmtypes.Data{Txs: []tmtypes.Tx{txBz}}},
			[]*abci.ExecTxResult{
				{
					Code:   11,
					Log:    "out of gas in location: block gas meter; gasWanted: 21000",
					Events: []abci.Event{},
				},
			},
			true,
		},
		{
			"success, failed eth tx (vm error)",
			&tmtypes.Block{Header: tmtypes.Header{Height: 1}, Data: tmtypes.Data{Txs: []tmtypes.Tx{txBz}}},
			[]*abci.ExecTxResult{
				{
					Code:   15,
					Log:    "failed to execute message; message index: 0: {\"tx_hash\":\"0x650d5204b07e83b00f489698ea55dd8259cbef658ca95723d1929a985fba8639\",\"gas_used\":120000,\"vm_error\":\"contract creation code storage out of gas\"}",
					Events: []abci.Event{},
				},
			},
			true,
		},
		{
			"success, failed eth tx (vm revert)",
			&tmtypes.Block{Header: tmtypes.Header{Height: 1}, Data: tmtypes.Data{Txs: []tmtypes.Tx{txBz}}},
			[]*abci.ExecTxResult{
				{
					Code:   3,
					Log:    "failed to execute message; message index: 0: {\"tx_hash\":\"0x8d8997cf065cbc6dd44a6c64645633e78bcc51534cc4c11dad1e059318134cc7\",\"gas_used\":60000,\"reason\":\"user requested a revert; see event for details\",\"vm_error\":\"execution reverted\",\"ret\":\"CMN5oAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAC51c2VyIHJlcXVlc3RlZCBhIHJldmVydDsgc2VlIGV2ZW50IGZvciBkZXRhaWxzAAAAAAAAAAAAAAAAAAAAAAAA\"}",
					Events: []abci.Event{},
				},
			},
			true,
		},
		{
			"fail, invalid events",
			&tmtypes.Block{Header: tmtypes.Header{Height: 1}, Data: tmtypes.Data{Txs: []tmtypes.Tx{txBz}}},
			[]*abci.ExecTxResult{
				{
					Code:   0,
					Events: []abci.Event{},
				},
			},
			false,
		},
		{
			"fail, not eth tx",
			&tmtypes.Block{Header: tmtypes.Header{Height: 1}, Data: tmtypes.Data{Txs: []tmtypes.Tx{txBz2}}},
			[]*abci.ExecTxResult{
				{
					Code:   0,
					Events: []abci.Event{},
				},
			},
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db := dbm.NewMemDB()
			idxer := indexer.NewKVIndexer(db, tmlog.NewNopLogger(), clientCtx)

			err = idxer.IndexBlock(tc.block, tc.blockResult)
			require.NoError(t, err)
			if !tc.expSuccess {
				first, err := idxer.FirstIndexedBlock()
				require.NoError(t, err)
				require.Equal(t, int64(-1), first)

				last, err := idxer.LastIndexedBlock()
				require.NoError(t, err)
				require.Equal(t, int64(-1), last)
			} else {
				first, err := idxer.FirstIndexedBlock()
				require.NoError(t, err)
				require.Equal(t, tc.block.Header.Height, first)

				last, err := idxer.LastIndexedBlock()
				require.NoError(t, err)
				require.Equal(t, tc.block.Header.Height, last)

				res1, err := idxer.GetByTxHash(txHash)
				require.NoError(t, err)
				require.NotNil(t, res1)
				res2, err := idxer.GetByBlockAndIndex(1, 0)
				require.NoError(t, err)
				require.Equal(t, res1, res2)
			}
		})
	}
}
