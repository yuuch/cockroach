// Copyright 2019 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package storage

import (
	"bytes"
	"context"
	"io"

	"github.com/cockroachdb/cockroach/pkg/clusterversion"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/util/uuid"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/sstable"
)

// SSTWriter writes SSTables.
type SSTWriter struct {
	fw *sstable.Writer
	f  io.Writer
	// DataSize tracks the total key and value bytes added so far.
	DataSize int64
	scratch  []byte
}

var _ Writer = &SSTWriter{}

// writeCloseSyncer interface copied from pebble.sstable.
type writeCloseSyncer interface {
	io.WriteCloser
	Sync() error
}

// noopSyncCloser is used to wrap io.Writers for sstable.Writer so that callers
// can decide when to close/sync.
type noopSyncCloser struct {
	io.Writer
}

func (noopSyncCloser) Sync() error {
	return nil
}

func (noopSyncCloser) Close() error {
	return nil
}

// MakeIngestionWriterOptions returns writer options suitable for writing SSTs
// that will subsequently be ingested (e.g. with AddSSTable).
func MakeIngestionWriterOptions(ctx context.Context, cs *cluster.Settings) sstable.WriterOptions {
	// By default, take a conservative approach and assume we don't have newer
	// table features available. Upgrade to an appropriate version only if the
	// cluster supports it.
	format := sstable.TableFormatRocksDBv2
	// Cases are ordered from newer to older versions.
	switch {
	case cs.Version.IsActive(ctx, clusterversion.EnablePebbleFormatVersionRangeKeys):
		format = sstable.TableFormatPebblev2 // Range keys.
	case cs.Version.IsActive(ctx, clusterversion.EnablePebbleFormatVersionBlockProperties):
		format = sstable.TableFormatPebblev1 // Block properties.
	}
	opts := DefaultPebbleOptions().MakeWriterOptions(0, format)
	if format < sstable.TableFormatPebblev1 {
		// Block properties aren't available at this version. Disable collection.
		opts.BlockPropertyCollectors = nil
	}
	opts.MergerName = "nullptr"
	return opts
}

// MakeBackupSSTWriter creates a new SSTWriter tailored for backup SSTs which
// are typically only ever iterated in their entirety.
func MakeBackupSSTWriter(ctx context.Context, cs *cluster.Settings, f io.Writer) SSTWriter {
	// By default, take a conservative approach and assume we don't have newer
	// table features available. Upgrade to an appropriate version only if the
	// cluster supports it.
	opts := DefaultPebbleOptions().MakeWriterOptions(0, sstable.TableFormatRocksDBv2)
	if cs.Version.IsActive(ctx, clusterversion.EnablePebbleFormatVersionRangeKeys) {
		opts.TableFormat = sstable.TableFormatPebblev2 // Range keys.
	}
	// Don't need BlockPropertyCollectors for backups.
	opts.BlockPropertyCollectors = nil

	// Disable bloom filters since we only ever iterate backups.
	opts.FilterPolicy = nil
	// Bump up block size, since we almost never seek or do point lookups, so more
	// block checksums and more index entries are just overhead and smaller blocks
	// reduce compression ratio.
	opts.BlockSize = 128 << 10

	opts.MergerName = "nullptr"
	sst := sstable.NewWriter(noopSyncCloser{f}, opts)
	return SSTWriter{fw: sst, f: f}
}

// MakeIngestionSSTWriter creates a new SSTWriter tailored for ingestion SSTs.
// These SSTs have bloom filters enabled (as set in DefaultPebbleOptions) and
// format set to RocksDBv2.
func MakeIngestionSSTWriter(
	ctx context.Context, cs *cluster.Settings, f writeCloseSyncer,
) SSTWriter {
	return SSTWriter{
		fw: sstable.NewWriter(f, MakeIngestionWriterOptions(ctx, cs)),
		f:  f,
	}
}

// Finish finalizes the writer and returns the constructed file's contents,
// since the last call to Truncate (if any). At least one kv entry must have been added.
func (fw *SSTWriter) Finish() error {
	if fw.fw == nil {
		return errors.New("cannot call Finish on a closed writer")
	}
	if err := fw.fw.Close(); err != nil {
		return err
	}
	fw.fw = nil
	return nil
}

// ClearRawRange implements the Writer interface.
//
// TODO(erikgrinaker): This must clear range keys when SSTs support them.
func (fw *SSTWriter) ClearRawRange(start, end roachpb.Key) error {
	return fw.clearRange(MVCCKey{Key: start}, MVCCKey{Key: end})
}

// ClearMVCCRange implements the Writer interface.
func (fw *SSTWriter) ClearMVCCRange(start, end roachpb.Key) error {
	panic("not implemented")
}

// ClearMVCCVersions implements the Writer interface.
func (fw *SSTWriter) ClearMVCCVersions(start, end MVCCKey) error {
	return fw.clearRange(start, end)
}

