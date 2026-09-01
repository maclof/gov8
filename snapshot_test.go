//go:build windows && amd64

package gov8_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	gov8 "gov8"
)

// Snapshot / startup-data tests, mirroring the pinned Rust oracle's
// snapshot checks (rust-oracle/src/bin/conformance-snapshots.rs, registry
// prefix snapshot/). Values observed from the engine; nothing hardcoded
// except the documented expectations also pinned by the fixture.

// snapMakeBlob builds a snapshot blob from a fresh creator isolate whose
// default context defines globalThis.a = 7 and a callable globalThis.f.
func snapMakeBlob(t *testing.T, keep gov8.FunctionCodeHandling) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatalf("NewSnapshotCreator: %v", err)
	}
	iso := creator.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
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
	blob, err := creator.CreateBlob(keep)
	if err != nil {
		t.Fatalf("CreateBlob(%v): %v", keep, err)
	}
	return blob
}

func TestSnapshotCreateBlobPolicies(t *testing.T) {
	for _, keep := range []gov8.FunctionCodeHandling{gov8.FunctionCodeClear, gov8.FunctionCodeKeep} {
		blob := snapMakeBlob(t, keep)
		if blob.IsEmpty() {
			t.Fatalf("keep=%v: blob is empty", keep)
		}
		if !blob.IsValid() {
			t.Fatalf("keep=%v: blob not valid", keep)
		}
		if err := blob.Release(); err != nil {
			t.Errorf("Release: %v", err)
		}
	}
}

// TestSnapshotStartupDataGuard pins the fatal-call guard: data shorter than
// the snapshot version header is answered invalid WITHOUT touching the
// engine (the pinned crate aborts the process there), and creation paths
// reject such data with errors instead of aborting.
func TestSnapshotStartupDataGuard(t *testing.T) {
	if gov8.StartupDataFromBytes(nil).IsValid() {
		t.Fatal("nil-byte blob must be invalid")
	}
	if !gov8.StartupDataFromBytes(nil).IsEmpty() {
		t.Fatal("nil-byte blob must be empty")
	}
	short := gov8.StartupDataFromBytes(make([]byte, 80))
	if short.IsValid() {
		t.Fatal("80-byte blob must be invalid (version header bound)")
	}
	if gov8.StartupDataFromBytes(make([]byte, 81)).IsEmpty() {
		t.Fatal("sanity: 81-byte blob is not empty")
	}

	// Creation paths refuse undersized blobs with errors, never fatals.
	if _, err := gov8.NewIsolateFromSnapshot(short); err == nil {
		t.Fatal("NewIsolateFromSnapshot with undersized blob must fail")
	}
	creator, err := gov8.NewSnapshotCreatorFromExistingSnapshot(short)
	if err == nil {
		_ = creator.Close()
		t.Fatal("NewSnapshotCreatorFromExistingSnapshot with undersized blob must fail")
	}
}

