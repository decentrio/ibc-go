package keeper_test

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v8/modules/core/23-commitment/types"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
)

func (suite *KeeperTestSuite) TestMsgCreateClientEvents() {
	suite.SetupTest()
	path := ibctesting.NewPath(suite.chainA, suite.chainB)

	path.EndpointA.Counterparty.Chain.NextBlock()

	tmConfig, ok := path.EndpointA.ClientConfig.(*ibctesting.TendermintConfig)
	suite.Require().True(ok)

	height := path.EndpointA.Counterparty.Chain.LatestCommittedHeader.GetHeight().(clienttypes.Height)
	clientState := ibctm.NewClientState(
		path.EndpointA.Counterparty.Chain.ChainID, tmConfig.TrustLevel, tmConfig.TrustingPeriod, tmConfig.UnbondingPeriod, tmConfig.MaxClockDrift,
		height, commitmenttypes.GetSDKSpecs(), ibctesting.UpgradePath)
	consensusState := path.EndpointA.Counterparty.Chain.LatestCommittedHeader.ConsensusState()

	msg, err := clienttypes.NewMsgCreateClient(
		clientState, consensusState, path.EndpointA.Chain.SenderAccount.GetAddress().String(),
	)
	suite.Require().NoError(err)

	res, err := suite.chainA.SendMsgs(msg)
	suite.Require().NoError(err)
	suite.Require().NotNil(res)

	events := res.Events
	expectedEvents := sdk.Events{
		sdk.NewEvent(
			clienttypes.EventTypeCreateClient,
			sdk.NewAttribute(clienttypes.AttributeKeyClientID, ibctesting.FirstClientID),
			sdk.NewAttribute(clienttypes.AttributeKeyClientType, clientState.ClientType()),
			sdk.NewAttribute(clienttypes.AttributeKeyConsensusHeight, clientState.GetLatestHeight().String()),
		),
	}.ToABCIEvents()

	var indexSet map[string]struct{}
	expectedEvents = sdk.MarkEventsToIndex(expectedEvents, indexSet)
	ibctesting.AssertEvents(&suite.Suite, expectedEvents, events)
}

func (suite *KeeperTestSuite) TestMsgUpdateClientEvents() {
	suite.SetupTest()
	path := ibctesting.NewPath(suite.chainA, suite.chainB)

	suite.Require().NoError(path.EndpointA.CreateClient())

	suite.chainB.Coordinator.CommitBlock(suite.chainB)

	header, err := suite.chainA.ConstructUpdateTMClientHeader(suite.chainB, ibctesting.FirstClientID)
	suite.Require().NoError(err)
	suite.Require().NotNil(header)

	msg, err := clienttypes.NewMsgUpdateClient(
		ibctesting.FirstClientID, header,
		path.EndpointA.Chain.SenderAccount.GetAddress().String(),
	)

	suite.Require().NoError(err)

	res, err := suite.chainA.SendMsgs(msg)
	suite.Require().NoError(err)
	suite.Require().NotNil(res)

	events := res.Events
	expectedEvents := sdk.Events{
		sdk.NewEvent(
			clienttypes.EventTypeUpdateClient,
			sdk.NewAttribute(clienttypes.AttributeKeyClientID, ibctesting.FirstClientID),
			sdk.NewAttribute(clienttypes.AttributeKeyClientType, path.EndpointA.GetClientState().ClientType()),
			sdk.NewAttribute(clienttypes.AttributeKeyConsensusHeight, path.EndpointA.GetClientState().GetLatestHeight().String()),
			sdk.NewAttribute(clienttypes.AttributeKeyConsensusHeights, path.EndpointA.GetClientState().GetLatestHeight().String()),
		),
	}.ToABCIEvents()

	var indexSet map[string]struct{}
	expectedEvents = sdk.MarkEventsToIndex(expectedEvents, indexSet)
	ibctesting.AssertEvents(&suite.Suite, expectedEvents, events)
}
