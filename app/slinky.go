package app

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	// this line is used by starport scaffolding # stargate/app/moduleImport

	stakingkeeper "github.com/initia-labs/initia/x/mstaking/keeper"

	// block-sdk dependencies

	// slinky oracle dependencies
	oraclepreblock "github.com/skip-mev/slinky/abci/preblock/oracle"
	oracleproposals "github.com/skip-mev/slinky/abci/proposals"
	"github.com/skip-mev/slinky/abci/strategies/aggregator"
	compression "github.com/skip-mev/slinky/abci/strategies/codec"
	"github.com/skip-mev/slinky/abci/strategies/currencypair"
	"github.com/skip-mev/slinky/abci/ve"
	oracleconfig "github.com/skip-mev/slinky/oracle/config"
	"github.com/skip-mev/slinky/pkg/math/voteweighted"
	oracleclient "github.com/skip-mev/slinky/service/clients/oracle"
	servicemetrics "github.com/skip-mev/slinky/service/metrics"

	// unnamed import of statik for swagger UI support
	_ "github.com/initia-labs/initia/client/docs/statik"
)

func setupSlinky(
	app *InitiaApp,
	oracleConfig oracleconfig.AppConfig,
	prepareProposalHandler sdk.PrepareProposalHandler,
	processProposalHandler sdk.ProcessProposalHandler,
) (
	oracleclient.OracleClient,
	sdk.PrepareProposalHandler,
	sdk.ProcessProposalHandler,
	sdk.PreBlocker,
	sdk.ExtendVoteHandler,
	sdk.VerifyVoteExtensionHandler,
	error,
) {
	serviceMetrics, err := servicemetrics.NewMetricsFromConfig(
		oracleConfig,
		app.ChainID(),
	)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	oracleClient, err := oracleclient.NewPriceDaemonClientFromConfig(
		oracleConfig,
		app.Logger().With("client", "oracle"),
		serviceMetrics,
	)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	oracleProposalHandler := oracleproposals.NewProposalHandler(
		app.Logger(),
		prepareProposalHandler,
		processProposalHandler,
		ve.NewDefaultValidateVoteExtensionsFn(
			stakingkeeper.NewCompatibilityKeeper(app.StakingKeeper),
		),
		compression.NewCompressionVoteExtensionCodec(
			compression.NewDefaultVoteExtensionCodec(),
			compression.NewZLibCompressor(),
		),
		compression.NewCompressionExtendedCommitCodec(
			compression.NewDefaultExtendedCommitCodec(),
			compression.NewZStdCompressor(),
		),
		currencypair.NewHashCurrencyPairStrategy(app.OracleKeeper),
		serviceMetrics,
	)

	prepareProposalHandler = oracleProposalHandler.PrepareProposalHandler()
	processProposalHandler = oracleProposalHandler.ProcessProposalHandler()

	// Create the aggregation function that will be used to aggregate oracle data
	// from each validator.
	aggregatorFn := voteweighted.MedianFromContext(
		app.Logger(),
		stakingkeeper.NewCompatibilityKeeper(app.StakingKeeper),
		voteweighted.DefaultPowerThreshold,
	)

	preBlocker := oraclepreblock.NewOraclePreBlockHandler(
		app.Logger(),
		aggregatorFn,
		app.OracleKeeper,
		serviceMetrics,
		currencypair.NewHashCurrencyPairStrategy(app.OracleKeeper),
		compression.NewCompressionVoteExtensionCodec(
			compression.NewDefaultVoteExtensionCodec(),
			compression.NewZLibCompressor(),
		),
		compression.NewCompressionExtendedCommitCodec(
			compression.NewDefaultExtendedCommitCodec(),
			compression.NewZStdCompressor(),
		),
	).WrappedPreBlocker(app.ModuleManager)

	// Create the vote extensions handler that will be used to extend and verify
	// vote extensions (i.e. oracle data).
	veCodec := compression.NewCompressionVoteExtensionCodec(
		compression.NewDefaultVoteExtensionCodec(),
		compression.NewZLibCompressor(),
	)
	extCommitCodec := compression.NewCompressionExtendedCommitCodec(
		compression.NewDefaultExtendedCommitCodec(),
		compression.NewZStdCompressor(),
	)

	// Create the vote extensions handler that will be used to extend and verify
	// vote extensions (i.e. oracle data).
	voteExtensionsHandler := ve.NewVoteExtensionHandler(
		app.Logger(),
		oracleClient,
		time.Second,
		currencypair.NewHashCurrencyPairStrategy(app.OracleKeeper),
		veCodec,
		aggregator.NewOraclePriceApplier(
			aggregator.NewDefaultVoteAggregator(
				app.Logger(),
				aggregatorFn,
				// we need a separate price strategy here, so that we can optimistically apply the latest prices
				// and extend our vote based on these prices
				currencypair.NewHashCurrencyPairStrategy(app.OracleKeeper),
			),
			app.OracleKeeper,
			veCodec,
			extCommitCodec,
			app.Logger(),
		),
		serviceMetrics,
	)

	extendedVoteHandler := voteExtensionsHandler.ExtendVoteHandler()
	verifyVoteExtensionHandler := voteExtensionsHandler.VerifyVoteExtensionHandler()

	return oracleClient, prepareProposalHandler, processProposalHandler, preBlocker, extendedVoteHandler, verifyVoteExtensionHandler, nil
}