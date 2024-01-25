//go:build !test_e2e

package transfer

import (
	"context"
	"testing"

	test "github.com/strangelove-ventures/interchaintest/v8/testutil"
	testifysuite "github.com/stretchr/testify/suite"

	sdkmath "cosmossdk.io/math"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/authz"

	"github.com/cosmos/ibc-go/e2e/testsuite"
	"github.com/cosmos/ibc-go/e2e/testvalues"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	ibcerrors "github.com/cosmos/ibc-go/v8/modules/core/errors"
)

func TestAuthzTransferTestSuite(t *testing.T) {
	testifysuite.Run(t, new(AuthzTransferTestSuite))
}

type AuthzTransferTestSuite struct {
	testsuite.E2ETestSuite
}

func (s *AuthzTransferTestSuite) SetupSuite() {
	ctx := context.TODO()
	chainA, chainB := s.GetChains()
	s.SetChainsIntoSuite(chainA, chainB)
	_, _ = s.SetupRelayer(ctx, s.TransferChannelOptions(), chainA, chainB)
}

func (s *AuthzTransferTestSuite) TestAuthz_MsgTransfer_Succeeds() {
	t := s.T()
	t.Parallel()
	ctx := context.TODO()

	chainA, chainB := s.GetChains()
	relayer, channelA := s.SetupRelayer(ctx, s.TransferChannelOptions(), chainA, chainB)

	chainADenom := chainA.Config().Denom

	granterWallet := s.CreateUserOnChainA(ctx, testvalues.StartingTokenAmount)
	granterAddress := granterWallet.FormattedAddress()

	granteeWallet := s.CreateUserOnChainA(ctx, testvalues.StartingTokenAmount)
	granteeAddress := granteeWallet.FormattedAddress()

	receiverWallet := s.CreateUserOnChainB(ctx, testvalues.StartingTokenAmount)
	receiverWalletAddress := receiverWallet.FormattedAddress()

	t.Run("start relayer", func(t *testing.T) {
		s.StartRelayer(relayer)
	})

	// createMsgGrantFn initializes a TransferAuthorization and broadcasts a MsgGrant message.
	createMsgGrantFn := func(t *testing.T) {
		t.Helper()
		transferAuth := transfertypes.TransferAuthorization{
			Allocations: []transfertypes.Allocation{
				{
					SourcePort:    channelA.PortID,
					SourceChannel: channelA.ChannelID,
					SpendLimit:    sdk.NewCoins(sdk.NewCoin(chainADenom, sdkmath.NewInt(testvalues.StartingTokenAmount))),
					AllowList:     []string{receiverWalletAddress},
				},
			},
		}

		protoAny, err := codectypes.NewAnyWithValue(&transferAuth)
		s.Require().NoError(err)

		msgGrant := &authz.MsgGrant{
			Granter: granterAddress,
			Grantee: granteeAddress,
			Grant: authz.Grant{
				Authorization: protoAny,
				// no expiration
				Expiration: nil,
			},
		}

		resp := s.BroadcastMessages(context.TODO(), chainA, granterWallet, msgGrant)
		s.AssertTxSuccess(resp)
	}

	// verifyGrantFn returns a test function which asserts chainA has a grant authorization
	// with the given spend limit.
	verifyGrantFn := func(expectedLimit int64) func(t *testing.T) {
		t.Helper()
		return func(t *testing.T) {
			t.Helper()
			grantAuths, err := s.QueryGranterGrants(ctx, chainA, granterAddress)

			s.Require().NoError(err)
			s.Require().Len(grantAuths, 1)
			grantAuthorization := grantAuths[0]

			transferAuth := s.extractTransferAuthorizationFromGrantAuthorization(grantAuthorization)
			expectedSpendLimit := sdk.NewCoins(sdk.NewCoin(chainADenom, sdkmath.NewInt(expectedLimit)))
			s.Require().Equal(expectedSpendLimit, transferAuth.Allocations[0].SpendLimit)
		}
	}

	t.Run("broadcast MsgGrant", createMsgGrantFn)

	t.Run("broadcast MsgExec for ibc MsgTransfer", func(t *testing.T) {
		transferMsg := transfertypes.MsgTransfer{
			SourcePort:    channelA.PortID,
			SourceChannel: channelA.ChannelID,
			Token:         testvalues.DefaultTransferAmount(chainADenom),
			Sender:        granterAddress,
			Receiver:      receiverWalletAddress,
			TimeoutHeight: s.GetTimeoutHeight(ctx, chainB),
		}

		protoAny, err := codectypes.NewAnyWithValue(&transferMsg)
		s.Require().NoError(err)

		msgExec := &authz.MsgExec{
			Grantee: granteeAddress,
			Msgs:    []*codectypes.Any{protoAny},
		}

		resp := s.BroadcastMessages(context.TODO(), chainA, granteeWallet, msgExec)
		s.AssertTxSuccess(resp)
	})

	t.Run("verify granter wallet amount", func(t *testing.T) {
		actualBalance, err := s.GetChainANativeBalance(ctx, granterWallet)
		s.Require().NoError(err)

		expected := testvalues.StartingTokenAmount - testvalues.IBCTransferAmount
		s.Require().Equal(expected, actualBalance)
	})

	s.Require().NoError(test.WaitForBlocks(context.TODO(), 10, chainB))

	t.Run("verify receiver wallet amount", func(t *testing.T) {
		chainBIBCToken := testsuite.GetIBCToken(chainADenom, channelA.Counterparty.PortID, channelA.Counterparty.ChannelID)
		actualBalance, err := s.QueryBalance(ctx, chainB, receiverWalletAddress, chainBIBCToken.IBCDenom())

		s.Require().NoError(err)
		s.Require().Equal(testvalues.IBCTransferAmount, actualBalance.Int64())
	})

	t.Run("granter grant spend limit reduced", verifyGrantFn(testvalues.StartingTokenAmount-testvalues.IBCTransferAmount))

	t.Run("re-initialize MsgGrant", createMsgGrantFn)

	t.Run("granter grant was reinitialized", verifyGrantFn(testvalues.StartingTokenAmount))

	t.Run("revoke access", func(t *testing.T) {
		msgRevoke := authz.MsgRevoke{
			Granter:    granterAddress,
			Grantee:    granteeAddress,
			MsgTypeUrl: transfertypes.TransferAuthorization{}.MsgTypeURL(),
		}

		resp := s.BroadcastMessages(context.TODO(), chainA, granterWallet, &msgRevoke)
		s.AssertTxSuccess(resp)
	})

	t.Run("exec unauthorized MsgTransfer", func(t *testing.T) {
		transferMsg := transfertypes.MsgTransfer{
			SourcePort:    channelA.PortID,
			SourceChannel: channelA.ChannelID,
			Token:         testvalues.DefaultTransferAmount(chainADenom),
			Sender:        granterAddress,
			Receiver:      receiverWalletAddress,
			TimeoutHeight: s.GetTimeoutHeight(ctx, chainB),
		}

		protoAny, err := codectypes.NewAnyWithValue(&transferMsg)
		s.Require().NoError(err)

		msgExec := &authz.MsgExec{
			Grantee: granteeAddress,
			Msgs:    []*codectypes.Any{protoAny},
		}

		resp := s.BroadcastMessages(context.TODO(), chainA, granteeWallet, msgExec)
		s.AssertTxFailure(resp, authz.ErrNoAuthorizationFound)
	})
}

