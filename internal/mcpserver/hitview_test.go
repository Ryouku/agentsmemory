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

// TestEveryWiredHitFieldIsActuallyPOPULATED closes the hole in the check above.
//
// TestEveryHitFieldIsOnTheWireOrExcused compares field NAMES: a name on
// palace.SearchHit must appear on searchHitView or be excused. That proves the
// view DECLARES somewhere to put the value. It does not prove newSearchHitView
// ever puts one there — a field can be declared on both structs, pass the name
// check, and be left at its zero value by the mapper on every single response.
//
// This is not hypothetical and it is the repository's own characteristic defect
// sitting inside the check written to prevent it: rung 1 (the field exists) and
// rung 3 (a caller can see it in the schema) both satisfied, rung 2 (something
// selects it) missing. It was found on 2026-08-26 by trial-merging the
// behind-index branch, whose StaleIndex field lands on BOTH structs while the
// merge resolution drops it from the constructor. The name check passed. Only a
// behavioural test on the other branch caught it, which is luck, not coverage.
//
// So: fill every field of a SearchHit with a non-zero value, map it, and require
// every wired field to have survived.
func TestEveryWiredHitFieldIsActuallyPOPULATED(t *testing.T) {
	var hit palace.SearchHit
	hv := reflect.ValueOf(&hit).Elem()
	for i := 0; i < hv.NumField(); i++ {
		setNonZero(t, hv.Field(i))
	}

	got := reflect.ValueOf(newSearchHitView(hit))
	ht := reflect.TypeOf(palace.SearchHit{})
	for i := 0; i < ht.NumField(); i++ {
		name := ht.Field(i).Name
		if hitFieldsNotOnTheWire[name] != "" {
			continue
		}
		f := got.FieldByName(name)
		if !f.IsValid() {
			continue // the name check above owns "declared at all"
		}
		if f.IsZero() {
			t.Errorf("newSearchHitView leaves searchHitView.%s at its zero value even though "+
				"palace.SearchHit.%s was set.\n"+
				"  The field is declared on both structs, so the name check passes and the key "+
				"appears in every response — always empty. A field wired into the type but not "+
				"into the constructor is a capability nothing selects, one layer out.", name, name)
		}
	}
}

// setNonZero writes a distinguishable non-zero value into v, recursing into
// structs so an embedded Drawer is populated rather than left empty.
func setNonZero(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(0.5)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		setNonZero(t, v.Index(0))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				setNonZero(t, v.Field(i))
			}
		}
	}
}
