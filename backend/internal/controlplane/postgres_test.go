package controlplane

import (
	"os"
	"testing"
)

// Run the same externally visible business invariants against real PostgreSQL.
// SQLite trigger fault-injection tests remain in the normal Lite suite.
func TestPostgresBehaviors(t *testing.T) {
	if os.Getenv("GY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL integration not configured")
	}
	for _, v := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"commerce_snapshot", TestCommerceSnapshotRoundTrip},
		{"commerce_recovery", TestCommerceRecoveryAndRenewal},
		{"commerce_integrity", TestCommerceIntegrityAndArchive},
		{"commerce_images", TestSupportInternalImagesAndRetention},
		{"commerce_redemption", TestCommerceRedemptionAtomicAndRevocation},
		{"commerce_orders", TestCommerceOrderCaptureResetAndRefund},
		{"commerce_cancellation", TestCommerceCancellationIsolationAndExpiry},
		{"commerce_support", TestSupportTicketImagesAndOwnership},
		{"business_lifecycle", TestBusinessLifecycle},
		{"direct_subsites", TestDirectSubsiteLifecycle},
		{"mounted_isolation", TestMountedNodeIsolationAndSubscriptions},
		{"mounted_accounting", TestMountedAccountingRevocationAndPeriods},
		{"mounted_sharing", TestMountedTokenSharingAndLease},
		{"subsite_default_group", TestSubsiteGroupInstallUpgradeAndCustomization},
		{"mounted_auto_access", TestAutomaticMountFollowsMainGroupsAndRevokes},
		{"mounted_auto_budget", TestAutomaticMountBudgetsRespectOtherSitesAndOfflineReservations},
		{"mounted_auto_compatibility", TestAutomaticMountManualCompatibilityAndInactiveUsers},
		{"offline_directory", TestLegacyOfflineDirectoryMigration},
		{"site_removal", TestRemovedLegacySiteRetainsUnsettledAccounting},
		{"site_policy_conversion", TestConvertedPoliciesPreserveNodeAccess},
		{"site_identity", TestMultipleDefaultSitesAndSelfImport},
		{"subsite_permissions", TestMasterControlsLiteSubsitePermissions},
		{"weighted_business", TestBusinessWeightedQuotasSparseAcknowledgementAndReset},
		{"business_membership_deletion", TestBusinessExitDeletionCleansGroupsAndRestoresOnFailure},
		{"entitlements", TestEntitlementSnapshotsRenewResetAndAccess},
		{"direct_node_access", TestIndependentNodeAccessAndPlanAuthority},
		{"plan_mount_access", TestPlanMountAccessWithoutUserAssignment},
		{"direct_node_validation", TestDirectNodeAccessPreviewAndAuthorization},
		{"direct_node_mounts", TestDirectNodeAccessMountsBothProtocolsAndRevokes},
		{"group_member_identity", TestNodeGroupMembersDistinguishProtocolAndSite},
		{"entitlement_migration", TestEntitlementMigrationAndUsageIsolation},
		{"entitlement_edit", TestRejectedEntitlementEditPreservesSessionsAndNewUserMeter},
		{"node_rates", TestNodeMeterRatesChangeDeletionAndNoLogs},
		{"membership_deletion", TestNodeDeleteRemovesGroupAndPreservesUsage},
		{"business_quota", TestBusinessAllocationsAndCounterRollback},
		{"shared_quality", TestDirectQualitySharedAcrossProtocols},
		{"authentication", TestUserIsolationAndCSRF},
		{"traffic_checkpoints", TestTrafficCheckpointRestartAndQuota},
		{"no_logs_accounting", TestRuntimeNoLogsPreservesAccountingAndExistingHistory},
		{"subscription_rotation", TestSubscriptionsAndCredentialRotation},
		{"pool_encryption", TestPoolAuthorizationEncryptionAndDuplicateProtection},
		{"source_cascade", TestSourceDeletionCascadesOwnedExitsNodesAndCredentials},
		{"shared_ownership", TestSourceDeletionReassignsSharedOwnershipWithoutManualConversion},
		{"subscription_isolation", TestPrivateAndPublicSubscriptionsHaveIndependentURLsContentsAndRotation},
		{"message_isolation", TestMessageRecipientIsolationAndReadState},
		{"default_nodes", TestDefaultDirectAPIRejectsDeletionDisableAndRebinding},
		{"multiple_hy2", TestMultipleHY2NodesDefaultDirectAndIndependentCredentials},
		{"node_save_receipts", TestNodeSaveReceiptReplay},
		{"node_save_rollback", TestNodeSaveReceiptRollbackAndValidation},
		{"node_save_concurrent", TestConcurrentNodeCreationUsesOneReceipt},
		{"stale_subscription", TestPartialAndInvalidSourceResponseDoesNotPrune},
	} {
		t.Run(v.name, v.run)
	}
}
