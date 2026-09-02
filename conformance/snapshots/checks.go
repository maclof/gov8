//go:build windows && amd64

package main

// The 15 snapshot/handle/termination checks of the pinned Rust snapshots
// oracle (rust-oracle/src/bin/conformance-snapshots.rs), re-implemented on
// the Go binding in the same registry order. Every value is produced by
// live engine observation; the comparison target is the pinned fixture.

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// --- harness (mirrors the runner's eval/eval_text helpers) -------------------

func evalText(t *testing.T, r *runtime, source string) string {
	t.Helper()
	v, err := r.eval(t, source)
	if err != nil {
		return ""
	}
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// evalOK compiles and runs source, reporting whether it completed.
func evalOK(t *testing.T, r *runtime, source string) bool {
	t.Helper()
	_, err := r.eval(t, source)
	return err == nil
}

// dataErrorKind maps a snapshot-data retrieval error to the normalized
// DataError kind ("NoData"/"BadType"); "" on success.
func dataErrorKind(err error) string {
	de, ok := err.(*gov8.SnapshotDataError)
	if !ok {
		return ""
	}
	if de.Kind == gov8.DataErrorBadType {
		return "BadType"
	}
	return "NoData"
}

// resultKind is the kind label for a retrieval result ("Ok" or error kind).
func resultKind(err error) string {
	if err == nil {
		return "Ok"
	}
	return dataErrorKind(err)
}

// makeBlob creates one snapshot blob from a fresh creator isolate whose
// default context defines globalThis.a = 7 and a callable globalThis.f.
// keep selects the FunctionCodeHandling policy. Mirrors the oracle's
// make_blob. The creator is always consumed; the caller releases the blob.
func makeBlob(t *testing.T, keep bool) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatalf("NewSnapshotCreator: %v", err)
	}
	iso := creator.Isolate()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if _, err := eval(t, ctx, scope, "globalThis.a = 7;"); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := eval(t, ctx, scope, "globalThis.f = function () { return a * 2; };"); err != nil {
		t.Fatalf("seed f: %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatalf("SetDefaultContext: %v", err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatalf("ctx.Close: %v", err)
	}
	blob, err := creator.CreateBlob(keepPolicy(keep))
	if err != nil {
		t.Fatalf("CreateBlob: %v", err)
	}
	return blob
}

func keepPolicy(keep bool) gov8.FunctionCodeHandling {
	if keep {
		return gov8.FunctionCodeKeep
	}
	return gov8.FunctionCodeClear
}

