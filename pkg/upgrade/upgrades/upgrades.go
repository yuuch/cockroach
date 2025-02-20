// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

// Package upgrades contains the implementation of upgrades. It is imported
// by the server library.
//
// This package registers the upgrades with the upgrade package.
package upgrades

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/clusterversion"
	"github.com/cockroachdb/cockroach/pkg/upgrade"
	"github.com/cockroachdb/errors"
)

// GetUpgrade returns the upgrade corresponding to this version if
// one exists.
func GetUpgrade(key clusterversion.ClusterVersion) (upgrade.Upgrade, bool) {
	m, ok := registry[key]
	return m, ok
}

// NoPrecondition is a PreconditionFunc that doesn't check anything.
func NoPrecondition(context.Context, clusterversion.ClusterVersion, upgrade.TenantDeps) error {
	return nil
}

// registry defines the global mapping between a cluster version and the
// associated upgrade. The upgrade is only executed after a cluster-wide
// bump of the corresponding version gate.
var registry = make(map[clusterversion.ClusterVersion]upgrade.Upgrade)

var upgrades = []upgrade.Upgrade{
	upgrade.NewTenantUpgrade(
		"ensure that draining names are no longer in use",
		toCV(clusterversion.DrainingNamesMigration),
		NoPrecondition,
		ensureNoDrainingNames,
	),
	upgrade.NewTenantUpgrade(
		"add column avgSize to table system.table_statistics",
		toCV(clusterversion.AlterSystemTableStatisticsAddAvgSizeCol),
		NoPrecondition,
		alterSystemTableStatisticsAddAvgSize,
	),
	upgrade.NewTenantUpgrade(
		"update system.statement_diagnostics_requests table to support conditional stmt diagnostics",
		toCV(clusterversion.AlterSystemStmtDiagReqs),
		NoPrecondition,
		alterSystemStmtDiagReqs,
	),
	upgrade.NewTenantUpgrade(
		"seed system.span_configurations with configs for existing tenants",
		toCV(clusterversion.SeedTenantSpanConfigs),
		NoPrecondition,
		seedTenantSpanConfigsMigration,
	),
	upgrade.NewTenantUpgrade("insert missing system.namespace entries for public schemas",
		toCV(clusterversion.InsertPublicSchemaNamespaceEntryOnRestore),
		NoPrecondition,
		insertMissingPublicSchemaNamespaceEntry,
	),
	upgrade.NewTenantUpgrade(
		"add column target to system.protected_ts_records",
		toCV(clusterversion.AlterSystemProtectedTimestampAddColumn),
		NoPrecondition,
		alterTableProtectedTimestampRecords,
	),
	upgrade.NewTenantUpgrade("update synthetic public schemas to be backed by a descriptor",
		toCV(clusterversion.PublicSchemasWithDescriptors),
		NoPrecondition,
		publicSchemaMigration,
	),
	upgrade.NewTenantUpgrade(
		"enable span configs infrastructure",
		toCV(clusterversion.EnsureSpanConfigReconciliation),
		NoPrecondition,
		ensureSpanConfigReconciliation,
	),
	upgrade.NewSystemUpgrade(
		"enable span configs infrastructure",
		toCV(clusterversion.EnsureSpanConfigSubscription),
		ensureSpanConfigSubscription,
	),
	upgrade.NewTenantUpgrade(
		"remove grant privilege from users",
		toCV(clusterversion.RemoveGrantPrivilege),
		NoPrecondition,
		removeGrantMigration,
	),
	upgrade.NewTenantUpgrade(
		"delete comments that belong to dropped indexes",
		toCV(clusterversion.DeleteCommentsWithDroppedIndexes),
		NoPrecondition,
		ensureCommentsHaveNonDroppedIndexes,
	),
	upgrade.NewSystemUpgrade(
		"populate RangeAppliedState.RaftAppliedIndexTerm for all ranges",
		toCV(clusterversion.AddRaftAppliedIndexTermMigration),
		raftAppliedIndexTermMigration,
	),
	upgrade.NewSystemUpgrade(
		"purge all replicas not populating RangeAppliedState.RaftAppliedIndexTerm",
		toCV(clusterversion.PostAddRaftAppliedIndexTermMigration),
		postRaftAppliedIndexTermMigration,
	),
	upgrade.NewTenantUpgrade(
		"add the system.tenant_settings table",
		toCV(clusterversion.TenantSettingsTable),
		NoPrecondition,
		tenantSettingsTableMigration,
	),
	upgrade.NewTenantUpgrade(
		"add the system.span_count table",
		toCV(clusterversion.SpanCountTable),
		NoPrecondition,
		spanCountTableMigration,
	),
	upgrade.NewTenantUpgrade(
		"seed system.span_count with span count for existing tenants",
		toCV(clusterversion.SeedSpanCountTable),
		NoPrecondition,
		seedSpanCountTableMigration,
	),
	upgrade.NewTenantUpgrade(
		"upgrade sequences to be referenced by ID",
		toCV(clusterversion.UpgradeSequenceToBeReferencedByID),
		NoPrecondition,
		upgradeSequenceToBeReferencedByID,
	),
}

func init() {
	for _, m := range upgrades {
		if _, exists := registry[m.ClusterVersion()]; exists {
			panic(errors.AssertionFailedf("duplicate upgrade registration for %v", m.ClusterVersion()))
		}
		registry[m.ClusterVersion()] = m
	}
}

func toCV(key clusterversion.Key) clusterversion.ClusterVersion {
	return clusterversion.ClusterVersion{
		Version: clusterversion.ByKey(key),
	}
}
