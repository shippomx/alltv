package newtvproto

import "testing"

func TestClipPlanExpandsNativeStreamCounts(t *testing.T) {
	plan := newClipPlan([]ClipInfo{{100, 3}, {120, 2}}, 100, 1)
	want := []dataClip{{100, 1}, {100, 2}, {120, 0}, {120, 1}}
	if len(plan.items) != len(want) {
		t.Fatalf("items=%v", plan.items)
	}
	for i := range want {
		if plan.items[i] != want[i] {
			t.Fatalf("item %d=%v want %v", i, plan.items[i], want[i])
		}
		if ordinal, ok := plan.ordinal(want[i].StreamID, want[i].Sequence); !ok || ordinal != uint32(i) {
			t.Fatalf("ordinal %d=%d,%v", i, ordinal, ok)
		}
	}
}

func TestClipPlanAppendsOverlappingLoadingRange(t *testing.T) {
	plan := newClipPlan([]ClipInfo{{100, 2}, {120, 2}}, 100, 0)
	plan.append([]ClipInfo{{120, 2}, {140, 3}})
	if len(plan.items) != 7 || plan.items[4] != (dataClip{140, 0}) || plan.items[6] != (dataClip{140, 2}) || plan.remaining(5) != 2 {
		t.Fatalf("items=%v", plan.items)
	}
}
