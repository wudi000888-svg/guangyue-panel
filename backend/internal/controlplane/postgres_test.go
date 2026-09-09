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
