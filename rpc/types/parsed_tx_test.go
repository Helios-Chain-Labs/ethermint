package types

import (
	"math/big"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/ethereum/go-ethereum/common"
	evmtypes "github.com/evmos/ethermint/x/evm/types"
	"github.com/stretchr/testify/require"
)

func TestParseTxResult(t *testing.T) {
	address := "0x57f96e6B86CdeFdB3d412547816a82E3E0EbF9D2"
	txHash := common.BigToHash(big.NewInt(1))

	testCases := []struct {
		name     string
		response abci.ExecTxResult
		expTxs   []*ParsedTx // expected parse result, nil means expect error.
	}{
		{
			"from events, success",
			abci.ExecTxResult{
				GasUsed: 21000,
				Events: []abci.Event{
					{Type: "coin_received", Attributes: []abci.EventAttribute{
						{Key: "receiver", Value: "ethm12luku6uxehhak02py4rcz65zu0swh7wjun6msa"},
						{Key: "amount", Value: "1252860basetcro"},
					}},
					{Type: "coin_spent", Attributes: []abci.EventAttribute{
						{Key: "spender", Value: "ethm17xpfvakm2amg962yls6f84z3kell8c5lthdzgl"},
						{Key: "amount", Value: "1252860basetcro"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "amount", Value: "1000"},
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "0"},
						{Key: "txGasUsed", Value: "21000"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
					}},
					{Type: "message", Attributes: []abci.EventAttribute{
						{Key: "action", Value: "/ethermint.evm.v1.MsgEthereumTx"},
						{Key: "key", Value: "ethm17xpfvakm2amg962yls6f84z3kell8c5lthdzgl"},
						{Key: "module", Value: "evm"},
						{Key: "sender", Value: address},
					}},
				},
			},
			[]*ParsedTx{
				{
					MsgIndex:   0,
					Hash:       txHash,
					EthTxIndex: 0,
					GasUsed:    21000,
					Failed:     false,
				},
			},
		},
		{
			"from events, failed",
			abci.ExecTxResult{
				GasUsed: 21000,
				Events: []abci.Event{
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "0x01"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "amount", Value: "1000"},
						{Key: "txGasUsed", Value: "21000"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
					}},
				},
			},
			nil,
		},
		{
			"from events (hex nums), failed",
			abci.ExecTxResult{
				GasUsed: 21000,
				Events: []abci.Event{
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "10"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "amount", Value: "1000"},
						{Key: "txGasUsed", Value: "0x01"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
					}},
				},
			},
			nil,
		},
		{
			"from log field, vm execution failed",
			abci.ExecTxResult{
				Code:      15,
				Codespace: evmtypes.ModuleName,
				GasUsed:   120000,
				Events:    []abci.Event{},
				Log:       "failed to execute message; message index: 0: {\"tx_hash\":\"0x650d5204b07e83b00f489698ea55dd8259cbef658ca95723d1929a985fba8639\",\"gas_used\":120000,\"vm_error\":\"contract creation code storage out of gas\"}",
			},
			[]*ParsedTx{
				{
					MsgIndex:   0,
					Hash:       common.HexToHash("0x650d5204b07e83b00f489698ea55dd8259cbef658ca95723d1929a985fba8639"),
					EthTxIndex: EthTxIndexUnitialized,
					GasUsed:    120000,
					Failed:     true,
				},
			},
		},
		{
			"from log field, vm execution reverted",
			abci.ExecTxResult{
				Code:      3,
				Codespace: evmtypes.ModuleName,
				GasUsed:   60000,
				Events:    []abci.Event{},
				Log:       "failed to execute message; message index: 0: {\"tx_hash\":\"0x8d8997cf065cbc6dd44a6c64645633e78bcc51534cc4c11dad1e059318134cc7\",\"gas_used\":60000,\"reason\":\"user requested a revert; see event for details\",\"vm_error\":\"execution reverted\",\"ret\":\"CMN5oAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAC51c2VyIHJlcXVlc3RlZCBhIHJldmVydDsgc2VlIGV2ZW50IGZvciBkZXRhaWxzAAAAAAAAAAAAAAAAAAAAAAAA\"}",
			},
			[]*ParsedTx{
				{
					MsgIndex:   0,
					Hash:       common.HexToHash("0x8d8997cf065cbc6dd44a6c64645633e78bcc51534cc4c11dad1e059318134cc7"),
					EthTxIndex: EthTxIndexUnitialized,
					GasUsed:    60000,
					Failed:     true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseTxResult(&tc.response, nil)
			if tc.expTxs == nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				for msgIndex, expTx := range tc.expTxs {
					require.Equal(t, expTx, parsed.GetTxByMsgIndex(msgIndex))
					require.Equal(t, expTx, parsed.GetTxByHash(expTx.Hash))
					require.Equal(t, expTx, parsed.GetTxByTxIndex(int(expTx.EthTxIndex)))
				}
				// non-exists tx hash
				require.Nil(t, parsed.GetTxByHash(common.Hash{}))
				// out of range
				require.Nil(t, parsed.GetTxByMsgIndex(len(tc.expTxs)))
				require.Nil(t, parsed.GetTxByTxIndex(99999999))
			}
		})
	}
}