// newConsumerFromBlob isolates a consumer isolate + context + scope triple
// from a snapshot blob (CreateParams::snapshot_blob via Isolate::new; the
// default context instantiates the snapshotted global object).
func newConsumerFromBlob(t *testing.T, blob *gov8.StartupData) *runtime {
	t.Helper()
	iso, err := gov8.NewIsolateFromSnapshot(blob)
	if err != nil {
		t.Fatalf("NewIsolateFromSnapshot: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = ctx.Close()
		_ = iso.Close()
		t.Fatalf("NewScope: %v", err)
	}
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

// --- snapshot checks -----------------------------------------------------------

func checkCreateBlobPolicies(t *testing.T) obs {
	t.Helper()
	actualPolicies := make([]jsonValue, 0, 2)
	for _, keep := range []bool{false, true} {
		blob := makeBlob(t, keep)
		actualPolicies = append(actualPolicies, jobj(
			kv("len_gt_zero", jbool(!blob.IsEmpty())),
			kv("is_valid", jbool(blob.IsValid())),
		))
		if err := blob.Release(); err != nil {
			t.Fatalf("Release: %v", err)
		}
	}
	policyShape := jobj(
		kv("len_gt_zero", jbool(true)),
		kv("is_valid", jbool(true)),
	)
	return wantGot("snapshot/create_blob_policies",
		jarr(policyShape, policyShape), jarr(actualPolicies...))
}

func checkStartupDataPredicates(t *testing.T) obs {
	t.Helper()
	blob := makeBlob(t, false)
	defer func() { _ = blob.Release() }()
	return wantGot("snapshot/startup_data_predicates",
		jobj(
			kv("blob_len_gt_zero", jbool(true)),
			kv("blob_is_valid", jbool(true)),
			kv("blob_can_be_rehashed", jbool(true)),
		),
		jobj(
			kv("blob_len_gt_zero", jbool(!blob.IsEmpty())),
			kv("blob_is_valid", jbool(blob.IsValid())),
			kv("blob_can_be_rehashed", jbool(blob.CanBeRehashed())),
		))
}

func checkDefaultContextCreateParamsRoundtrip(t *testing.T) obs {
	t.Helper()
	blob := makeBlob(t, false)
	defer func() { _ = blob.Release() }()
	r := newConsumerFromBlob(t, blob)
	defer r.close(t)

	aIs7 := evalText(t, r, "String(a === 7)")
	fCall := evalText(t, r, "String(f())")
	fIsFunction := false
	if f, err := r.eval(t, "f"); err == nil {
		fIsFunction, _ = f.IsFunction()
	}

	return wantGot("snapshot/default_context_create_params_roundtrip",
		jobj(
			kv("a_is_7", jstr("true")),
			kv("f_call", jstr("14")),
			kv("f_is_function", jbool(true)),
		),
		jobj(
			kv("a_is_7", jstr(aIs7)),
			kv("f_call", jstr(fCall)),
			kv("f_is_function", jbool(fIsFunction)),
		))
}

func checkChainedSnapshotRoundtrip(t *testing.T) obs {
	t.Helper()
	first := makeBlob(t, true)
	defer func() { _ = first.Release() }()

	second := func() *gov8.StartupData {
		creator, err := gov8.NewSnapshotCreatorFromExistingSnapshot(first)
		if err != nil {
			t.Fatalf("NewSnapshotCreatorFromExistingSnapshot: %v", err)
		}
		iso := creator.Isolate()
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		// Inherited state from the first blob:
		if got := evalText(t, &runtime{iso: iso, ctx: ctx, scope: scope}, "String(a)"); got != "7" {
			t.Fatalf("inherited default context state: a = %q, want 7", got)
		}
		if _, err := eval(t, ctx, scope, "globalThis.b = a + 1;"); err != nil {
			t.Fatalf("seed b: %v", err)
		}
		if err := scope.Close(); err != nil {
			t.Fatalf("scope.Close: %v", err)
		}
		if err := creator.SetDefaultContext(ctx); err != nil {
			t.Fatalf("SetDefaultContext: %v", err)
		}
		if err := ctx.Close(); err != nil {
			t.Fatalf("ctx.Close: %v", err)
		}
		blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
		if err != nil {
			t.Fatalf("CreateBlob: %v", err)
		}
		return blob
	}()
	defer func() { _ = second.Release() }()
	secondValid := second.IsValid()

	r := newConsumerFromBlob(t, second)
	defer r.close(t)
	a := evalText(t, r, "String(a)")
	b := evalText(t, r, "String(b)")

	return wantGot("snapshot/chained_roundtrip",
		jobj(
			kv("second_blob_valid", jbool(true)),
			kv("a", jstr("7")),
			kv("b", jstr("8")),
		),
		jobj(
			kv("second_blob_valid", jbool(secondValid)),
			kv("a", jstr(a)),
			kv("b", jstr(b)),
		))
}

func checkAddContextFromSnapshot(t *testing.T) obs {
	t.Helper()
	var index0, index1 int
	blob := func() *gov8.StartupData {
		creator, err := gov8.NewSnapshotCreator()
		if err != nil {
			t.Fatalf("NewSnapshotCreator: %v", err)
		}
		iso := creator.Isolate()
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		// Upstream caveat: create_blob CHECK-fails if no default context
		// was set, even when only add_context is used, so a minimal
		// default context is always part of the blob.
		defaultCtx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext default: %v", err)
		}
		if err := creator.SetDefaultContext(defaultCtx); err != nil {
			t.Fatalf("SetDefaultContext: %v", err)
		}
		add := func(marker string) (*gov8.Context, int) {
			t.Helper()
			c, err := iso.NewContext()
			if err != nil {
				t.Fatalf("NewContext: %v", err)
			}
			s, err := iso.NewScope()
			if err != nil {
				t.Fatalf("NewScope: %v", err)
			}
			if _, err := eval(t, c, s, "globalThis.marker = '"+marker+"';"); err != nil {
				t.Fatalf("seed marker: %v", err)
			}
			_ = s.Close()
			idx, err := creator.AddContext(c)
			if err != nil {
				t.Fatalf("AddContext: %v", err)
			}
			return c, idx
		}
		c0, i0 := add("c0")
		c1, i1 := add("c1")
		_ = scope.Close()
		_ = defaultCtx.Close()
		_ = c0.Close()
		_ = c1.Close()
		index0, index1 = i0, i1
		b, err := creator.CreateBlob(gov8.FunctionCodeClear)
		if err != nil {
			t.Fatalf("CreateBlob: %v", err)
		}
		return b
	}()
	defer func() { _ = blob.Release() }()

	// Consume in a creator-from-existing isolate so the re-add index can be
	// observed too; that isolate is consumed by create_blob at the end.
	consumer, err := gov8.NewSnapshotCreatorFromExistingSnapshot(blob)
	if err != nil {
		t.Fatalf("NewSnapshotCreatorFromExistingSnapshot: %v", err)
	}
	cIso := consumer.Isolate()
	cScope, err := cIso.NewScope()
	if err != nil {
		t.Fatalf("consumer NewScope: %v", err)
	}
	// The consumer is a creator isolate; create_blob below requires a
	// default context on it as well, so seed one from the snapshot.
	cDefault, err := cIso.NewContext()
	if err != nil {
		t.Fatalf("consumer NewContext: %v", err)
	}
	if err := consumer.SetDefaultContext(cDefault); err != nil {
		t.Fatalf("consumer SetDefaultContext: %v", err)
	}

	recoverMarker := func(idx int) string {
		t.Helper()
		c, ok, err := cScope.ContextFromSnapshot(idx)
		if err != nil {
			t.Fatalf("ContextFromSnapshot(%d): %v", idx, err)
		}
		if !ok {
			return "<none>"
		}
		s, err := cIso.NewScope()
		if err != nil {
			t.Fatalf("recover NewScope: %v", err)
		}
		defer func() { _ = s.Close() }()
		v, err := eval(t, c, s, "marker")
		if err != nil {
			return "<none>"
		}
		defer func() { _ = c.Close() }()
		txt, err := v.ToString(c)
		if err != nil {
			return "<none>"
		}
		return txt
	}
	marker0 := recoverMarker(index0)
	marker1 := recoverMarker(index1)

	oobIsNone := false
	if _, ok, err := cScope.ContextFromSnapshot(7); err == nil && !ok {
		oobIsNone = true
	}

	readdIndex := -1
	if again, ok, err := cScope.ContextFromSnapshot(index0); err == nil && ok {
		if idx, err := consumer.AddContext(again); err == nil {
			readdIndex = idx
		}
		_ = again.Close()
	}
	_ = cScope.Close()
	_ = cDefault.Close()
	if _, err := consumer.CreateBlob(gov8.FunctionCodeClear); err != nil {
		t.Fatalf("consumer CreateBlob: %v", err)
	}

	return wantGot("snapshot/add_context_from_snapshot",
		jobj(
			kv("index0", jint(0)),
			kv("index1", jint(1)),
			kv("marker0", jstr("c0")),
			kv("marker1", jstr("c1")),
			kv("oob_is_none", jbool(true)),
			kv("readd_index", jint(0)),
		),
		jobj(
			kv("index0", jint(int64(index0))),
			kv("index1", jint(int64(index1))),
			kv("marker0", jstr(marker0)),
			kv("marker1", jstr(marker1)),
			kv("oob_is_none", jbool(oobIsNone)),
			kv("readd_index", jint(int64(readdIndex))),
		))
}

func checkIsolateDataOnce(t *testing.T) obs {
	t.Helper()
	var intIndex, strIndex int
	blob := func() *gov8.StartupData {
		creator, err := gov8.NewSnapshotCreator()
		if err != nil {
			t.Fatalf("NewSnapshotCreator: %v", err)
		}
		iso := creator.Isolate()
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		if err := creator.SetDefaultContext(ctx); err != nil {
			t.Fatalf("SetDefaultContext: %v", err)
		}
		intData, err := scope.Int32(41)
		if err != nil {
			t.Fatalf("Int32: %v", err)
		}
		if intIndex, err = creator.AddIsolateData(intData); err != nil {
			t.Fatalf("AddIsolateData: %v", err)
		}
		strData, err := scope.NewString("iso-data")
		if err != nil {
			t.Fatalf("NewString: %v", err)
		}
		if strIndex, err = creator.AddIsolateData(strData); err != nil {
			t.Fatalf("AddIsolateData str: %v", err)
		}
		_ = scope.Close()
		_ = ctx.Close()
		b, err := creator.CreateBlob(gov8.FunctionCodeClear)
		if err != nil {
			t.Fatalf("CreateBlob: %v", err)
		}
		return b
	}()
	defer func() { _ = blob.Release() }()

	r := newConsumerFromBlob(t, blob)
	defer r.close(t)

	first, firstErr := r.scope.GetIsolateDataFromSnapshotOnce(intIndex, gov8.SnapshotDataValue)
	intValue := int64(-1)
	if firstErr == nil {
		if n, ok, err := first.IntegerValue(r.ctx); err == nil && ok {
			intValue = n
		}
	}
	secondKind := resultKind(func() error {
		_, err := r.scope.GetIsolateDataFromSnapshotOnce(intIndex, gov8.SnapshotDataValue)
		return err
	}())

	strValue, strErr := r.scope.GetIsolateDataFromSnapshotOnce(strIndex, gov8.SnapshotDataString)
	text := ""
	if strErr == nil {
		text, _ = strValue.StringValue()
	}
	oobKind := resultKind(func() error {
		_, err := r.scope.GetIsolateDataFromSnapshotOnce(9, gov8.SnapshotDataValue)
		return err
	}())

	return wantGot("snapshot/isolate_data_once",
		jobj(
			kv("int_index", jint(0)),
			kv("int_value", jint(41)),
			kv("second_read_kind", jstr("NoData")),
			kv("str_index", jint(1)),
			kv("str_value", jstr("iso-data")),
			kv("oob_kind", jstr("NoData")),
		),
		jobj(
			kv("int_index", jint(int64(intIndex))),
			kv("int_value", jint(intValue)),
			kv("second_read_kind", jstr(secondKind)),
			kv("str_index", jint(int64(strIndex))),
			kv("str_value", jstr(text)),
			kv("oob_kind", jstr(oobKind)),
		))
}

func checkContextDataOnceAndBadType(t *testing.T) obs {
	t.Helper()
	var valueIndex, textIndex, wrongTypeIndex int
	blob := func() *gov8.StartupData {
		creator, err := gov8.NewSnapshotCreator()
		if err != nil {
			t.Fatalf("NewSnapshotCreator: %v", err)
		}
		iso := creator.Isolate()
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		add := func(v gov8.Value) int {
			t.Helper()
			idx, err := creator.AddContextData(ctx, v)
			if err != nil {
				t.Fatalf("AddContextData: %v", err)
			}
			return idx
		}
		intData, err := scope.Int32(5)
		if err != nil {
			t.Fatalf("Int32: %v", err)
		}
		valueIndex = add(intData)
		strData, err := scope.NewString("ctx-data")
		if err != nil {
			t.Fatalf("NewString: %v", err)
		}
		textIndex = add(strData)
		wrongData, err := scope.Int32(6)
		if err != nil {
			t.Fatalf("Int32 wrong: %v", err)
		}
		wrongTypeIndex = add(wrongData)
		if err := creator.SetDefaultContext(ctx); err != nil {
			t.Fatalf("SetDefaultContext: %v", err)
		}
		_ = scope.Close()
		_ = ctx.Close()
		b, err := creator.CreateBlob(gov8.FunctionCodeClear)
		if err != nil {
			t.Fatalf("CreateBlob: %v", err)
		}
		return b
	}()
	defer func() { _ = blob.Release() }()

	r := newConsumerFromBlob(t, blob)
	defer r.close(t)

	value, valueErr := r.scope.GetContextDataFromSnapshotOnce(r.ctx, valueIndex, gov8.SnapshotDataValue)
	intValue := int64(-1)
	if valueErr == nil {
		if n, ok, err := value.IntegerValue(r.ctx); err == nil && ok {
			intValue = n
		}
	}
	secondKind := resultKind(func() error {
		_, err := r.scope.GetContextDataFromSnapshotOnce(r.ctx, valueIndex, gov8.SnapshotDataValue)
		return err
	}())

	textValue, textErr := r.scope.GetContextDataFromSnapshotOnce(r.ctx, textIndex, gov8.SnapshotDataString)
	text := ""
	if textErr == nil {
		text, _ = textValue.StringValue()
	}
	// Wrongly typed request over a still-filled slot. Pinned upstream
	// caveat: the raw data is fetched from the snapshot before the
	// downcast, so a BadType request consumes the slot; the follow-up
	// correctly typed read observes the consequence.
	badKind := resultKind(func() error {
		_, err := r.scope.GetContextDataFromSnapshotOnce(r.ctx, wrongTypeIndex, gov8.SnapshotDataPrivate)
		return err
	}())
	afterBadKind := resultKind(func() error {
		_, err := r.scope.GetContextDataFromSnapshotOnce(r.ctx, wrongTypeIndex, gov8.SnapshotDataValue)
		return err
	}())

	return wantGot("snapshot/context_data_once_and_badtype",
		jobj(
			kv("int_value", jint(5)),
			kv("second_read_kind", jstr("NoData")),
			kv("str_value", jstr("ctx-data")),
			kv("bad_request_kind", jstr("BadType")),
			kv("after_bad_request_kind", jstr("NoData")),
		),
		jobj(
			kv("int_value", jint(intValue)),
			kv("second_read_kind", jstr(secondKind)),
			kv("str_value", jstr(text)),
			kv("bad_request_kind", jstr(badKind)),
			kv("after_bad_request_kind", jstr(afterBadKind)),
		))
}

// --- global handle checks ---------------------------------------------------------

func newObject(t *testing.T, r *runtime) gov8.Value {
	t.Helper()
	v, err := r.eval(t, "({})")
	if err != nil {
		t.Fatalf("new object: %v", err)
	}
	return v
}

// newObjectGlobal roots a fresh object and closes its local scope, leaving
// the global as the only strong reference (mirrors the oracle, whose
// HandleScope ends before collection is forced).
func newObjectGlobal(t *testing.T, iso *gov8.Isolate, ctx *gov8.Context) *gov8.Global {
	t.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	r := &runtime{iso: iso, ctx: ctx, scope: scope}
	v := newObject(t, r)
	g, err := gov8.NewGlobal(scope, v)
	if err != nil {
		t.Fatalf("NewGlobal: %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}
	return g
}

func checkGlobalIntoRawRoundtrip(t *testing.T) obs {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	r := &runtime{iso: iso, ctx: ctx, scope: scope}

	original, err := gov8.NewGlobal(scope, newObject(t, r))
	if err != nil {
		t.Fatalf("NewGlobal: %v", err)
	}
	keeper, err := original.Clone() // new global cell, same object
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	distinct, err := gov8.NewGlobal(scope, newObject(t, r))
	if err != nil {
		t.Fatalf("NewGlobal distinct: %v", err)
	}
	raw, err := original.IntoRaw()
	if err != nil {
		t.Fatalf("IntoRaw: %v", err)
	}
	_ = scope.Close()
	_ = ctx.Close()

	// Re-adopt the raw cell outside any scope (from_raw takes the isolate).
	restored, err := gov8.GlobalFromRaw(iso, raw)
	if err != nil {
		t.Fatalf("GlobalFromRaw: %v", err)
	}
	roundtripEqual, err := restored.Equal(keeper)
	if err != nil {
		t.Fatalf("roundtrip Equal: %v", err)
	}
	distinctUnequal := false
	if d, err := restored.Equal(distinct); err != nil {
		t.Fatalf("distinct Equal: %v", err)
	} else {
		distinctUnequal = !d
	}

	_ = restored.Close()
	_ = keeper.Close()
	_ = distinct.Close()

	return wantGot("handle/global_into_raw_roundtrip",
		jobj(
			kv("roundtrip_equal", jbool(true)),
			kv("distinct_unequal", jbool(true)),
		),
		jobj(
			kv("roundtrip_equal", jbool(roundtripEqual)),
			kv("distinct_unequal", jbool(distinctUnequal)),
		))
}

func checkGlobalEqCrossIsolate(t *testing.T) obs {
	t.Helper()
	isoA, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate A: %v", err)
	}
	defer func() { _ = isoA.Close() }()
	ctxA, err := isoA.NewContext()
	if err != nil {
		t.Fatalf("NewContext A: %v", err)
	}
	globalA := newObjectGlobal(t, isoA, ctxA)
	_ = ctxA.Close()

	// The cross-isolate comparison must run while both isolates are alive,
	// so it sits between the two isolate lifetimes.
	crossIsolateEqual := false
	func() {
		isoB, err := gov8.NewIsolate()
		if err != nil {
			t.Fatalf("NewIsolate B: %v", err)
		}
		defer func() { _ = isoB.Close() }()
		ctxB, err := isoB.NewContext()
		if err != nil {
			t.Fatalf("NewContext B: %v", err)
		}
		globalB := newObjectGlobal(t, isoB, ctxB)
		_ = ctxB.Close()
		cross, err := globalA.Equal(globalB)
		if err != nil {
			t.Fatalf("cross Equal: %v", err)
		}
		crossIsolateEqual = cross
	}()

	ownLocalEqual := false
	func() {
		ctx, err := isoA.NewContext()
		if err != nil {
			t.Fatalf("reopen NewContext: %v", err)
		}
		defer func() { _ = ctx.Close() }()
		scope, err := isoA.NewScope()
		if err != nil {
			t.Fatalf("reopen NewScope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		reopened, err := globalA.ToLocal(scope)
		if err != nil {
			t.Fatalf("ToLocal: %v", err)
		}
		g2, err := gov8.NewGlobal(scope, reopened)
		if err != nil {
			t.Fatalf("reopen NewGlobal: %v", err)
		}
		eq, err := globalA.Equal(g2)
		if err != nil {
			t.Fatalf("own-local Equal: %v", err)
		}
		ownLocalEqual = eq
		_ = globalA.Close()
		_ = g2.Close()
	}()

	return wantGot("handle/global_eq_cross_isolate",
		jobj(
			kv("cross_isolate_equal", jbool(false)),
			kv("own_local_equal", jbool(true)),
		),
		jobj(
			kv("cross_isolate_equal", jbool(crossIsolateEqual)),
			kv("own_local_equal", jbool(ownLocalEqual)),
		))
}

// checkGlobalDropAfterIsolateDispose mirrors the pinned check: a Global may
// be closed after its host isolate was disposed; the Go wrapper takes the
// documented no-op path (the pinned Drop detects the disposed isolate and
// does nothing). No panic, no access afterwards.
func checkGlobalDropAfterIsolateDispose(t *testing.T) obs {
	t.Helper()
	noPanic := true
	func() {
		defer func() {
			if r := recover(); r != nil {
				noPanic = false
			}
		}()
		var global *gov8.Global
		func() {
			iso, err := gov8.NewIsolate()
			if err != nil {
				t.Fatalf("NewIsolate: %v", err)
			}
			ctx, err := iso.NewContext()
			if err != nil {
				t.Fatalf("NewContext: %v", err)
			}
			global = newObjectGlobal(t, iso, ctx)
			_ = ctx.Close()
			_ = iso.Close() // isolate disposed here, global outlives it
		}()
		_ = global.Close() // must be a silent no-op, not a panic
	}()
	return wantGot("handle/global_drop_after_isolate_dispose", jbool(true), jbool(noPanic))
}

// --- weak handle checks ------------------------------------------------------------

func checkWeakFinalizerFiresAfterForcedGC(t *testing.T) obs {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	events := []string(nil)

	strong := newObjectGlobal(t, iso, ctx)
	weak, err := strong.NewWeakWithFinalizer(func(i *gov8.Isolate) {
		events = append(events, "finalizer")
	})
	if err != nil {
		t.Fatalf("NewWeakWithFinalizer: %v", err)
	}

	aliveBeforeGC := false
	if empty, _ := weak.IsEmpty(); !empty {
		aliveBeforeGC = true
	}
	_ = strong.Close()
	_ = iso.LowMemoryNotification()
	_ = iso.LowMemoryNotification()

	collected, _ := weak.IsEmpty()
	_, resurrectOK, _ := weak.ToGlobal()
	fired := false
	for _, e := range events {
		if e == "finalizer" {
			fired = true
		}
	}
	_ = weak.Close()
	_ = ctx.Close()

	return wantGot("handle/weak_finalizer_fires_after_forced_gc",
		jobj(
			kv("alive_before_gc", jbool(true)),
			kv("collected_after_gc", jbool(true)),
			kv("resurrect_is_none", jbool(true)),
			kv("finalizer_fired", jbool(true)),
		),
		jobj(
			kv("alive_before_gc", jbool(aliveBeforeGC)),
			kv("collected_after_gc", jbool(collected)),
			kv("resurrect_is_none", jbool(!resurrectOK)),
			kv("finalizer_fired", jbool(fired)),
		))
}

// checkWeakGuaranteedFinalizerRunsByTeardown mirrors the pinned check: not
// necessarily invoked by a forced GC, but guaranteed to run before the
// isolate is destroyed. The pinned crate drains its finalizer map during
// the isolate Drop; the Go analog is the explicit drain immediately before
// Isolate.Close (documented deviation: same guarantee, explicit step).
func checkWeakGuaranteedFinalizerRunsByTeardown(t *testing.T) obs {
	t.Helper()
	events := []string(nil)
	func() {
		iso, err := gov8.NewIsolate()
		if err != nil {
			t.Fatalf("NewIsolate: %v", err)
		}
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		strong := newObjectGlobal(t, iso, ctx)
		weak, err := strong.NewWeakWithGuaranteedFinalizer(func() {
			events = append(events, "guaranteed")
		})
		if err != nil {
			t.Fatalf("NewWeakWithGuaranteedFinalizer: %v", err)
		}
		_ = strong.Close() // last strong reference gone; the weak keeps its cell
		_ = weak           // deliberately left open: the drain consumes it
		if err := gov8.DrainGuaranteedWeakFinalizers(iso); err != nil {
			t.Errorf("DrainGuaranteedWeakFinalizers: %v", err)
		}
		_ = ctx.Close()
		_ = iso.Close()
	}()
	firedAfterTeardown := false
	for _, e := range events {
		if e == "guaranteed" {
			firedAfterTeardown = true
		}
	}
	return wantGot("handle/weak_guaranteed_finalizer_runs_by_teardown",
		jbool(true), jbool(firedAfterTeardown))
}

func checkWeakDropCancelsFinalizer(t *testing.T) obs {
	t.Helper()
	events := []string(nil)
	func() {
		iso, err := gov8.NewIsolate()
		if err != nil {
			t.Fatalf("NewIsolate: %v", err)
		}
		defer func() { _ = iso.Close() }()
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		strong := newObjectGlobal(t, iso, ctx)
		weak, err := strong.NewWeakWithFinalizer(func(i *gov8.Isolate) {
			events = append(events, "cancelled-should-not-run")
		})
		if err != nil {
			t.Fatalf("NewWeakWithFinalizer: %v", err)
		}
		_ = weak.Close() // cancels the finalizer; the object is still strongly held
		_ = iso.LowMemoryNotification()
		_ = iso.LowMemoryNotification()
		if len(events) != 0 {
			t.Fatalf("cancelled finalizer fired: %v", events)
		}
		_ = strong.Close()
		_ = ctx.Close()
	}()
	stillEmpty := len(events) == 0
	return wantGot("handle/weak_drop_cancels_finalizer", jbool(true), jbool(stillEmpty))
}

func checkWeakEqualityAndClone(t *testing.T) obs {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()

	strong := newObjectGlobal(t, iso, ctx)
	weak, err := strong.NewWeak()
	if err != nil {
		t.Fatalf("NewWeak: %v", err)
	}
	weakClone, err := weak.Clone() // documented: clone carries no finalizer
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	weakEqualsClone, err := weak.EqualWeak(weakClone)
	if err != nil {
		t.Fatalf("EqualWeak clone: %v", err)
	}
	weakEqualsGlobal, err := weak.EqualGlobal(strong)
	if err != nil {
		t.Fatalf("EqualGlobal: %v", err)
	}
	toLocalSome := false
	func() {
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("scope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		_, ok, err := weak.ToLocal(scope)
		if err != nil {
			t.Fatalf("ToLocal: %v", err)
		}
		toLocalSome = ok
	}()

	_ = strong.Close()
	_ = iso.LowMemoryNotification()
	_ = iso.LowMemoryNotification()

	collectedEmpty, _ := weak.IsEmpty()
	collectedCloneEmpty, _ := weakClone.IsEmpty()
	collectedToLocalNone := false
	func() {
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("scope 2: %v", err)
		}
		defer func() { _ = scope.Close() }()
		_, ok, err := weak.ToLocal(scope)
		if err != nil {
			t.Fatalf("ToLocal 2: %v", err)
		}
		collectedToLocalNone = !ok
	}()

	empty1, err := iso.EmptyWeak()
	if err != nil {
		t.Fatalf("EmptyWeak: %v", err)
	}
	empty2, err := iso.EmptyWeak()
	if err != nil {
		t.Fatalf("EmptyWeak 2: %v", err)
	}
	emptyEqualsEmpty, err := empty1.EqualWeak(empty2)
	if err != nil {
		t.Fatalf("empty EqualWeak: %v", err)
	}

	_ = weak.Close()
	_ = weakClone.Close()

	return wantGot("handle/weak_equality_and_clone",
		jobj(
			kv("weak_equals_clone", jbool(true)),
			kv("weak_equals_global", jbool(true)),
			kv("to_local_some", jbool(true)),
			kv("collected_empty", jbool(true)),
			kv("collected_to_local_none", jbool(true)),
			kv("collected_clone_empty", jbool(true)),
			kv("empty_equals_empty", jbool(true)),
		),
		jobj(
			kv("weak_equals_clone", jbool(weakEqualsClone)),
			kv("weak_equals_global", jbool(weakEqualsGlobal)),
			kv("to_local_some", jbool(toLocalSome)),
			kv("collected_empty", jbool(collectedEmpty)),
			kv("collected_to_local_none", jbool(collectedToLocalNone)),
			kv("collected_clone_empty", jbool(collectedCloneEmpty)),
			kv("empty_equals_empty", jbool(emptyEqualsEmpty)),
		))
}

// --- termination check -----------------------------------------------------------

// termRequestCb records the termination flag around its own request. The
// pinned runner writes the flags to JS globals because Rust captures are
// unavailable in non-capturing callbacks; Go records both the closure
// values AND the globals for parity.
func termRequestCb(t *testing.T, r *runtime, before, after *bool) gov8.FunctionCallback {
	return func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		iso := cs.Isolate()
		sc := cs.Scope()

		flagBefore, err := iso.IsExecutionTerminating()
		if err != nil {
			t.Errorf("callback IsExecutionTerminating: %v", err)
			return
		}
		if err := iso.TerminateExecution(); err != nil {
			t.Errorf("callback TerminateExecution: %v", err)
			return
		}
		flagAfter, err := iso.IsExecutionTerminating()
		if err != nil {
			t.Errorf("callback IsExecutionTerminating 2: %v", err)
			return
		}
		*before = flagBefore
		*after = flagAfter

		global, err := r.ctx.GlobalObject(sc)
		if err != nil {
			t.Errorf("callback GlobalObject: %v", err)
			return
		}
		b1, _ := sc.Boolean(flagBefore)
		if _, err := global.SetByName(sc, r.ctx, "__termFlagBefore", b1); err != nil {
			t.Errorf("callback set __termFlagBefore: %v", err)
		}
		b2, _ := sc.Boolean(flagAfter)
		if _, err := global.SetByName(sc, r.ctx, "__termFlagAfter", b2); err != nil {
			t.Errorf("callback set __termFlagAfter: %v", err)
		}
	}
}

func checkTerminateRequestAndCancelDuringJS(t *testing.T) obs {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	handle := iso.ThreadSafeHandle()

	r := &runtime{}
	r.iso = iso
	r.ctx, err = iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = r.ctx.Close() }()
	r.scope, err = iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = r.scope.Close() }()

	var flagBefore, flagAfter bool
	request, err := iso.NewFunction(r.scope, r.ctx, termRequestCb(t, r, &flagBefore, &flagAfter), nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(r.scope, r.ctx, "__requestTerminate", request.Value); err != nil {
		t.Fatalf("seed __requestTerminate: %v", err)
	}

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	// The termination request is delivered at the next interrupt check
	// (loop back-edge), not synchronously inside the native callback, so
	// the script must keep running for the request to land.
	ranOK := false
	if script, err := r.ctx.Compile(r.scope, "__requestTerminate(); while (true) { }", tc); err != nil {
		t.Fatalf("Compile: %v", err)
	} else {
		_, runErr := script.Run(r.scope, tc)
		_ = script.Close()
		ranOK = runErr == nil
	}
	hasCaught, _ := tc.HasCaught()
	canContinue, _ := tc.CanContinue()
	hasTerminated, _ := tc.HasTerminated()

	// The TryCatch observes the termination; closing it clears the pending
	// termination exception from the isolate (the pinned runner's TryCatch
	// is dropped before this read, and v8's ~TryCatch/Reset performs the
	// same clearing) — that is why the flag reads false below.
	if err := tc.Close(); err != nil {
		t.Fatalf("tc.Close: %v", err)
	}
	// Once the termination exception has fully unwound to the embedder, V8
	// has already cleared the terminate flag; the durable post-abort
	// observable is the TryCatch's terminated state.
	flagAfterAbort, _ := iso.IsExecutionTerminating()
	cancelled := handle.CancelTerminateExecution()
	idleAgain := handle.IsExecutionTerminating()
	flagBeforeText := evalText(t, r, "String(__termFlagBefore)")
	flagAfterText := evalText(t, r, "String(__termFlagAfter)")
	next := evalText(t, r, "String(7 * 6)")

	if next != "42" {
		t.Fatalf("isolate must be reusable after cancellation: %q", next)
	}

	return wantGot("terminate/request_and_cancel_during_js",
		jobj(
			kv("flag_before_request", jstr("false")),
			kv("flag_after_request", jstr("false")),
			kv("flag_after_abort", jbool(false)),
			kv("ran_ok", jbool(false)),
			kv("has_caught", jbool(true)),
			kv("can_continue", jbool(false)),
			kv("has_terminated", jbool(true)),
			kv("cancel_ok", jbool(true)),
			kv("idle_again", jbool(false)),
			kv("reused_result", jstr("42")),
		),
		jobj(
			kv("flag_before_request", jstr(flagBeforeText)),
			kv("flag_after_request", jstr(flagAfterText)),
			kv("flag_after_abort", jbool(flagAfterAbort)),
			kv("ran_ok", jbool(ranOK)),
			kv("has_caught", jbool(hasCaught)),
			kv("can_continue", jbool(canContinue)),
			kv("has_terminated", jbool(hasTerminated)),
			kv("cancel_ok", jbool(cancelled)),
			kv("idle_again", jbool(idleAgain)),
			kv("reused_result", jstr(next)),
		))
}

// --- registry ------------------------------------------------------------------

type checkFn func(t *testing.T) obs

type snapshotCheck struct {
	id string
	fn checkFn
}

// allSnapshotChecks is the fixed oracle registry order
// (conformance-snapshots.rs CHECKS), all 15 checks.
func allSnapshotChecks() []snapshotCheck {
	return []snapshotCheck{
		// snapshot creation and startup data
		{"snapshot/create_blob_policies", checkCreateBlobPolicies},
		{"snapshot/startup_data_predicates", checkStartupDataPredicates},
		// snapshot consumption
		{"snapshot/default_context_create_params_roundtrip", checkDefaultContextCreateParamsRoundtrip},
		{"snapshot/chained_roundtrip", checkChainedSnapshotRoundtrip},
		{"snapshot/add_context_from_snapshot", checkAddContextFromSnapshot},
		{"snapshot/isolate_data_once", checkIsolateDataOnce},
		{"snapshot/context_data_once_and_badtype", checkContextDataOnceAndBadType},
		// global handle semantics
		{"handle/global_into_raw_roundtrip", checkGlobalIntoRawRoundtrip},
		{"handle/global_eq_cross_isolate", checkGlobalEqCrossIsolate},
		{"handle/global_drop_after_isolate_dispose", checkGlobalDropAfterIsolateDispose},
		// weak handle semantics
		{"handle/weak_finalizer_fires_after_forced_gc", checkWeakFinalizerFiresAfterForcedGC},
		{"handle/weak_guaranteed_finalizer_runs_by_teardown", checkWeakGuaranteedFinalizerRunsByTeardown},
		{"handle/weak_drop_cancels_finalizer", checkWeakDropCancelsFinalizer},
		{"handle/weak_equality_and_clone", checkWeakEqualityAndClone},
		// thread-safe isolate handle
		{"terminate/request_and_cancel_during_js", checkTerminateRequestAndCancelDuringJS},
	}
}

// snapshotCheckIDs lists the registry ids in the same order.
func snapshotCheckIDs() []string {
	ids := make([]string, 0, 15)
	for _, c := range allSnapshotChecks() {
		ids = append(ids, c.id)
	}
	return ids
}
