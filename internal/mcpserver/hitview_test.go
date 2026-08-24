package mcpserver

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// hitFieldsNotOnTheWire are palace.SearchHit fields deliberately absent from
// searchHitView, each with the reason. An entry is an argument, not a mute
// button: TestEveryHitFieldIsOnTheWireOrExcused rejects a name that is not
// actually a field, so the list cannot outlive what it excuses.
var hitFieldsNotOnTheWire = map[string]string{
	"Drawer":        "flattened into the embedded drawerView rather than nested, so the memory's own fields sit at the top level of a hit",
	"MemoryContent": "mapped onto drawerView.Content after reassembly rather than exposed as a second duplicate content field",
}

// TestEveryHitFieldIsOnTheWireOrExcused: a field on palace.SearchHit either
// reaches the agent or is named here with a reason.
//
// Two fields failed this the day it was written, and both had shipped. Reranked
// had been on the domain hit since ADR-006 T4 made the telemetry honest, and the
// view dropped it — so "no reranker", "weight 0", "below the pool cutoff" and "the
// cross-encoder scored it 0.0" were one absent key at the only surface an agent
// reads. ChunksMatched was added by ADR-013 that same day, whose Decision says a
// memory matching in four places is stronger evidence than one matching in one,
// and it reached nothing: the signal the collapse was careful to preserve was
// discarded one layer later.
//
// A domain field with no wire field is the same defect as a capability nothing
// selects, one layer out. This is the check that makes it visible.
func TestEveryHitFieldIsOnTheWireOrExcused(t *testing.T) {
	wire := map[string]bool{}
	vt := reflect.TypeOf(searchHitView{})
	for i := 0; i < vt.NumField(); i++ {
		wire[vt.Field(i).Name] = true
	}

	ht := reflect.TypeOf(palace.SearchHit{})
	if ht.NumField() == 0 {
		t.Fatal("palace.SearchHit has no fields — this check has stopped reading it")
	}

	var missing []string
	for i := 0; i < ht.NumField(); i++ {
		name := ht.Field(i).Name
		if wire[name] || hitFieldsNotOnTheWire[name] != "" {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("palace.SearchHit.%s never reaches an agent: it is not a field of searchHitView "+
			"and not named in hitFieldsNotOnTheWire.\n"+
			"  Either map it, or say in one line why the caller does not need it.", m)
	}

	// The excuse list is gated too, or it becomes the hole.
	for name, why := range hitFieldsNotOnTheWire {
		if _, ok := ht.FieldByName(name); !ok {
			t.Errorf("hitFieldsNotOnTheWire names %q, which is not a field of palace.SearchHit — "+
				"a stale excuse outlives the thing it excused", name)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s is excused with no reason", name)
		}
	}
}
