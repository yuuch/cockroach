// Copyright 2021 The Cockroach Authors.
//
// Licensed as a CockroachDB Enterprise file under the Cockroach Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//     https://github.com/cockroachdb/cockroach/blob/master/licenses/CCL.txt

package streamingest

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/base"
	_ "github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/changefeedbase"
	"github.com/cockroachdb/cockroach/pkg/ccl/streamingccl/streamingtest"
	"github.com/cockroachdb/cockroach/pkg/ccl/streamingccl/streampb"
	_ "github.com/cockroachdb/cockroach/pkg/ccl/streamingccl/streamproducer"
	"github.com/cockroachdb/cockroach/pkg/jobs"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/keys"
	"github.com/cockroachdb/cockroach/pkg/kv"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/security/username"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/jobutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/testcluster"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	json2 "github.com/cockroachdb/cockroach/pkg/util/json"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

func verifyIngestionStats(t *testing.T, streamID int64, cutoverTime time.Time, stats string) {
	fetchValueKey := func(j json2.JSON, key string) json2.JSON {
		val, err := j.FetchValKey(key)
		require.NoError(t, err)
		return val
	}

	parseInt64 := func(s string) int64 {
		res, err := strconv.Atoi(s)
		require.NoError(t, err)
		return int64(res)
	}

	statsJSON, err := json2.ParseJSON(stats)
	require.NoError(t, err)

	ingestionProgress := fetchValueKey(statsJSON, "ingestion_progress")
	require.Equal(t, cutoverTime.UnixNano(),
		parseInt64(fetchValueKey(fetchValueKey(ingestionProgress, "cutover_time"), "wall_time").String()))

	partitionProgressIter, err := fetchValueKey(ingestionProgress, "partition_progress").ObjectIter()
	require.NoError(t, err)
	for partitionProgressIter.Next() {
		require.Less(t, cutoverTime.UnixNano(), parseInt64(fetchValueKey(fetchValueKey(
			partitionProgressIter.Value(), "ingested_timestamp"), "wall_time").String()))
	}

	require.Equal(t, streamID, parseInt64(fetchValueKey(statsJSON, "stream_id").String()))
	require.Equal(t, strconv.Itoa(int(streampb.StreamReplicationStatus_STREAM_INACTIVE)),
		fetchValueKey(fetchValueKey(statsJSON, "stream_replication_status"), "stream_status").String())
}