func TestSnapshotDefaultContextRoundtrip(t *testing.T) {
	blob := snapMakeBlob(t, gov8.FunctionCodeClear)
	defer func() { _ = blob.Release() }()

	iso, err := gov8.NewIsolateFromSnapshot(blob)
	if err != nil {
		t.Fatalf("NewIsolateFromSnapshot: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()

	if got := snapEvalText(t, ctx, scope, "String(a === 7)"); got != "true" {
		t.Fatalf("String(a === 7) = %q, want true", got)
	}
	if got := snapEvalText(t, ctx, scope, "String(f())"); got != "14" {
		t.Fatalf("String(f()) = %q, want 14", got)
	}
	f, ok, err := snapEval(t, ctx, scope, "f")
	if err != nil || !ok {
		t.Fatalf("eval f: ok=%v err=%v", ok, err)
	}
	if isFn, _ := f.IsFunction(); !isFn {
		t.Fatal("snapshotted f must be a function")
	}
	if err := blob.Release(); err == nil {
		t.Fatal("Release must fail while the snapshot isolate is open")
	}
}

func TestSnapshotChainedRoundtrip(t *testing.T) {
	first := snapMakeBlob(t, gov8.FunctionCodeKeep)
	defer func() { _ = first.Release() }()

	creator, err := gov8.NewSnapshotCreatorFromExistingSnapshot(first)
	if err != nil {
		t.Fatalf("NewSnapshotCreatorFromExistingSnapshot: %v", err)
	}
	iso := creator.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	if got := snapEvalText(t, ctx, scope, "String(a)"); got != "7" {
		t.Fatalf("inherited String(a) = %q, want 7", got)
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
	second, err := creator.CreateBlob(gov8.FunctionCodeClear)
	if err != nil {
		t.Fatalf("CreateBlob chained: %v", err)
	}
	// The creator isolate was consumed by CreateBlob.
	if err := creator.Close(); err == nil {
		t.Error("creator must be consumed by CreateBlob")
	}

	defer func() { _ = second.Release() }()
	if !second.IsValid() {
		t.Fatal("chained blob must be valid")
	}
	iso2, err := gov8.NewIsolateFromSnapshot(second)
	if err != nil {
		t.Fatalf("NewIsolateFromSnapshot chained: %v", err)
	}
	defer func() { _ = iso2.Close() }()
	ctx2, err := iso2.NewContext()
	if err != nil {
		t.Fatalf("NewContext 2: %v", err)
	}
	defer func() { _ = ctx2.Close() }()
	scope2, err := iso2.NewScope()
	if err != nil {
		t.Fatalf("NewScope 2: %v", err)
	}
	defer func() { _ = scope2.Close() }()
	if got := snapEvalText(t, ctx2, scope2, "String(a)"); got != "7" {
		t.Fatalf("chained String(a) = %q, want 7", got)
	}
	if got := snapEvalText(t, ctx2, scope2, "String(b)"); got != "8" {
		t.Fatalf("chained String(b) = %q, want 8", got)
	}
}

func TestSnapshotAddContextFromSnapshot(t *testing.T) {
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatalf("NewSnapshotCreator: %v", err)
	}
	iso := creator.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	// Upstream caveat: create_blob CHECK-fails without a default context
	// even when only add_context is used, so a minimal default context is
	// always part of the blob.
	defaultCtx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext default: %v", err)
	}
	if err := creator.SetDefaultContext(defaultCtx); err != nil {
		t.Fatalf("SetDefaultContext: %v", err)
	}

	addCtx := func(marker string) (*gov8.Context, int) {
		t.Helper()
		c, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext added: %v", err)
		}
		s, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope added: %v", err)
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
	ctx0, index0 := addCtx("c0")
	ctx1, index1 := addCtx("c1")
	_ = scope.Close()
	_ = defaultCtx.Close()
	_ = ctx0.Close()
	_ = ctx1.Close()
	blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
	if err != nil {
		t.Fatalf("CreateBlob: %v", err)
	}
	defer func() { _ = blob.Release() }()

	// Consume in a creator-from-existing isolate so the re-add index can be
	// observed too; that isolate is consumed by CreateBlob at the end.
	consumer, err := gov8.NewSnapshotCreatorFromExistingSnapshot(blob)
	if err != nil {
		t.Fatalf("NewSnapshotCreatorFromExistingSnapshot: %v", err)
	}
	cIso := consumer.Isolate()
	cScope, err := cIso.NewScope()
	if err != nil {
		t.Fatalf("consumer NewScope: %v", err)
	}
	cDefault, err := cIso.NewContext()
	if err != nil {
		t.Fatalf("consumer NewContext: %v", err)
	}
	if err := consumer.SetDefaultContext(cDefault); err != nil {
		t.Fatalf("consumer SetDefaultContext: %v", err)
	}

	recoverCtx := func(idx int) string {
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
	marker0 := recoverCtx(index0)
	marker1 := recoverCtx(index1)

	oobIsNone := false
	if _, ok, err := cScope.ContextFromSnapshot(7); err == nil && !ok {
		oobIsNone = true
	}

	// A recovered context can be re-added at the same index.
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

	if index0 != 0 || index1 != 1 {
		t.Errorf("indices = %d, %d; want 0, 1", index0, index1)
	}
	if marker0 != "c0" || marker1 != "c1" {
		t.Errorf("markers = %q, %q; want c0, c1", marker0, marker1)
	}
	if !oobIsNone {
		t.Error("out-of-range index must recover no context")
	}
	if readdIndex != 0 {
		t.Errorf("re-add index = %d; want 0", readdIndex)
	}
}

func TestSnapshotIsolateDataOnce(t *testing.T) {
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatalf("NewSnapshotCreator: %v", err)
	}
	iso := creator.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	one, err := scope.Int32(41)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	intIndex, err := creator.AddIsolateData(one)
	if err != nil {
		t.Fatalf("AddIsolateData: %v", err)
	}
	str, err := scope.NewString("iso-data")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	strIndex, err := creator.AddIsolateData(str)
	if err != nil {
		t.Fatalf("AddIsolateData str: %v", err)
	}
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatalf("SetDefaultContext: %v", err)
	}
	_ = scope.Close()
	_ = ctx.Close()
	blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
	if err != nil {
		t.Fatalf("CreateBlob: %v", err)
	}
	defer func() { _ = blob.Release() }()

	cIso, err := gov8.NewIsolateFromSnapshot(blob)
	if err != nil {
		t.Fatalf("NewIsolateFromSnapshot: %v", err)
	}
	defer func() { _ = cIso.Close() }()
	cCtx, err := cIso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = cCtx.Close() }()
	cScope, err := cIso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = cScope.Close() }()

	first, err := cScope.GetIsolateDataFromSnapshotOnce(intIndex, gov8.SnapshotDataValue)
	if err != nil {
		t.Fatalf("first retrieval: %v", err)
	}
	intValue, ok, err := first.IntegerValue(cCtx)
	if err != nil || !ok {
		t.Fatalf("integer_value = %d, %v, %v", intValue, ok, err)
	}
	if _, err := cScope.GetIsolateDataFromSnapshotOnce(intIndex, gov8.SnapshotDataValue); err == nil {
		t.Fatal("second retrieval of the same index must yield NoData")
	} else if !snapIsNoData(err) {
		t.Fatalf("second retrieval error = %v, want NoData", err)
	}

	sv, err := cScope.GetIsolateDataFromSnapshotOnce(strIndex, gov8.SnapshotDataString)
	if err != nil {
		t.Fatalf("string retrieval: %v", err)
	}
	text, err := sv.StringValue()
	if err != nil {
		t.Fatalf("StringValue: %v", err)
	}
	if _, err := cScope.GetIsolateDataFromSnapshotOnce(9, gov8.SnapshotDataValue); !snapIsNoData(err) {
		t.Fatalf("out-of-range error = %v, want NoData", err)
	}

	if intIndex != 0 || strIndex != 1 {
		t.Errorf("indices = %d, %d; want 0, 1", intIndex, strIndex)
	}
	if intValue != 41 {
		t.Errorf("int_value = %d, want 41", intValue)
	}
	if text != "iso-data" {
		t.Errorf("str_value = %q, want iso-data", text)
	}
}

