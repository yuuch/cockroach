// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

// Package tablestorageparam implements storageparam.Setter for
// tabledesc.Mutable.
package tablestorageparam

import (
	"context"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/settings"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/catpb"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/tabledesc"
	"github.com/cockroachdb/cockroach/pkg/sql/paramparse"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgnotice"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/eval"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/storageparam"
	"github.com/cockroachdb/cockroach/pkg/util/duration"
	"github.com/cockroachdb/cockroach/pkg/util/errorutil/unimplemented"
	"github.com/cockroachdb/errors"
)

// Setter observes storage parameters for tables.
type Setter struct {
	tableDesc          *tabledesc.Mutable
	setAutomaticColumn bool
}

var _ storageparam.Setter = (*Setter)(nil)

// NewSetter returns a new Setter.
func NewSetter(tableDesc *tabledesc.Mutable) *Setter {
	return &Setter{tableDesc: tableDesc}
}

// RunPostChecks implements the Setter interface.
func (po *Setter) RunPostChecks() error {
	ttl := po.tableDesc.GetRowLevelTTL()
	if po.setAutomaticColumn && (ttl == nil || ttl.DurationExpr == "") {
		return pgerror.Newf(
			pgcode.InvalidParameterValue,
			`"ttl_expire_after" must be set if "ttl_automatic_column" is set`,
		)
	}
	if err := tabledesc.ValidateRowLevelTTL(ttl); err != nil {
		return err
	}
	return nil
}

func boolFromDatum(evalCtx *eval.Context, key string, datum tree.Datum) (bool, error) {
	if stringVal, err := paramparse.DatumAsString(evalCtx, key, datum); err == nil {
		return paramparse.ParseBoolVar(key, stringVal)
	}
	s, err := paramparse.GetSingleBool(key, datum)
	if err != nil {
		return false, err
	}
	return bool(*s), nil
}

func intFromDatum(evalCtx *eval.Context, key string, datum tree.Datum) (int64, error) {
	intDatum := datum
	if stringVal, err := paramparse.DatumAsString(evalCtx, key, datum); err == nil {
		if intDatum, err = tree.ParseDInt(stringVal); err != nil {
			return 0, errors.Wrapf(err, "invalid integer value for %s", key)
		}
	}
	s, err := paramparse.DatumAsInt(evalCtx, key, intDatum)
	if err != nil {
		return 0, err
	}
	return s, nil
}

func floatFromDatum(evalCtx *eval.Context, key string, datum tree.Datum) (float64, error) {
	floatDatum := datum
	if stringVal, err := paramparse.DatumAsString(evalCtx, key, datum); err == nil {
		if floatDatum, err = tree.ParseDFloat(stringVal); err != nil {
			return 0, errors.Wrapf(err, "invalid float value for %s", key)
		}
	}
	s, err := paramparse.DatumAsFloat(evalCtx, key, floatDatum)
	if err != nil {
		return 0, err
	}
	return s, nil
}

type tableParam struct {
	onSet   func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error
	onReset func(po *Setter, evalCtx *eval.Context, key string) error
}

