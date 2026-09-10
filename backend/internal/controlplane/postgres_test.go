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
		{"weighted_business", TestBusinessWeightedQuotasSparseAcknowledgementAndReset},
		{"business_membership_deletion", TestBusinessExitDeletionCleansGroupsAndRestoresOnFailure},
		{"entitlements", TestEntitlementSnapshotsRenewResetAndAccess},
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
		{"stale_subscription", TestPartialAndInvalidSourceResponseDoesNotPrune},
	} {
		t.Run(v.name, v.run)
	}
}