func TestSnapshotContextDataOnceAndBadType(t *testing.T) {
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatalf("NewSnapshotCreator: %v", err)
	}
	iso := creator.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	add := func(n int32) int {
		t.Helper()
		v, err := scope.Int32(n)
		if err != nil {
			t.Fatalf("Int32: %v", err)
		}
		idx, err := creator.AddContextData(ctx, v)
		if err != nil {
			t.Fatalf("AddContextData: %v", err)
		}
		return idx
	}
	valueIndex := add(5)
	textIndex := add(6) // overwritten below with a string at its own index
	{
		str, err := scope.NewString("ctx-data")
		if err != nil {
			t.Fatalf("NewString: %v", err)
		}
		textIndex, err = creator.AddContextData(ctx, str)
		if err != nil {
			t.Fatalf("AddContextData str: %v", err)
		}
	}
	wrongTypeIndex := add(6)
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatalf("SetDefaultContext: %v", err)
	}
	_ = scope.Close()
	_ = ctx.Close()
	blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
	if err != nil {
		t.Fatalf("CreateBlob: %v", err)
	}
	defer func() { _ = blob.Release() }()

	cIso, err := gov8.NewIsolateFromSnapshot(blob)
	if err != nil {
		t.Fatalf("NewIsolateFromSnapshot: %v", err)
	}
	defer func() { _ = cIso.Close() }()
	cCtx, err := cIso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = cCtx.Close() }()
	cScope, err := cIso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = cScope.Close() }()

	value, err := cScope.GetContextDataFromSnapshotOnce(cCtx, valueIndex, gov8.SnapshotDataValue)
	if err != nil {
		t.Fatalf("value retrieval: %v", err)
	}
	intValue, _, _ := value.IntegerValue(cCtx)
	if _, err := cScope.GetContextDataFromSnapshotOnce(cCtx, valueIndex, gov8.SnapshotDataValue); !snapIsNoData(err) {
		t.Fatalf("second read = %v, want NoData", err)
	}
	textValue, err := cScope.GetContextDataFromSnapshotOnce(cCtx, textIndex, gov8.SnapshotDataString)
	if err != nil {
		t.Fatalf("text retrieval: %v", err)
	}
	text, _ := textValue.StringValue()

	// Wrongly typed request over a still-filled slot: the raw data is
	// fetched before the downcast, so the BadType request consumes the slot
	// (upstream caveat) and the follow-up correctly typed read is NoData.
	if _, err := cScope.GetContextDataFromSnapshotOnce(cCtx, wrongTypeIndex, gov8.SnapshotDataPrivate); !snapIsBadType(err) {
		t.Fatalf("bad request = %v, want BadType", err)
	}
	if _, err := cScope.GetContextDataFromSnapshotOnce(cCtx, wrongTypeIndex, gov8.SnapshotDataValue); !snapIsNoData(err) {
		t.Fatalf("after bad request = %v, want NoData", err)
	}

	if intValue != 5 {
		t.Errorf("int_value = %d, want 5", intValue)
	}
	if text != "ctx-data" {
		t.Errorf("str_value = %q, want ctx-data", text)
	}
}

