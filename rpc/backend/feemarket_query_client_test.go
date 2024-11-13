package backend

import (
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/Helios-Chain-Labs/ethermint/rpc/backend/mocks"
	rpc "github.com/Helios-Chain-Labs/ethermint/rpc/types"
	feemarkettypes "github.com/Helios-Chain-Labs/ethermint/x/feemarket/types"
)

var _ feemarkettypes.QueryClient = &mocks.FeeMarketQueryClient{}

// Params
func RegisterFeeMarketParams(feeMarketClient *mocks.FeeMarketQueryClient, height int64) {
	feeMarketClient.On("Params", rpc.ContextWithHeight(height), &feemarkettypes.QueryParamsRequest{}).
		Return(&feemarkettypes.QueryParamsResponse{Params: feemarkettypes.DefaultParams()}, nil)
}

func RegisterFeeMarketParamsError(feeMarketClient *mocks.FeeMarketQueryClient, height int64) {
	feeMarketClient.On("Params", rpc.ContextWithHeight(height), &feemarkettypes.QueryParamsRequest{}).
		Return(nil, sdkerrors.ErrInvalidRequest)
}