var tableParams = map[string]tableParam{
	`fillfactor`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			return storageparam.SetFillFactor(evalCtx, key, datum)
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			// Operation is a no-op so do nothing.
			return nil
		},
	},
	`autovacuum_enabled`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			var boolVal bool
			if stringVal, err := paramparse.DatumAsString(evalCtx, key, datum); err == nil {
				boolVal, err = paramparse.ParseBoolVar(key, stringVal)
				if err != nil {
					return err
				}
			} else {
				s, err := paramparse.GetSingleBool(key, datum)
				if err != nil {
					return err
				}
				boolVal = bool(*s)
			}
			if !boolVal && evalCtx != nil {
				evalCtx.ClientNoticeSender.BufferClientNotice(
					evalCtx.Context,
					pgnotice.Newf(`storage parameter "%s = %s" is ignored`, key, datum.String()),
				)
			}
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			// Operation is a no-op so do nothing.
			return nil
		},
	},
	`ttl`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			setTrue, err := boolFromDatum(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if setTrue && po.tableDesc.RowLevelTTL == nil {
				// Set the base struct, but do not populate it.
				// An error from runPostChecks will appear if the requisite fields are not set.
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			if !setTrue && po.tableDesc.RowLevelTTL != nil {
				po.tableDesc.RowLevelTTL = nil
			}
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			po.tableDesc.RowLevelTTL = nil
			return nil
		},
	},
	`ttl_automatic_column`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			setTrue, err := boolFromDatum(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if setTrue {
				po.setAutomaticColumn = true
			}
			if !setTrue && po.tableDesc.RowLevelTTL != nil {
				return unimplemented.NewWithIssue(76916, "unsetting TTL automatic column not yet implemented")
			}
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			return unimplemented.NewWithIssue(76916, "unsetting TTL automatic column not yet implemented")
		},
	},
	`ttl_expire_after`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			var d *tree.DInterval
			if stringVal, err := paramparse.DatumAsString(evalCtx, key, datum); err == nil {
				d, err = tree.ParseDInterval(evalCtx.SessionData().GetIntervalStyle(), stringVal)
				if err != nil || d == nil {
					return pgerror.Newf(
						pgcode.InvalidParameterValue,
						`value of "ttl_expire_after" must be an interval`,
					)
				}
			} else {
				var ok bool
				d, ok = datum.(*tree.DInterval)
				if !ok || d == nil {
					return pgerror.Newf(
						pgcode.InvalidParameterValue,
						`value of "%s" must be an interval`,
						key,
					)
				}
			}

			if d.Duration.Compare(duration.MakeDuration(0, 0, 0)) < 0 {
				return pgerror.Newf(
					pgcode.InvalidParameterValue,
					`value of "%s" must be at least zero`,
					key,
				)
			}
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			po.tableDesc.RowLevelTTL.DurationExpr = catpb.Expression(tree.Serialize(d))
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			return errors.WithHintf(
				pgerror.Newf(
					pgcode.InvalidParameterValue,
					`resetting "ttl_expire_after" is not permitted`,
				),
				"use `RESET (ttl)` to remove TTL from the table",
			)
		},
	},
	`ttl_select_batch_size`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			val, err := paramparse.DatumAsInt(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if err := tabledesc.ValidateTTLBatchSize(key, val); err != nil {
				return err
			}
			po.tableDesc.RowLevelTTL.SelectBatchSize = val
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			if po.tableDesc.RowLevelTTL != nil {
				po.tableDesc.RowLevelTTL.SelectBatchSize = 0
			}
			return nil
		},
	},
	`ttl_delete_batch_size`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			val, err := paramparse.DatumAsInt(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if err := tabledesc.ValidateTTLBatchSize(key, val); err != nil {
				return err
			}
			po.tableDesc.RowLevelTTL.DeleteBatchSize = val
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			if po.tableDesc.RowLevelTTL != nil {
				po.tableDesc.RowLevelTTL.DeleteBatchSize = 0
			}
			return nil
		},
	},
	`ttl_range_concurrency`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			val, err := paramparse.DatumAsInt(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if err := tabledesc.ValidateTTLRangeConcurrency(key, val); err != nil {
				return err
			}
			po.tableDesc.RowLevelTTL.RangeConcurrency = val
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			if po.tableDesc.RowLevelTTL != nil {
				po.tableDesc.RowLevelTTL.RangeConcurrency = 0
			}
			return nil
		},
	},
	`ttl_delete_rate_limit`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			val, err := paramparse.DatumAsInt(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if err := tabledesc.ValidateTTLRateLimit(key, val); err != nil {
				return err
			}
			po.tableDesc.RowLevelTTL.DeleteRateLimit = val
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			if po.tableDesc.RowLevelTTL != nil {
				po.tableDesc.RowLevelTTL.DeleteRateLimit = 0
			}
			return nil
		},
	},
	`ttl_label_metrics`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			val, err := boolFromDatum(evalCtx, key, datum)
			if err != nil {
				return err
			}
			po.tableDesc.RowLevelTTL.LabelMetrics = val
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			po.tableDesc.RowLevelTTL.LabelMetrics = false
			return nil
		},
	},
	`ttl_job_cron`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			str, err := paramparse.DatumAsString(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if err := tabledesc.ValidateTTLCronExpr(key, str); err != nil {
				return err
			}
			po.tableDesc.RowLevelTTL.DeletionCron = str
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			if po.tableDesc.RowLevelTTL != nil {
				po.tableDesc.RowLevelTTL.DeletionCron = ""
			}
			return nil
		},
	},
	`ttl_pause`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			b, err := boolFromDatum(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			po.tableDesc.RowLevelTTL.Pause = b
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			po.tableDesc.RowLevelTTL.Pause = false
			return nil
		},
	},
	`ttl_row_stats_poll_interval`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
			d, err := paramparse.DatumAsDuration(evalCtx, key, datum)
			if err != nil {
				return err
			}
			if po.tableDesc.RowLevelTTL == nil {
				po.tableDesc.RowLevelTTL = &catpb.RowLevelTTL{}
			}
			if err := tabledesc.ValidateTTLRowStatsPollInterval(key, d); err != nil {
				return err
			}
			po.tableDesc.RowLevelTTL.RowStatsPollInterval = d
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			po.tableDesc.RowLevelTTL.RowStatsPollInterval = 0
			return nil
		},
	},
	`exclude_data_from_backup`: {
		onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext,
			evalCtx *eval.Context, key string, datum tree.Datum) error {
			if po.tableDesc.Temporary {
				return pgerror.Newf(pgcode.FeatureNotSupported,
					"cannot set data in a temporary table to be excluded from backup")
			}

			// Check that the table does not have any incoming FK references. During a
			// backup, the rows of a table with ephemeral data will not be backed up, and
			// could result in a violation of FK constraints on restore. To prevent this,
			// we only allow a table with no incoming FK references to be marked as
			// ephemeral.
			if len(po.tableDesc.InboundFKs) != 0 {
				return errors.New("cannot set data in a table with inbound foreign key constraints to be excluded from backup")
			}

			excludeDataFromBackup, err := boolFromDatum(evalCtx, key, datum)
			if err != nil {
				return err
			}
			// If the table descriptor being changed has the same value for the
			// `ExcludeDataFromBackup` flag, no-op.
			if po.tableDesc.ExcludeDataFromBackup == excludeDataFromBackup {
				return nil
			}
			po.tableDesc.ExcludeDataFromBackup = excludeDataFromBackup
			return nil
		},
		onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
			po.tableDesc.ExcludeDataFromBackup = false
			return nil
		},
	},
	catpb.AutoStatsEnabledTableSettingName: {
		onSet:   autoStatsEnabledSettingFunc,
		onReset: autoStatsTableSettingResetFunc,
	},
	catpb.AutoStatsMinStaleTableSettingName: {
		onSet:   autoStatsMinStaleRowsSettingFunc(settings.NonNegativeInt),
		onReset: autoStatsTableSettingResetFunc,
	},
	catpb.AutoStatsFractionStaleTableSettingName: {
		onSet:   autoStatsFractionStaleRowsSettingFunc(settings.NonNegativeFloat),
		onReset: autoStatsTableSettingResetFunc,
	},
}