// ExperimentalPutMVCCRangeKey implements the Writer interface.
func (fw *SSTWriter) ExperimentalPutMVCCRangeKey(MVCCRangeKey, MVCCValue) error {
	panic("not implemented")
}

// ExperimentalClearMVCCRangeKey implements the Writer interface.
func (fw *SSTWriter) ExperimentalClearMVCCRangeKey(MVCCRangeKey) error {
	panic("not implemented")
}

// ExperimentalClearAllMVCCRangeKeys implements the Writer interface.
//
// TODO(erikgrinaker): This must clear range keys when SSTs support them.
func (fw *SSTWriter) ExperimentalClearAllMVCCRangeKeys(roachpb.Key, roachpb.Key) error {
	return nil
}

func (fw *SSTWriter) clearRange(start, end MVCCKey) error {
	if fw.fw == nil {
		return errors.New("cannot call ClearRange on a closed writer")
	}
	fw.DataSize += int64(len(start.Key)) + int64(len(end.Key))
	fw.scratch = EncodeMVCCKeyToBuf(fw.scratch[:0], start)
	return fw.fw.DeleteRange(fw.scratch, EncodeMVCCKey(end))
}

// Put puts a kv entry into the sstable being built. An error is returned if it
// is not greater than any previously added entry (according to the comparator
// configured during writer creation). `Close` cannot have been called.
//
// TODO(sumeer): Put has been removed from the Writer interface, but there
// are many callers of this SSTWriter method. Fix those callers and remove.
func (fw *SSTWriter) Put(key MVCCKey, value []byte) error {
	if fw.fw == nil {
		return errors.New("cannot call Put on a closed writer")
	}
	fw.DataSize += int64(len(key.Key)) + int64(len(value))
	fw.scratch = EncodeMVCCKeyToBuf(fw.scratch[:0], key)
	return fw.fw.Set(fw.scratch, value)
}

// PutMVCC implements the Writer interface.
// An error is returned if it is not greater than any previously added entry
// (according to the comparator configured during writer creation). `Close`
// cannot have been called.
func (fw *SSTWriter) PutMVCC(key MVCCKey, value MVCCValue) error {
	if key.Timestamp.IsEmpty() {
		panic("PutMVCC timestamp is empty")
	}
	encValue, err := EncodeMVCCValue(value)
	if err != nil {
		return err
	}
	return fw.put(key, encValue)
}

// PutRawMVCC implements the Writer interface.
// An error is returned if it is not greater than any previously added entry
// (according to the comparator configured during writer creation). `Close`
// cannot have been called.
func (fw *SSTWriter) PutRawMVCC(key MVCCKey, value []byte) error {
	if key.Timestamp.IsEmpty() {
		panic("PutRawMVCC timestamp is empty")
	}
	return fw.put(key, value)
}

// PutUnversioned implements the Writer interface.
// An error is returned if it is not greater than any previously added entry
// (according to the comparator configured during writer creation). `Close`
// cannot have been called.
func (fw *SSTWriter) PutUnversioned(key roachpb.Key, value []byte) error {
	return fw.put(MVCCKey{Key: key}, value)
}

// PutIntent implements the Writer interface.
// An error is returned if it is not greater than any previously added entry
// (according to the comparator configured during writer creation). `Close`
// cannot have been called.
func (fw *SSTWriter) PutIntent(
	ctx context.Context, key roachpb.Key, value []byte, txnUUID uuid.UUID,
) error {
	return fw.put(MVCCKey{Key: key}, value)
}

// PutEngineKey implements the Writer interface.
// An error is returned if it is not greater than any previously added entry
// (according to the comparator configured during writer creation). `Close`
// cannot have been called.
func (fw *SSTWriter) PutEngineKey(key EngineKey, value []byte) error {
	if fw.fw == nil {
		return errors.New("cannot call Put on a closed writer")
	}
	fw.DataSize += int64(len(key.Key)) + int64(len(value))
	fw.scratch = key.EncodeToBuf(fw.scratch[:0])
	return fw.fw.Set(fw.scratch, value)
}

// put puts a kv entry into the sstable being built. An error is returned if it
// is not greater than any previously added entry (according to the comparator
// configured during writer creation). `Close` cannot have been called.
func (fw *SSTWriter) put(key MVCCKey, value []byte) error {
	if fw.fw == nil {
		return errors.New("cannot call Put on a closed writer")
	}
	fw.DataSize += int64(len(key.Key)) + int64(len(value))
	fw.scratch = EncodeMVCCKeyToBuf(fw.scratch[:0], key)
	return fw.fw.Set(fw.scratch, value)
}

// ApplyBatchRepr implements the Writer interface.
func (fw *SSTWriter) ApplyBatchRepr(repr []byte, sync bool) error {
	panic("unimplemented")
}

