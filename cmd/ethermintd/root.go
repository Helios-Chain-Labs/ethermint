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
package main

import (
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	cmtlog "cosmossdk.io/log"
	confixcmd "cosmossdk.io/tools/confix/cmd"
	cmtcfg "github.com/cometbft/cometbft/config"
	cmtcli "github.com/cometbft/cometbft/libs/cli"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	txmodule "github.com/cosmos/cosmos-sdk/x/auth/tx/config"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	clientcfg "github.com/cosmos/cosmos-sdk/client/config"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/pruning"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	"github.com/cosmos/cosmos-sdk/client/snapshot"
	sdkserver "github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/cosmos-sdk/client/debug"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	"github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/crisis"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	rosettaCmd "github.com/cosmos/rosetta/cmd"
	"github.com/Helios-Chain-Labs/ethermint/app"
	ethermintclient "github.com/Helios-Chain-Labs/ethermint/client"
	"github.com/Helios-Chain-Labs/ethermint/crypto/hd"
	"github.com/Helios-Chain-Labs/ethermint/ethereum/eip712"
	"github.com/Helios-Chain-Labs/ethermint/server"
	servercfg "github.com/Helios-Chain-Labs/ethermint/server/config"
	srvflags "github.com/Helios-Chain-Labs/ethermint/server/flags"
	ethermint "github.com/Helios-Chain-Labs/ethermint/types"
	evmtypes "github.com/Helios-Chain-Labs/ethermint/x/evm/types"
)

const (
	EnvPrefix = "ETHERMINT"
	ChainID   = "ethermint-9000"
)

type emptyAppOptions struct{}

func (ao emptyAppOptions) Get(_ string) interface{} { return nil }

// NewRootCmd creates a new root command for simd. It is called once in the
// main function.
func NewRootCmd() (*cobra.Command, ethermint.EncodingConfig) {
	tempApp := app.NewEthermintApp(cmtlog.NewNopLogger(), dbm.NewMemDB(), nil, true, emptyAppOptions{})
	encodingConfig := tempApp.EncodingConfig()
	initClientCtx := client.Context{}.
		WithCodec(encodingConfig.Codec).
		WithInterfaceRegistry(encodingConfig.InterfaceRegistry).
		WithTxConfig(encodingConfig.TxConfig).
		WithLegacyAmino(encodingConfig.Amino).
		WithInput(os.Stdin).
		WithAccountRetriever(types.AccountRetriever{}).
		WithBroadcastMode(flags.BroadcastSync).
		WithHomeDir(app.DefaultNodeHome).
		WithKeyringOptions(hd.EthSecp256k1Option()).
		WithViper(EnvPrefix)

	eip712.SetEncodingConfig(encodingConfig)

	rootCmd := &cobra.Command{
		Use:   "ethermintd",
		Short: "Ethermint Daemon",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// set the default command outputs
			cmd.SetOut(cmd.OutOrStdout())
			cmd.SetErr(cmd.ErrOrStderr())

			initClientCtx, err := client.ReadPersistentCommandFlags(initClientCtx, cmd.Flags())
			if err != nil {
				return err
			}

			initClientCtx, err = clientcfg.ReadFromClientConfig(initClientCtx)
			if err != nil {
				return err
			}

			// This needs to go after ReadFromClientConfig, as that function
			// sets the RPC client needed for SIGN_MODE_TEXTUAL. This sign mode
			// is only available if the client is online.
			if !initClientCtx.Offline {
				txConfigOpts := tx.ConfigOptions{
					EnabledSignModes:           append(tx.DefaultSignModes, signing.SignMode_SIGN_MODE_TEXTUAL),
					TextualCoinMetadataQueryFn: txmodule.NewGRPCCoinMetadataQueryFn(initClientCtx),
				}
				txConfig, err := tx.NewTxConfigWithOptions(
					initClientCtx.Codec,
					txConfigOpts,
				)
				if err != nil {
					return err
				}

				initClientCtx = initClientCtx.WithTxConfig(txConfig)
			}
			if err := client.SetCmdClientContextHandler(initClientCtx, cmd); err != nil {
				return err
			}

			// FIXME: replace AttoPhoton with bond denom
			customAppTemplate, customAppConfig := servercfg.AppConfig(evmtypes.DefaultEVMDenom)

			return sdkserver.InterceptConfigsPreRunHandler(cmd, customAppTemplate, customAppConfig, cmtcfg.DefaultConfig())
		},
	}

	// TODO: double-check
	// authclient.Codec = encodingConfig.Codec

	initRootCmd(rootCmd, encodingConfig, tempApp.BasicModuleManager)
	autoCliOpts := tempApp.AutoCliOpts()
	initClientCtx, _ = clientcfg.ReadDefaultValuesFromDefaultClientConfig(initClientCtx)
	autoCliOpts.ClientCtx = initClientCtx
	if err := autoCliOpts.EnhanceRootCommand(rootCmd); err != nil {
		panic(err)
	}
	return rootCmd, encodingConfig
}