func init() {
	for _, param := range []string{
		`toast_tuple_target`,
		`parallel_workers`,
		`toast.autovacuum_enabled`,
		`autovacuum_vacuum_threshold`,
		`toast.autovacuum_vacuum_threshold`,
		`autovacuum_vacuum_scale_factor`,
		`toast.autovacuum_vacuum_scale_factor`,
		`autovacuum_analyze_threshold`,
		`autovacuum_analyze_scale_factor`,
		`autovacuum_vacuum_cost_delay`,
		`toast.autovacuum_vacuum_cost_delay`,
		`autovacuum_vacuum_cost_limit`,
		`autovacuum_freeze_min_age`,
		`toast.autovacuum_freeze_min_age`,
		`autovacuum_freeze_max_age`,
		`toast.autovacuum_freeze_max_age`,
		`autovacuum_freeze_table_age`,
		`toast.autovacuum_freeze_table_age`,
		`autovacuum_multixact_freeze_min_age`,
		`toast.autovacuum_multixact_freeze_min_age`,
		`autovacuum_multixact_freeze_max_age`,
		`toast.autovacuum_multixact_freeze_max_age`,
		`autovacuum_multixact_freeze_table_age`,
		`toast.autovacuum_multixact_freeze_table_age`,
		`log_autovacuum_min_duration`,
		`toast.log_autovacuum_min_duration`,
		`user_catalog_table`,
	} {
		tableParams[param] = tableParam{
			onSet: func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext, evalCtx *eval.Context, key string, datum tree.Datum) error {
				return unimplemented.NewWithIssuef(43299, "storage parameter %q", key)
			},
			onReset: func(po *Setter, evalCtx *eval.Context, key string) error {
				return nil
			},
		}
	}
}