// ClearMVCC implements the Writer interface. An error is returned if it is
// not greater than any previous point key passed to this Writer (according to
// the comparator configured during writer creation). `Close` cannot have been
// called.
func (fw *SSTWriter) ClearMVCC(key MVCCKey) error {
	if key.Timestamp.IsEmpty() {
		panic("ClearMVCC timestamp is empty")
	}
	return fw.clear(key)
}

// ClearUnversioned implements the Writer interface. An error is returned if
// it is not greater than any previous point key passed to this Writer
// (according to the comparator configured during writer creation). `Close`
// cannot have been called.
func (fw *SSTWriter) ClearUnversioned(key roachpb.Key) error {
	return fw.clear(MVCCKey{Key: key})
}

// ClearIntent implements the Writer interface. An error is returned if it is
// not greater than any previous point key passed to this Writer (according to
// the comparator configured during writer creation). `Close` cannot have been
// called.
func (fw *SSTWriter) ClearIntent(
	key roachpb.Key, txnDidNotUpdateMeta bool, txnUUID uuid.UUID,
) error {
	panic("ClearIntent is unsupported")
}

// ClearEngineKey implements the Writer interface. An error is returned if it is
// not greater than any previous point key passed to this Writer (according to
// the comparator configured during writer creation). `Close` cannot have been
// called.
func (fw *SSTWriter) ClearEngineKey(key EngineKey) error {
	if fw.fw == nil {
		return errors.New("cannot call Clear on a closed writer")
	}
	fw.scratch = key.EncodeToBuf(fw.scratch[:0])
	fw.DataSize += int64(len(key.Key))
	return fw.fw.Delete(fw.scratch)
}

// An error is returned if it is not greater than any previous point key
// passed to this Writer (according to the comparator configured during writer
// creation). `Close` cannot have been called.
func (fw *SSTWriter) clear(key MVCCKey) error {
	if fw.fw == nil {
		return errors.New("cannot call Clear on a closed writer")
	}
	fw.scratch = EncodeMVCCKeyToBuf(fw.scratch[:0], key)
	fw.DataSize += int64(len(key.Key))
	return fw.fw.Delete(fw.scratch)
}

// SingleClearEngineKey implements the Writer interface.
func (fw *SSTWriter) SingleClearEngineKey(key EngineKey) error {
	panic("unimplemented")
}

// ClearMVCCIteratorRange implements the Writer interface.
func (fw *SSTWriter) ClearMVCCIteratorRange(start, end roachpb.Key) error {
	panic("not implemented")
}

// Merge implements the Writer interface.
func (fw *SSTWriter) Merge(key MVCCKey, value []byte) error {
	if fw.fw == nil {
		return errors.New("cannot call Merge on a closed writer")
	}
	fw.DataSize += int64(len(key.Key)) + int64(len(value))
	fw.scratch = EncodeMVCCKeyToBuf(fw.scratch[:0], key)
	return fw.fw.Merge(fw.scratch, value)
}

// LogData implements the Writer interface.
func (fw *SSTWriter) LogData(data []byte) error {
	// No-op.
	return nil
}

// LogLogicalOp implements the Writer interface.
func (fw *SSTWriter) LogLogicalOp(op MVCCLogicalOpType, details MVCCLogicalOpDetails) {
	// No-op.
}

// Close finishes and frees memory and other resources. Close is idempotent.
func (fw *SSTWriter) Close() {
	if fw.fw == nil {
		return
	}
	// pebble.Writer *does* return interesting errors from Close... but normally
	// we already called its Close() in Finish() and we no-op here. Thus the only
	// time we expect to be here is in a deferred Close(), in which case the caller
	// probably is already returning some other error, so returning one from this
	// method just makes for messy defers.
	_ = fw.fw.Close()
	fw.fw = nil
}

// ShouldWriteLocalTimestamps implements the Writer interface.
func (fw *SSTWriter) ShouldWriteLocalTimestamps(context.Context) bool {
	return false
}

// MemFile is a file-like struct that buffers all data written to it in memory.
// Implements the writeCloseSyncer interface and is intended for use with
// SSTWriter.
type MemFile struct {
	bytes.Buffer
}

// Close implements the writeCloseSyncer interface.
func (*MemFile) Close() error {
	return nil
}

// Flush implements the same interface as the standard library's *bufio.Writer's
// Flush method. The Pebble sstable Writer tests whether files implement a Flush
// method. If not, it wraps the file with a bufio.Writer to buffer writes to the
// underlying file. This buffering is not necessary for an in-memory file. We
// signal this by implementing Flush as a noop.
func (*MemFile) Flush() error {
	return nil
}

// Sync implements the writeCloseSyncer interface.
func (*MemFile) Sync() error {
	return nil
}

// Data returns the in-memory buffer behind this MemFile.
func (f *MemFile) Data() []byte {
	return f.Bytes()
}