// TestTenantStreaming tests that tenants can stream changes end-to-end.
func TestTenantStreaming(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.WithIssue(t, 82706)

	skip.UnderRace(t, "slow under race")

	ctx := context.Background()

	args := base.TestServerArgs{Knobs: base.TestingKnobs{
		JobsTestingKnobs: jobs.NewTestingKnobsWithShortIntervals()},
	}

	// Start the source server.
	source, sourceDB, _ := serverutils.StartServer(t, args)
	defer source.Stopper().Stop(ctx)

	// Start tenant server in the source cluster.
	tenantID := serverutils.TestTenantID()
	_, tenantConn := serverutils.StartTenant(t, source, base.TestTenantArgs{TenantID: tenantID})
	defer func() {
		require.NoError(t, tenantConn.Close())
	}()
	// sourceSQL refers to the tenant generating the data.
	sourceSQL := sqlutils.MakeSQLRunner(tenantConn)

	// Make changefeeds run faster.
	resetFreq := changefeedbase.TestingSetDefaultMinCheckpointFrequency(50 * time.Millisecond)
	defer resetFreq()
	// Set required cluster settings.
	sourceDBRunner := sqlutils.MakeSQLRunner(sourceDB)
	sourceDBRunner.ExecMultiple(t, strings.Split(`
SET CLUSTER SETTING kv.rangefeed.enabled = true;
SET CLUSTER SETTING kv.closed_timestamp.target_duration = '1s';
SET CLUSTER SETTING changefeed.experimental_poll_interval = '10ms';
SET CLUSTER SETTING stream_replication.min_checkpoint_frequency = '1s';
`,
		";")...)

	// Start the destination server.
	hDest, cleanupDest := streamingtest.NewReplicationHelper(t, base.TestServerArgs{}, roachpb.MakeTenantID(20))
	defer cleanupDest()
	// destSQL refers to the system tenant as that's the one that's running the
	// job.
	destSQL := hDest.SysDB
	destSQL.ExecMultiple(t, strings.Split(`
SET CLUSTER SETTING stream_replication.consumer_heartbeat_frequency = '2s';
SET CLUSTER SETTING bulkio.stream_ingestion.minimum_flush_interval = '500ms';
SET CLUSTER SETTING bulkio.stream_ingestion.cutover_signal_poll_interval = '100ms';
SET CLUSTER SETTING streaming.partition_progress_frequency = '100ms';
SET enable_experimental_stream_replication = true;
`,
		";")...)

	// Sink to read data from.
	pgURL, cleanupSink := sqlutils.PGUrl(t, source.ServingSQLAddr(), t.Name(), url.User(username.RootUser))
	defer cleanupSink()

	var ingestionJobID, streamProducerJobID int64
	var startTime string
	sourceSQL.QueryRow(t, "SELECT cluster_logical_timestamp()").Scan(&startTime)

	destSQL.ExpectErr(t, "pq: either old tenant ID 10 or the new tenant ID 1 cannot be system tenant",
		`RESTORE TENANT 10 FROM REPLICATION STREAM FROM $1 AS OF SYSTEM TIME `+startTime+` AS TENANT `+fmt.Sprintf("%d", roachpb.SystemTenantID.ToUint64()),
		pgURL.String())
	destSQL.QueryRow(t,
		`RESTORE TENANT 10 FROM REPLICATION STREAM FROM $1 AS OF SYSTEM TIME `+startTime+` AS TENANT 20`,
		pgURL.String(),
	).Scan(&ingestionJobID)

	sourceDBRunner.CheckQueryResultsRetry(t,
		"SELECT count(*) FROM [SHOW JOBS] WHERE job_type = 'STREAM REPLICATION'", [][]string{{"1"}})
	sourceDBRunner.QueryRow(t, "SELECT job_id FROM [SHOW JOBS] WHERE job_type = 'STREAM REPLICATION'").
		Scan(&streamProducerJobID)

	sourceSQL.Exec(t, `
CREATE DATABASE d;
CREATE TABLE d.t1(i int primary key, a string, b string);
CREATE TABLE d.t2(i int primary key);
INSERT INTO d.t1 (i) VALUES (42);
INSERT INTO d.t2 VALUES (2);
`)

	// Pick a cutover time, then wait for the job to reach that time.
	cutoverTime := timeutil.Now().Round(time.Microsecond)
	testutils.SucceedsSoon(t, func() error {
		progress := jobutils.GetJobProgress(t, destSQL, jobspb.JobID(ingestionJobID))
		if progress.GetHighWater() == nil {
			return errors.Newf("stream ingestion has not recorded any progress yet, waiting to advance pos %s",
				cutoverTime.String())
		}
		highwater := timeutil.Unix(0, progress.GetHighWater().WallTime)
		if highwater.Before(cutoverTime) {
			return errors.Newf("waiting for stream ingestion job progress %s to advance beyond %s",
				highwater.String(), cutoverTime.String())
		}
		return nil
	})

	destSQL.Exec(
		t,
		`SELECT crdb_internal.complete_stream_ingestion_job($1, $2)`,
		ingestionJobID, cutoverTime)

	jobutils.WaitForJobToSucceed(t, destSQL, jobspb.JobID(ingestionJobID))
	jobutils.WaitForJobToSucceed(t, sourceDBRunner, jobspb.JobID(streamProducerJobID))

	verifyIngestionStats(t, streamProducerJobID, cutoverTime,
		destSQL.QueryStr(t, "SELECT crdb_internal.stream_ingestion_stats($1)", ingestionJobID)[0][0])

	query := "SELECT * FROM d.t1"
	sourceData := sourceSQL.QueryStr(t, query)
	destData := hDest.Tenant.SQL.QueryStr(t, query)
	require.Equal(t, sourceData, destData)
}