func autoStatsEnabledSettingFunc(
	ctx context.Context,
	po *Setter,
	semaCtx *tree.SemaContext,
	evalCtx *eval.Context,
	key string,
	datum tree.Datum,
) error {
	boolVal, err := boolFromDatum(evalCtx, key, datum)
	if err != nil {
		return err
	}
	if po.tableDesc.AutoStatsSettings == nil {
		po.tableDesc.AutoStatsSettings = &catpb.AutoStatsSettings{}
	}
	po.tableDesc.AutoStatsSettings.Enabled = &boolVal
	return nil
}

func autoStatsMinStaleRowsSettingFunc(
	validateFunc func(v int64) error,
) func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext,
	evalCtx *eval.Context, key string, datum tree.Datum) error {
	return func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext,
		evalCtx *eval.Context, key string, datum tree.Datum) error {
		intVal, err := intFromDatum(evalCtx, key, datum)
		if err != nil {
			return err
		}
		if po.tableDesc.AutoStatsSettings == nil {
			po.tableDesc.AutoStatsSettings = &catpb.AutoStatsSettings{}
		}
		if err = validateFunc(intVal); err != nil {
			return errors.Wrapf(err, "invalid integer value for %s", key)
		}
		po.tableDesc.AutoStatsSettings.MinStaleRows = &intVal
		return nil
	}
}

func autoStatsFractionStaleRowsSettingFunc(
	validateFunc func(v float64) error,
) func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext,
	evalCtx *eval.Context, key string, datum tree.Datum) error {
	return func(ctx context.Context, po *Setter, semaCtx *tree.SemaContext,
		evalCtx *eval.Context, key string, datum tree.Datum) error {
		floatVal, err := floatFromDatum(evalCtx, key, datum)
		if err != nil {
			return err
		}
		if po.tableDesc.AutoStatsSettings == nil {
			po.tableDesc.AutoStatsSettings = &catpb.AutoStatsSettings{}
		}
		if err = validateFunc(floatVal); err != nil {
			return errors.Wrapf(err, "invalid float value for %s", key)
		}
		po.tableDesc.AutoStatsSettings.FractionStaleRows = &floatVal
		return nil
	}
}

func autoStatsTableSettingResetFunc(po *Setter, evalCtx *eval.Context, key string) error {
	if po.tableDesc.AutoStatsSettings == nil {
		return nil
	}
	autoStatsSettings := po.tableDesc.AutoStatsSettings
	switch key {
	case catpb.AutoStatsEnabledTableSettingName:
		autoStatsSettings.Enabled = nil
		return nil
	case catpb.AutoStatsMinStaleTableSettingName:
		autoStatsSettings.MinStaleRows = nil
		return nil
	case catpb.AutoStatsFractionStaleTableSettingName:
		autoStatsSettings.FractionStaleRows = nil
		return nil
	}
	return errors.Newf("unable to reset table setting %s", key)
}

// Set implements the Setter interface.
func (po *Setter) Set(
	ctx context.Context,
	semaCtx *tree.SemaContext,
	evalCtx *eval.Context,
	key string,
	datum tree.Datum,
) error {
	if strings.HasPrefix(key, "ttl_") && len(po.tableDesc.AllMutations()) > 0 {
		return pgerror.Newf(
			pgcode.FeatureNotSupported,
			"cannot modify TTL settings while another schema change on the table is being processed",
		)
	}
	if p, ok := tableParams[key]; ok {
		return p.onSet(ctx, po, semaCtx, evalCtx, key, datum)
	}
	return pgerror.Newf(pgcode.InvalidParameterValue, "invalid storage parameter %q", key)
}

// Reset implements the Setter interface.
func (po *Setter) Reset(evalCtx *eval.Context, key string) error {
	if strings.HasPrefix(key, "ttl_") && len(po.tableDesc.AllMutations()) > 0 {
		return pgerror.Newf(
			pgcode.FeatureNotSupported,
			"cannot modify TTL settings while another schema change on the table is being processed",
		)
	}
	if p, ok := tableParams[key]; ok {
		return p.onReset(po, evalCtx, key)
	}
	return pgerror.Newf(pgcode.InvalidParameterValue, "invalid storage parameter %q", key)
}