// TestSnapshotCreatorLifecycle pins the creator consumption semantics: a
// creator is consumed by CreateBlob (or Close); further use errors.
func TestSnapshotCreatorLifecycle(t *testing.T) {
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatalf("NewSnapshotCreator: %v", err)
	}
	iso := creator.Isolate()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatalf("SetDefaultContext: %v", err)
	}
	_ = ctx.Close()
	blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
	if err != nil {
		t.Fatalf("CreateBlob: %v", err)
	}
	if err := blob.Release(); err != nil {
		t.Errorf("Release: %v", err)
	}
	// Documented deviation from the pinned panic: a creator abandoned
	// without a blob is closed explicitly and safely.
	creator2, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatalf("NewSnapshotCreator 2: %v", err)
	}
	iso2 := creator2.Isolate()
	if err := creator2.Close(); err != nil {
		t.Fatalf("Close without blob: %v", err)
	}
	if _, err := iso2.NewScope(); err == nil {
		t.Error("creator isolate must be dead after Close")
	}
	if err := creator2.Close(); err == nil {
		t.Error("double Close must fail")
	}
}

func TestSnapshotReleaseGuards(t *testing.T) {
	blob := snapMakeBlob(t, gov8.FunctionCodeClear)
	iso, err := gov8.NewIsolateFromSnapshot(blob)
	if err != nil {
		t.Fatalf("NewIsolateFromSnapshot: %v", err)
	}
	if err := blob.Release(); err == nil {
		t.Fatal("Release with an open consumer must fail")
	}
	if err := iso.Close(); err != nil {
		t.Fatalf("iso.Close: %v", err)
	}
	if err := blob.Release(); err != nil {
		t.Fatalf("Release after close: %v", err)
	}
	if err := blob.Release(); err != nil {
		t.Fatalf("double Release must be a safe no-op: %v", err)
	}
}

// snapEval is eval plus an ok flag (object results are plain values here).
func snapEval(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, src string) (gov8.Value, bool, error) {
	t.Helper()
	v, err := eval(t, ctx, scope, src)
	return v, err == nil, err
}

// snapEvalText mirrors eval_text ("" on failure).
func snapEvalText(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, src string) string {
	t.Helper()
	v, err := eval(t, ctx, scope, src)
	if err != nil {
		return ""
	}
	txt, err := v.ToString(ctx)
	if err != nil {
		return ""
	}
	return txt
}

func snapIsNoData(err error) bool {
	var de *gov8.SnapshotDataError
	if ok := errorsAs(err, &de); ok {
		return de.Kind == gov8.DataErrorNoData
	}
	return false
}

func snapIsBadType(err error) bool {
	var de *gov8.SnapshotDataError
	if ok := errorsAs(err, &de); ok {
		return de.Kind == gov8.DataErrorBadType
	}
	return false
}

// errorsAs is a minimal errors.As for the concrete *SnapshotDataError type,
// kept local to this file to avoid import churn in tests.
func errorsAs(err error, target **gov8.SnapshotDataError) bool {
	for err != nil {
		if e, ok := err.(*gov8.SnapshotDataError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestSubprocessInvalidStartupBlobGuard runs the guard scenario in a
// dedicated subprocess: the pinned crate aborts the process on
// StartupData::is_valid for undersized data (mode=invalid-startup-data-fatal
// upstream); the Go wrapper must answer invalid and keep the process fully
// alive. The child prints one JSON line; the parent asserts it byte-for-byte.
func TestSubprocessInvalidStartupBlobGuard(t *testing.T) {
	if os.Getenv("GOV8_TEST_SUBPROCESS") == "invalid-startup-blob" {
		runInvalidStartupBlobSubprocess(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSubprocessInvalidStartupBlobGuard$")
	cmd.Env = append(os.Environ(), "GOV8_TEST_SUBPROCESS=invalid-startup-blob")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, out)
	}
	line := extractJSONLine(string(out), `{"mode":"invalid-startup-blob"`)
	want := `{"mode":"invalid-startup-blob","is_valid":false,"creation_error":true,"alive":true}`
	if line != want {
		t.Fatalf("guard report diverged:\n want: %s\n got:  %s", want, line)
	}
}

func runInvalidStartupBlobSubprocess(t *testing.T) {
	short := gov8.StartupDataFromBytes(make([]byte, 32))
	isValid := short.IsValid() // must not abort
	_, creationErr := gov8.NewIsolateFromSnapshot(short)
	report := `{"mode":"invalid-startup-blob","is_valid":` +
		b2sJSON(isValid) +
		`,"creation_error":` + b2sJSON(creationErr != nil) +
		`,"alive":true}`
	println(report)
	// The engine must still be fully usable after the guarded rejection.
	iso, err := gov8.NewIsolate()
	if err != nil {
		os.Exit(1)
	}
	_ = iso.Close()
	os.Exit(0)
}

func b2sJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// extractJSONLine finds the line beginning with prefix in test output.
func extractJSONLine(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
