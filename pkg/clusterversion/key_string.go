// Code generated by "stringer"; DO NOT EDIT.

package clusterversion

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[V21_2-0]
	_ = x[Start22_1-1]
	_ = x[TargetBytesAvoidExcess-2]
	_ = x[AvoidDrainingNames-3]
	_ = x[DrainingNamesMigration-4]
	_ = x[TraceIDDoesntImplyStructuredRecording-5]
	_ = x[AlterSystemTableStatisticsAddAvgSizeCol-6]
	_ = x[AlterSystemStmtDiagReqs-7]
	_ = x[MVCCAddSSTable-8]
	_ = x[InsertPublicSchemaNamespaceEntryOnRestore-9]
	_ = x[UnsplitRangesInAsyncGCJobs-10]
	_ = x[ValidateGrantOption-11]
	_ = x[PebbleFormatBlockPropertyCollector-12]
	_ = x[ProbeRequest-13]
	_ = x[SelectRPCsTakeTracingInfoInband-14]
	_ = x[PreSeedTenantSpanConfigs-15]
	_ = x[SeedTenantSpanConfigs-16]
	_ = x[PublicSchemasWithDescriptors-17]
	_ = x[EnsureSpanConfigReconciliation-18]
	_ = x[EnsureSpanConfigSubscription-19]
	_ = x[EnableSpanConfigStore-20]
	_ = x[ScanWholeRows-21]
	_ = x[SCRAMAuthentication-22]
	_ = x[UnsafeLossOfQuorumRecoveryRangeLog-23]
	_ = x[AlterSystemProtectedTimestampAddColumn-24]
	_ = x[EnableProtectedTimestampsForTenant-25]
	_ = x[DeleteCommentsWithDroppedIndexes-26]
	_ = x[RemoveIncompatibleDatabasePrivileges-27]
	_ = x[AddRaftAppliedIndexTermMigration-28]
	_ = x[PostAddRaftAppliedIndexTermMigration-29]
	_ = x[DontProposeWriteTimestampForLeaseTransfers-30]
	_ = x[TenantSettingsTable-31]
	_ = x[EnablePebbleFormatVersionBlockProperties-32]
	_ = x[DisableSystemConfigGossipTrigger-33]
	_ = x[MVCCIndexBackfiller-34]
	_ = x[EnableLeaseHolderRemoval-35]
	_ = x[BackupResolutionInJob-36]
	_ = x[LooselyCoupledRaftLogTruncation-37]
	_ = x[ChangefeedIdleness-38]
	_ = x[BackupDoesNotOverwriteLatestAndCheckpoint-39]
	_ = x[EnableDeclarativeSchemaChanger-40]
	_ = x[RowLevelTTL-41]
	_ = x[PebbleFormatSplitUserKeysMarked-42]
	_ = x[IncrementalBackupSubdir-43]
	_ = x[DateStyleIntervalStyleCastRewrite-44]
	_ = x[EnableNewStoreRebalancer-45]
	_ = x[ClusterLocksVirtualTable-46]
	_ = x[AutoStatsTableSettings-47]
	_ = x[ForecastStats-48]
	_ = x[SuperRegions-49]
	_ = x[EnableNewChangefeedOptions-50]
	_ = x[SpanCountTable-51]
	_ = x[PreSeedSpanCountTable-52]
	_ = x[SeedSpanCountTable-53]
	_ = x[V22_1-54]
	_ = x[Start22_2-55]
	_ = x[LocalTimestamps-56]
	_ = x[EnsurePebbleFormatVersionRangeKeys-57]
	_ = x[EnablePebbleFormatVersionRangeKeys-58]
	_ = x[TrigramInvertedIndexes-59]
	_ = x[RemoveGrantPrivilege-60]
	_ = x[MVCCRangeTombstones-61]
	_ = x[UpgradeSequenceToBeReferencedByID-62]
}

const _Key_name = "V21_2Start22_1TargetBytesAvoidExcessAvoidDrainingNamesDrainingNamesMigrationTraceIDDoesntImplyStructuredRecordingAlterSystemTableStatisticsAddAvgSizeColAlterSystemStmtDiagReqsMVCCAddSSTableInsertPublicSchemaNamespaceEntryOnRestoreUnsplitRangesInAsyncGCJobsValidateGrantOptionPebbleFormatBlockPropertyCollectorProbeRequestSelectRPCsTakeTracingInfoInbandPreSeedTenantSpanConfigsSeedTenantSpanConfigsPublicSchemasWithDescriptorsEnsureSpanConfigReconciliationEnsureSpanConfigSubscriptionEnableSpanConfigStoreScanWholeRowsSCRAMAuthenticationUnsafeLossOfQuorumRecoveryRangeLogAlterSystemProtectedTimestampAddColumnEnableProtectedTimestampsForTenantDeleteCommentsWithDroppedIndexesRemoveIncompatibleDatabasePrivilegesAddRaftAppliedIndexTermMigrationPostAddRaftAppliedIndexTermMigrationDontProposeWriteTimestampForLeaseTransfersTenantSettingsTableEnablePebbleFormatVersionBlockPropertiesDisableSystemConfigGossipTriggerMVCCIndexBackfillerEnableLeaseHolderRemovalBackupResolutionInJobLooselyCoupledRaftLogTruncationChangefeedIdlenessBackupDoesNotOverwriteLatestAndCheckpointEnableDeclarativeSchemaChangerRowLevelTTLPebbleFormatSplitUserKeysMarkedIncrementalBackupSubdirDateStyleIntervalStyleCastRewriteEnableNewStoreRebalancerClusterLocksVirtualTableAutoStatsTableSettingsForecastStatsSuperRegionsEnableNewChangefeedOptionsSpanCountTablePreSeedSpanCountTableSeedSpanCountTableV22_1Start22_2LocalTimestampsEnsurePebbleFormatVersionRangeKeysEnablePebbleFormatVersionRangeKeysTrigramInvertedIndexesRemoveGrantPrivilegeMVCCRangeTombstonesUpgradeSequenceToBeReferencedByID"

var _Key_index = [...]uint16{0, 5, 14, 36, 54, 76, 113, 152, 175, 189, 230, 256, 275, 309, 321, 352, 376, 397, 425, 455, 483, 504, 517, 536, 570, 608, 642, 674, 710, 742, 778, 820, 839, 879, 911, 930, 954, 975, 1006, 1024, 1065, 1095, 1106, 1137, 1160, 1193, 1217, 1241, 1263, 1276, 1288, 1314, 1328, 1349, 1367, 1372, 1381, 1396, 1430, 1464, 1486, 1506, 1525, 1558}

func (i Key) String() string {
	if i < 0 || i >= Key(len(_Key_index)-1) {
		return "Key(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Key_name[_Key_index[i]:_Key_index[i+1]]
}