func initRootCmd(
	rootCmd *cobra.Command,
	encodingConfig ethermint.EncodingConfig,
	basicManager module.BasicManager,
) {
	cfg := sdk.GetConfig()
	cfg.Seal()

	rootCmd.AddCommand(
		ethermintclient.ValidateChainID(
			genutilcli.InitCmd(basicManager, app.DefaultNodeHome),
		),
		cmtcli.NewCompletionCmd(rootCmd, true),
		ethermintclient.NewTestnetCmd(basicManager, banktypes.GenesisBalancesIterator{}),
		debug.Cmd(),
		confixcmd.ConfigCommand(),
		pruning.Cmd(newApp, app.DefaultNodeHome),
		snapshot.Cmd(newApp),
		// this line is used by starport scaffolding # stargate/root/commands
	)

	server.AddCommands(rootCmd, server.NewDefaultStartOptions(newApp, app.DefaultNodeHome), appExport, addModuleInitFlags)

	// add keybase, auxiliary RPC, query, and tx child commands
	rootCmd.AddCommand(
		sdkserver.StatusCommand(),
		genesisCommand(encodingConfig.TxConfig, basicManager),
		queryCommand(),
		txCommand(),
		ethermintclient.KeyCommands(app.DefaultNodeHome),
	)

	rootCmd, err := srvflags.AddGlobalFlags(rootCmd)
	if err != nil {
		panic(err)
	}

	// add rosetta
	rootCmd.AddCommand(rosettaCmd.RosettaCommand(encodingConfig.InterfaceRegistry, encodingConfig.Codec))
}

// genesisCommand builds genesis-related `simd genesis` command. Users may provide application specific commands as a parameter
func genesisCommand(txConfig client.TxConfig, basicManager module.BasicManager, cmds ...*cobra.Command) *cobra.Command {
	cmd := genutilcli.Commands(txConfig, basicManager, app.DefaultNodeHome)

	for _, subCmd := range cmds {
		cmd.AddCommand(subCmd)
	}
	return cmd
}

func addModuleInitFlags(startCmd *cobra.Command) {
	crisis.AddModuleInitFlags(startCmd)
}

func queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		rpc.ValidatorCommand(),
		sdkserver.QueryBlockCmd(),
		sdkserver.QueryBlocksCmd(),
		sdkserver.QueryBlockResultsCmd(),
		authcmd.QueryTxsByEventsCmd(),
		authcmd.QueryTxCmd(),
		rpc.QueryEventForTxCmd(),
	)

	return cmd
}

func txCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		authcmd.GetSignCommand(),
		authcmd.GetSignBatchCommand(),
		authcmd.GetMultiSignCommand(),
		authcmd.GetMultiSignBatchCmd(),
		authcmd.GetValidateSignaturesCommand(),
		authcmd.GetBroadcastCommand(),
		authcmd.GetEncodeCommand(),
		authcmd.GetDecodeCommand(),
		authcmd.GetSimulateCmd(),
	)

	return cmd
}

// newApp creates the application
func newApp(logger cmtlog.Logger, db dbm.DB, traceStore io.Writer, appOpts servertypes.AppOptions) servertypes.Application {
	baseappOptions := sdkserver.DefaultBaseappOptions(appOpts)
	ethermintApp := app.NewEthermintApp(
		logger, db, traceStore, true,
		appOpts,
		baseappOptions...,
	)
	return ethermintApp
}

// appExport creates a new app (optionally at a given height)
// and exports state.
func appExport(
	logger cmtlog.Logger,
	db dbm.DB,
	traceStore io.Writer,
	height int64,
	forZeroHeight bool,
	jailAllowedAddrs []string,
	appOpts servertypes.AppOptions,
	modulesToExport []string,
) (servertypes.ExportedApp, error) {
	var ethermintApp *app.EthermintApp
	homePath, ok := appOpts.Get(flags.FlagHome).(string)
	if !ok || homePath == "" {
		return servertypes.ExportedApp{}, errors.New("application home not set")
	}

	if height != -1 {
		ethermintApp = app.NewEthermintApp(logger, db, traceStore, false, appOpts, baseapp.SetChainID(ChainID))

		if err := ethermintApp.LoadHeight(height); err != nil {
			return servertypes.ExportedApp{}, err
		}
	} else {
		ethermintApp = app.NewEthermintApp(logger, db, traceStore, true, appOpts, baseapp.SetChainID(ChainID))
	}

	return ethermintApp.ExportAppStateAndValidators(forZeroHeight, jailAllowedAddrs, modulesToExport)
}
