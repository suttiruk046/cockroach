// Copyright 2017 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package batcheval

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/context"

	opentracing "github.com/opentracing/opentracing-go"

	"github.com/cockroachdb/cockroach/pkg/internal/client"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/storage/abortspan"
	"github.com/cockroachdb/cockroach/pkg/storage/engine"
	"github.com/cockroachdb/cockroach/pkg/storage/engine/enginepb"
	"github.com/cockroachdb/cockroach/pkg/storage/spanset"
	"github.com/cockroachdb/cockroach/pkg/storage/txnwait"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/uuid"
)

type mockEvalCtx struct {
	abortSpan *abortspan.AbortSpan
}

func (m *mockEvalCtx) String() string {
	return "mock"
}
func (m *mockEvalCtx) ClusterSettings() *cluster.Settings {
	panic("unimplemented")
}
func (m *mockEvalCtx) EvalKnobs() TestingKnobs {
	panic("unimplemented")
}
func (m *mockEvalCtx) Tracer() opentracing.Tracer {
	panic("unimplemented")
}
func (m *mockEvalCtx) Engine() engine.Engine {
	panic("unimplemented")
}
func (m *mockEvalCtx) DB() *client.DB {
	panic("unimplemented")
}
func (m *mockEvalCtx) AbortSpan() *abortspan.AbortSpan {
	return m.abortSpan
}
func (m *mockEvalCtx) GetTxnWaitQueue() *txnwait.Queue {
	panic("unimplemented")
}
func (m *mockEvalCtx) NodeID() roachpb.NodeID {
	panic("unimplemented")
}
func (m *mockEvalCtx) StoreID() roachpb.StoreID {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetRangeID() roachpb.RangeID {
	panic("unimplemented")
}
func (m *mockEvalCtx) IsFirstRange() bool {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetFirstIndex() (uint64, error) {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetTerm(uint64) (uint64, error) {
	panic("unimplemented")
}
func (m *mockEvalCtx) Desc() *roachpb.RangeDescriptor {
	panic("unimplemented")
}
func (m *mockEvalCtx) ContainsKey(key roachpb.Key) bool {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetMVCCStats() enginepb.MVCCStats {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetGCThreshold() hlc.Timestamp {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetTxnSpanGCThreshold() hlc.Timestamp {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetLastReplicaGCTimestamp(context.Context) (hlc.Timestamp, error) {
	panic("unimplemented")
}
func (m *mockEvalCtx) GetLease() (roachpb.Lease, *roachpb.Lease) {
	panic("unimplemented")
}

func TestDeclareKeysResolveIntent(t *testing.T) {
	defer leaktest.AfterTest(t)()

	const id = "f90b99de-6bd2-48a3-873c-12fdb9867a3c"
	txnMeta := enginepb.TxnMeta{}
	{
		var err error
		txnMeta.ID, err = uuid.FromString(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	abortSpanKey := fmt.Sprintf(`1 1: /{Local/RangeID/99/r/AbortSpan/"%s"-Min}`, id)
	desc := roachpb.RangeDescriptor{
		RangeID:  99,
		StartKey: roachpb.RKey("a"),
		EndKey:   roachpb.RKey("a"),
	}
	tests := []struct {
		status      roachpb.TransactionStatus
		poison      bool
		expDeclares bool
	}{
		{
			status:      roachpb.ABORTED,
			poison:      true,
			expDeclares: true,
		},
		{
			status:      roachpb.ABORTED,
			poison:      false,
			expDeclares: true,
		},
		{
			status:      roachpb.COMMITTED,
			poison:      true,
			expDeclares: false,
		},
		{
			status:      roachpb.COMMITTED,
			poison:      false,
			expDeclares: false,
		},
	}
	ctx := context.Background()
	engine := engine.NewInMem(roachpb.Attributes{}, 1<<20)
	defer engine.Close()
	testutils.RunTrueAndFalse(t, "ranged", func(t *testing.T, ranged bool) {
		for _, test := range tests {
			t.Run("", func(t *testing.T) {
				ri := roachpb.ResolveIntentRequest{
					IntentTxn: txnMeta,
					Status:    test.status,
					Poison:    test.poison,
				}
				ri.Key = roachpb.Key("b")
				rir := roachpb.ResolveIntentRangeRequest(ri)
				rir.EndKey = roachpb.Key("c")

				ac := abortspan.New(desc.RangeID)

				var spans spanset.SpanSet
				batch := engine.NewBatch()
				batch = spanset.NewBatch(batch, &spans)
				defer batch.Close()

				var h roachpb.Header
				h.RangeID = desc.RangeID

				cArgs := CommandArgs{Header: h}
				cArgs.EvalCtx = &mockEvalCtx{abortSpan: ac}

				if !ranged {
					cArgs.Args = &ri
					declareKeysResolveIntent(desc, h, &ri, &spans)
					if _, err := ResolveIntent(ctx, batch, cArgs, &roachpb.ResolveIntentResponse{}); err != nil {
						t.Fatal(err)
					}
				} else {
					cArgs.Args = &rir
					declareKeysResolveIntentRange(desc, h, &rir, &spans)
					if _, err := ResolveIntentRange(ctx, batch, cArgs, &roachpb.ResolveIntentRangeResponse{}); err != nil {
						t.Fatal(err)
					}
				}

				if s := spans.String(); strings.Contains(s, abortSpanKey) != test.expDeclares {
					t.Errorf("expected AbortSpan declared: %t, but got spans\n%s", test.expDeclares, s)
				}
			})
		}
	})
}