func (s *AuthzTransferTestSuite) TestAuthz_InvalidTransferAuthorizations() {
	t := s.T()
	t.Parallel()
	ctx := context.TODO()

	chainA, chainB := s.GetChains()
	relayer, channelA := s.SetupRelayer(ctx, s.TransferChannelOptions(), chainA, chainB)

	chainAVersion := chainA.Config().Images[0].Version
	chainADenom := chainA.Config().Denom

	granterWallet := s.CreateUserOnChainA(ctx, testvalues.StartingTokenAmount)
	granterAddress := granterWallet.FormattedAddress()

	granteeWallet := s.CreateUserOnChainA(ctx, testvalues.StartingTokenAmount)
	granteeAddress := granteeWallet.FormattedAddress()

	receiverWallet := s.CreateUserOnChainB(ctx, testvalues.StartingTokenAmount)
	receiverWalletAddress := receiverWallet.FormattedAddress()

	t.Run("start relayer", func(t *testing.T) {
		s.StartRelayer(relayer)
	})

	const spendLimit = 1000

	t.Run("broadcast MsgGrant", func(t *testing.T) {
		transferAuth := transfertypes.TransferAuthorization{
			Allocations: []transfertypes.Allocation{
				{
					SourcePort:    channelA.PortID,
					SourceChannel: channelA.ChannelID,
					SpendLimit:    sdk.NewCoins(sdk.NewCoin(chainADenom, sdkmath.NewInt(spendLimit))),
					AllowList:     []string{receiverWalletAddress},
				},
			},
		}

		protoAny, err := codectypes.NewAnyWithValue(&transferAuth)
		s.Require().NoError(err)

		msgGrant := &authz.MsgGrant{
			Granter: granterAddress,
			Grantee: granteeAddress,
			Grant: authz.Grant{
				Authorization: protoAny,
				// no expiration
				Expiration: nil,
			},
		}

		resp := s.BroadcastMessages(context.TODO(), chainA, granterWallet, msgGrant)
		s.AssertTxSuccess(resp)
	})

	t.Run("exceed spend limit", func(t *testing.T) {
		const invalidSpendAmount = spendLimit + 1

		t.Run("broadcast MsgExec for ibc MsgTransfer", func(t *testing.T) {
			transferMsg := transfertypes.MsgTransfer{
				SourcePort:    channelA.PortID,
				SourceChannel: channelA.ChannelID,
				Token:         sdk.Coin{Denom: chainADenom, Amount: sdkmath.NewInt(invalidSpendAmount)},
				Sender:        granterAddress,
				Receiver:      receiverWalletAddress,
				TimeoutHeight: s.GetTimeoutHeight(ctx, chainB),
			}

			protoAny, err := codectypes.NewAnyWithValue(&transferMsg)
			s.Require().NoError(err)

			msgExec := &authz.MsgExec{
				Grantee: granteeAddress,
				Msgs:    []*codectypes.Any{protoAny},
			}

			resp := s.BroadcastMessages(context.TODO(), chainA, granteeWallet, msgExec)
			if testvalues.IbcErrorsFeatureReleases.IsSupported(chainAVersion) {
				s.AssertTxFailure(resp, ibcerrors.ErrInsufficientFunds)
			} else {
				s.AssertTxFailure(resp, sdkerrors.ErrInsufficientFunds)
			}
		})

		t.Run("verify granter wallet amount", func(t *testing.T) {
			actualBalance, err := s.GetChainANativeBalance(ctx, granterWallet)
			s.Require().NoError(err)
			s.Require().Equal(testvalues.StartingTokenAmount, actualBalance)
		})

		t.Run("verify receiver wallet amount", func(t *testing.T) {
			chainBIBCToken := testsuite.GetIBCToken(chainADenom, channelA.Counterparty.PortID, channelA.Counterparty.ChannelID)
			actualBalance, err := s.QueryBalance(ctx, chainB, receiverWalletAddress, chainBIBCToken.IBCDenom())

			s.Require().NoError(err)
			s.Require().Equal(int64(0), actualBalance.Int64())
		})

		t.Run("granter grant spend limit unchanged", func(t *testing.T) {
			grantAuths, err := s.QueryGranterGrants(ctx, chainA, granterAddress)

			s.Require().NoError(err)
			s.Require().Len(grantAuths, 1)
			grantAuthorization := grantAuths[0]

			transferAuth := s.extractTransferAuthorizationFromGrantAuthorization(grantAuthorization)
			expectedSpendLimit := sdk.NewCoins(sdk.NewCoin(chainADenom, sdkmath.NewInt(spendLimit)))
			s.Require().Equal(expectedSpendLimit, transferAuth.Allocations[0].SpendLimit)
		})
	})

	t.Run("send funds to invalid address", func(t *testing.T) {
		invalidWallet := s.CreateUserOnChainB(ctx, testvalues.StartingTokenAmount)
		invalidWalletAddress := invalidWallet.FormattedAddress()

		t.Run("broadcast MsgExec for ibc MsgTransfer", func(t *testing.T) {
			transferMsg := transfertypes.MsgTransfer{
				SourcePort:    channelA.PortID,
				SourceChannel: channelA.ChannelID,
				Token:         sdk.Coin{Denom: chainADenom, Amount: sdkmath.NewInt(spendLimit)},
				Sender:        granterAddress,
				Receiver:      invalidWalletAddress,
				TimeoutHeight: s.GetTimeoutHeight(ctx, chainB),
			}

			protoAny, err := codectypes.NewAnyWithValue(&transferMsg)
			s.Require().NoError(err)

			msgExec := &authz.MsgExec{
				Grantee: granteeAddress,
				Msgs:    []*codectypes.Any{protoAny},
			}

			resp := s.BroadcastMessages(context.TODO(), chainA, granteeWallet, msgExec)
			if testvalues.IbcErrorsFeatureReleases.IsSupported(chainAVersion) {
				s.AssertTxFailure(resp, ibcerrors.ErrInvalidAddress)
			} else {
				s.AssertTxFailure(resp, sdkerrors.ErrInvalidAddress)
			}
		})
	})
}

// extractTransferAuthorizationFromGrantAuthorization extracts a TransferAuthorization from the given
// GrantAuthorization.
func (s *AuthzTransferTestSuite) extractTransferAuthorizationFromGrantAuthorization(grantAuth *authz.GrantAuthorization) *transfertypes.TransferAuthorization {
	cfg := testsuite.EncodingConfig()
	var authorization authz.Authorization
	err := cfg.InterfaceRegistry.UnpackAny(grantAuth.Authorization, &authorization)
	s.Require().NoError(err)

	transferAuth, ok := authorization.(*transfertypes.TransferAuthorization)
	s.Require().True(ok)
	return transferAuth
}
