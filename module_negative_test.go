//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "gov8"
)

const moduleResolverPanicAbortCode = 3221226505 // 0xC0000409

func TestModuleResolverPanicAbortsProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run", "^TestProbeModuleResolverPanic$", "-test.count=1", "-test.v=false")
	cmd.Env = append(os.Environ(), "GOV8_MODULE_PANIC_PROBE=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	for _, marker := range []string{
		"marker:module-resolver-before", "marker:module-resolver-entered",
		"gov8: panic in module resolver: module-resolver-panic",
	} {
		if !strings.Contains(text, marker) {
			t.Errorf("missing %q; output:\n%s", marker, text)
		}
	}
	if strings.Contains(text, "marker:module-resolver-after") {
		t.Errorf("resolver panic returned; output:\n%s", text)
	}
	if err == nil {
		t.Fatalf("probe exited cleanly; output:\n%s", text)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("probe error = %T %v", err, err)
	}
	if code := exitErr.ExitCode(); code != moduleResolverPanicAbortCode {
		t.Fatalf("exit code = %d, want %d (0xC0000409); output:\n%s", code, moduleResolverPanicAbortCode, text)
	}
}

func TestProbeModuleResolverPanic(t *testing.T) {
	if os.Getenv("GOV8_MODULE_PANIC_PROBE") != "1" {
		t.Skip("probe body")
	}
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	m, err := ctx.CompileModule(scope, "import './panic.mjs';", "panic-entry.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "marker:module-resolver-before")
	_, _ = m.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		fmt.Fprintln(os.Stderr, "marker:module-resolver-entered")
		panic("module-resolver-panic")
	}, nil)
	fmt.Fprintln(os.Stderr, "marker:module-resolver-after")
}

func TestModuleLifecycleAndStateGuards(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	m, err := ctx.CompileModule(scope, "export const x = 1;", "life.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Namespace(scope); err == nil {
		t.Fatal("Namespace before instantiate must fail")
	}
	if _, err := m.Exception(scope); err == nil {
		t.Fatal("Exception before errored must fail")
	}
	if _, err := m.Evaluate(scope, nil); err == nil {
		t.Fatal("Evaluate before instantiate must fail")
	}
	if _, err := m.IsGraphAsync(); err == nil {
		t.Fatal("IsGraphAsync before instantiate must fail")
	}
	linked, err := m.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		t.Fatal("resolver called for request-free module")
		return nil, nil
	}, nil)
	if err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	if _, err := m.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err == nil {
		t.Fatal("double Instantiate must fail")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("Status after Close = %v", err)
	}
	if err := m.Close(); err == nil {
		t.Fatal("double Module.Close must fail")
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestModuleScopeAndContextLifetime(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	m, err := ctx.CompileModule(scope, "export default 1;", "scope.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	// Persistent metadata remains usable after the compiling handle scope.
	if status, err := m.Status(); err != nil || status != gov8.ModuleUninstantiated {
		t.Fatalf("Status after compiling scope close = %v, %v", status, err)
	}
	scope2 := newScope(t, iso)
	if _, err := m.Namespace(scope); err == nil {
		t.Fatal("closed scope must be rejected")
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(); err == nil || !strings.Contains(err.Error(), "context used after Close") {
		t.Fatalf("module after context close = %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scope2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestModuleWrongThreadAndCrossIsolate(t *testing.T) {
	_, _, ctxA, ctxB, scopeA, scopeB := twoIsolates(t)
	mA, err := ctxA.CompileModule(scopeA, "import './foreign.mjs'; export default 1;", "a.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	mB, err := ctxB.CompileModule(scopeB, "export default 2;", "b.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mB.Close(); _ = mA.Close() }()

	wantForeignIsolateError(t, "CompileModule foreign scope", func() error {
		_, err := ctxA.CompileModule(scopeB, "", "", nil)
		return err
	})
	wantForeignIsolateError(t, "Namespace foreign scope", func() error {
		_, err := mA.Namespace(scopeB)
		return err
	})
	_, err = mA.Instantiate(scopeA, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		return mB, nil
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign resolved module error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := mA.Status()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread Status = %v", err)
	}
}

func TestModuleBoundaryInputs(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	var modules []*gov8.Module
	defer closeModuleRuntime(t, modules, scope, ctx, iso)

	for _, tc := range []struct {
		name   string
		source string
	}{
		{"empty", ""},
		{"unicode", "export const π = '🙂';"},
		{"embedded-nul", "export const x = 'a\x00b';"},
	} {
		m, err := ctx.CompileModule(scope, tc.source, tc.name+".mjs", nil)
		if err != nil {
			t.Fatalf("%s CompileModule: %v", tc.name, err)
		}
		modules = append(modules, m)
	}
	if _, err := ctx.CompileModule(scope, "export {", "bad.mjs", nil); !gov8.IsException(err) {
		t.Fatalf("truncated source error = %v", err)
	}
}
