// Code generated by "stringer -type=fetcherState"; DO NOT EDIT.

package colfetcher

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[stateInvalid-0]
	_ = x[stateInitFetch-1]
	_ = x[stateResetBatch-2]
	_ = x[stateDecodeFirstKVOfRow-3]
	_ = x[stateSeekPrefix-4]
	_ = x[stateFetchNextKVWithUnfinishedRow-5]
	_ = x[stateFinalizeRow-6]
	_ = x[stateEmitLastBatch-7]
	_ = x[stateFinished-8]
}

const _fetcherState_name = "stateInvalidstateInitFetchstateResetBatchstateDecodeFirstKVOfRowstateSeekPrefixstateFetchNextKVWithUnfinishedRowstateFinalizeRowstateEmitLastBatchstateFinished"

var _fetcherState_index = [...]uint8{0, 12, 26, 41, 64, 79, 112, 128, 146, 159}

func (i fetcherState) String() string {
	if i < 0 || i >= fetcherState(len(_fetcherState_index)-1) {
		return "fetcherState(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _fetcherState_name[_fetcherState_index[i]:_fetcherState_index[i+1]]
}
