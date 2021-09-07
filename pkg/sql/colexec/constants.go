// Copyright 2019 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package colexec

// DefaultVectorizeRowCountThreshold denotes the default row count threshold.
// When it is met, the vectorized execution engine will be used if possible.
// TODO(yuzefovich): remove this together with vectorize_row_count_threshold
// setting.
const DefaultVectorizeRowCountThreshold = 0

// VecMaxOpenFDsLimit specifies the maximum number of open file descriptors
// that the vectorized engine can have (globally) for use of the temporary
// storage.
const VecMaxOpenFDsLimit = 256