func TestCutoverBuiltin(t *testing.T) {
	defer leaktest.AfterTest(t)()
	ctx := context.Background()

	args := base.TestClusterArgs{ServerArgs: base.TestServerArgs{
		Knobs: base.TestingKnobs{JobsTestingKnobs: jobs.NewTestingKnobsWithShortIntervals()},
	}}
	tc := testcluster.StartTestCluster(t, 1, args)
	defer tc.Stopper().Stop(ctx)
	registry := tc.Server(0).JobRegistry().(*jobs.Registry)
	sqlDB := sqlutils.MakeSQLRunner(tc.ServerConn(0))
	db := sqlDB.DB

	startTimestamp := hlc.Timestamp{WallTime: timeutil.Now().UnixNano()}
	streamIngestJobRecord := jobs.Record{
		Description: "test stream ingestion",
		Username:    username.RootUserName(),
		Details: jobspb.StreamIngestionDetails{
			StreamAddress: "randomgen://test",
			Span:          roachpb.Span{Key: keys.LocalMax, EndKey: keys.LocalMax.Next()},
			StartTime:     startTimestamp,
		},
		Progress: jobspb.StreamIngestionProgress{},
	}
	var job *jobs.StartableJob
	id := registry.MakeJobID()
	err := tc.Server(0).DB().Txn(ctx, func(ctx context.Context, txn *kv.Txn) (err error) {
		return registry.CreateStartableJobWithTxn(ctx, &job, id, txn, streamIngestJobRecord)
	})
	require.NoError(t, err)

	// Check that sentinel is not set.
	progress := job.Progress()
	sp, ok := progress.GetDetails().(*jobspb.Progress_StreamIngest)
	require.True(t, ok)
	require.True(t, sp.StreamIngest.CutoverTime.IsEmpty())

	var highWater time.Time
	err = job.Update(ctx, nil, func(_ *kv.Txn, md jobs.JobMetadata, ju *jobs.JobUpdater) error {
		highWater = timeutil.Now().Round(time.Microsecond)
		hlcHighWater := hlc.Timestamp{WallTime: highWater.UnixNano()}
		return jobs.UpdateHighwaterProgressed(hlcHighWater, md, ju)
	})
	require.NoError(t, err)

	// Ensure that the builtin runs locally.
	var explain string
	err = db.QueryRowContext(ctx,
		`EXPLAIN SELECT crdb_internal.complete_stream_ingestion_job($1, $2)`, job.ID(),
		highWater).Scan(&explain)
	require.NoError(t, err)
	require.Equal(t, "distribution: local", explain)

	var jobID int64
	err = db.QueryRowContext(
		ctx,
		`SELECT crdb_internal.complete_stream_ingestion_job($1, $2)`,
		job.ID(), highWater).Scan(&jobID)
	require.NoError(t, err)
	require.Equal(t, job.ID(), jobspb.JobID(jobID))

	// This should fail since we already have a cutover time set on the job
	// progress.
	_, err = db.ExecContext(
		ctx,
		`SELECT crdb_internal.complete_stream_ingestion_job($1, $2)`,
		job.ID(), highWater)
	require.Error(t, err, "cutover timestamp already set")

	// Check that sentinel is set on the job progress.
	sj, err := registry.LoadJob(ctx, job.ID())
	require.NoError(t, err)
	progress = sj.Progress()
	sp, ok = progress.GetDetails().(*jobspb.Progress_StreamIngest)
	require.True(t, ok)
	require.Equal(t, hlc.Timestamp{WallTime: highWater.UnixNano()}, sp.StreamIngest.CutoverTime)
}
