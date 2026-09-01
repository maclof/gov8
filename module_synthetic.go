//go:build windows && amd64

package gov8

import (
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

// SyntheticModuleEvaluationCallback runs once when a synthetic module is
// evaluated. Returning a Promise preserves V8's asynchronous evaluation
// semantics; the first EvaluateValue returns any other value directly. Later
// evaluation returns V8's fulfilled top-level Promise with undefined. A zero
// Value is normalized to undefined; unlike rusty_v8's empty MaybeLocal callback
// result, it cannot trigger V8's fatal missing-exception check. Returning an
// error throws into V8.
type SyntheticModuleEvaluationCallback func(*SyntheticModuleEvaluation) (Value, error)

// SyntheticModuleEvaluation is valid only for the duration of its callback.
// Values created through Scope are callback-local and must not escape it.
type SyntheticModuleEvaluation struct {
	module *Module
	scope  *CallbackScope
	thrown bool
}

// Module returns the synthetic module being evaluated.
func (e *SyntheticModuleEvaluation) Module() *Module { return e.module }

// Scope returns the callback-local construction and conversion scope.
func (e *SyntheticModuleEvaluation) Scope() *CallbackScope { return e.scope }

// SetExport updates one of the names declared when the module was created.
func (e *SyntheticModuleEvaluation) SetExport(name string, value Value) error {
	if e == nil || e.module == nil || e.scope == nil {
		return errors.New("gov8: synthetic module evaluation is no longer active")
	}
	_, err := e.module.setSyntheticModuleExport(e.scope.sc, name, value, nil)
	if shimErr, ok := err.(*ShimError); ok && shimErr.Code == errException && shimErr.Detail != "" {
		// The callback-only native path catches the exception and reports its
		// exact ECMAScript text. Keep the public callback error identical to
		// the former explicit TryCatch path without three extra DLL crossings.
		return errors.New(shimErr.Detail)
	}
	return err
}

// NewTypeError constructs a callback-local TypeError.
func (e *SyntheticModuleEvaluation) NewTypeError(message string) (Value, error) {
	if e == nil || e.module == nil || e.scope == nil {
		return Value{}, errors.New("gov8: synthetic module evaluation is no longer active")
	}
	return e.module.ctx.NewTypeError(e.scope.sc, message)
}

// Throw schedules exception as the evaluation failure. The callback should
// then return; the dispatcher converts the result to an empty MaybeLocal.
func (e *SyntheticModuleEvaluation) Throw(exception Value) error {
	if e == nil || e.module == nil || e.scope == nil {
		return errors.New("gov8: synthetic module evaluation is no longer active")
	}
	if err := e.scope.ThrowException(exception); err != nil {
		return err
	}
	e.thrown = true
	return nil
}

type syntheticModuleEntry struct {
	module   *Module
	callback SyntheticModuleEvaluationCallback
}

// syntheticEvaluationState keeps the three callback-local wrappers in one
// allocation. It is intentionally not retained by the registry: modules that
// are created but never evaluated pay no scratch-storage cost.
type syntheticEvaluationState struct {
	borrowedScope Scope
	callbackScope CallbackScope
	evaluation    SyntheticModuleEvaluation
}

var syntheticModuleRegistry = struct {
	sync.Mutex
	next    uint64
	entries map[uint64]syntheticModuleEntry
}{entries: make(map[uint64]syntheticModuleEntry)}

var (
	syntheticDispatcherOnce sync.Once
	syntheticDispatcherErr  error
)

var syntheticEvaluationDispatcher = syscall.NewCallback(
	func(id, contextWire, scopeWire, outWire uintptr) (handled uintptr) {
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(os.Stderr, "gov8: panic in synthetic module evaluation callback: %v\n", recovered)
				proc("gov8_host_panic_abort").Call()
				panic(recovered) // unreachable
			}
		}()
		syntheticModuleRegistry.Lock()
		entry, found := syntheticModuleRegistry.entries[uint64(id)]
		syntheticModuleRegistry.Unlock()
		if !found || entry.module == nil {
			fatalHostMisuse("synthetic module callback for unknown handle %d", id)
			return 0
		}
		if err := entry.module.check(); err != nil {
			fatalHostMisuse("synthetic module callback lifecycle: %v", err)
			return 0
		}
		state := new(syntheticEvaluationState)
		borrowedScope := &state.borrowedScope
		*borrowedScope = Scope{iso: entry.module.iso, handle: scopeWire, borrowed: true}
		callbackScope := &state.callbackScope
		*callbackScope = CallbackScope{
			iso: entry.module.iso, sc: borrowedScope, ctxWire: contextWire,
		}
		evaluation := &state.evaluation
		*evaluation = SyntheticModuleEvaluation{module: entry.module, scope: callbackScope}
		entry.module.syntheticActive = true
		defer func() {
			entry.module.syntheticActive = false
			evaluation.module = nil
			evaluation.scope = nil
			borrowedScope.closed = true
			borrowedScope.handle = 0
		}()
		value, err := entry.callback(evaluation)
		if err != nil {
			moduleThrowResolverError(entry.module.iso, err.Error())
			return 0
		}
		if evaluation.thrown {
			return 0
		}
		if value.h != 0 {
			if err := value.check(); err != nil {
				moduleThrowResolverError(entry.module.iso, err.Error())
				return 0
			}
			if value.iso != entry.module.iso {
				moduleThrowResolverError(entry.module.iso, foreignIsolate("synthetic evaluation result").Error())
				return 0
			}
			*(*uintptr)(abiWordToPtr(outWire)) = value.h
		}
		return 1
	})

func registerSyntheticModuleCallback(callback SyntheticModuleEvaluationCallback) (uint64, error) {
	if callback == nil {
		return 0, errors.New("gov8: synthetic module evaluation callback is required")
	}
	syntheticDispatcherOnce.Do(func() {
		syntheticDispatcherErr = callErr("SyntheticModule.Dispatcher",
			proc("gov8_synthetic_set_dispatcher"), syntheticEvaluationDispatcher)
	})
	if syntheticDispatcherErr != nil {
		return 0, syntheticDispatcherErr
	}
	syntheticModuleRegistry.Lock()
	if syntheticModuleRegistry.next == math.MaxUint64 {
		syntheticModuleRegistry.Unlock()
		return 0, errors.New("gov8: synthetic module callback registry exhausted")
	}
	syntheticModuleRegistry.next++
	id := syntheticModuleRegistry.next
	syntheticModuleRegistry.entries[id] = syntheticModuleEntry{callback: callback}
	syntheticModuleRegistry.Unlock()
	return id, nil
}

func bindSyntheticModuleCallback(id uint64, module *Module) {
	syntheticModuleRegistry.Lock()
	if entry, found := syntheticModuleRegistry.entries[id]; found {
		entry.module = module
		syntheticModuleRegistry.entries[id] = entry
	}
	syntheticModuleRegistry.Unlock()
}

func dropSyntheticModuleCallback(id uint64) {
	if id == 0 {
		return
	}
	syntheticModuleRegistry.Lock()
	delete(syntheticModuleRegistry.entries, id)
	syntheticModuleRegistry.Unlock()
}

// NewSyntheticModule creates a module with fixed export names. Duplicate
// names are rejected in Go because V8's later instantiation path CHECK-fails
// on duplicates. The callback is retained until Module.Close.
func (c *Context) NewSyntheticModule(s *Scope, moduleName string,
	exportNames []string, callback SyntheticModuleEvaluationCallback) (*Module, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	if s == nil || s.iso != c.iso {
		return nil, foreignIsolate("scope")
	}
	scopeHandle, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return nil, err
	}
	if len(moduleName) > math.MaxInt32 || len(exportNames) > math.MaxInt32 {
		return nil, errors.New("gov8: synthetic module input exceeds int32")
	}
	if !utf8.ValidString(moduleName) {
		return nil, errors.New("gov8: synthetic module name is not valid UTF-8")
	}
	var seen map[string]struct{}
	if len(exportNames) > 8 {
		seen = make(map[string]struct{}, len(exportNames))
	}
	// Keep pointer provenance in GC-visible unsafe.Pointer values until the
	// native call. A uintptr captured before callback registration could become
	// stale if a caller supplied stack-backed string data and the stack moved.
	namePointers := make([]unsafe.Pointer, len(exportNames))
	nameLengths := make([]int64, len(exportNames))
	for index, name := range exportNames {
		if !utf8.ValidString(name) {
			return nil, errors.New("gov8: synthetic module export name is not valid UTF-8")
		}
		if len(name) > math.MaxInt32 {
			return nil, errors.New("gov8: synthetic module export name exceeds int32")
		}
		if seen != nil {
			if _, duplicate := seen[name]; duplicate {
				return nil, fmt.Errorf("gov8: duplicate synthetic module export %q", name)
			}
			seen[name] = struct{}{}
		} else {
			for previous := 0; previous < index; previous++ {
				if exportNames[previous] == name {
					return nil, fmt.Errorf("gov8: duplicate synthetic module export %q", name)
				}
			}
		}
		if len(name) != 0 {
			namePointers[index] = unsafe.Pointer(unsafe.StringData(name))
		}
		nameLengths[index] = int64(len(name))
	}
	callbackID, err := registerSyntheticModuleCallback(callback)
	if err != nil {
		return nil, err
	}
	createProc := proc("gov8_synthetic_create")
	isolateHandle := c.iso.handleAssumingCheck()
	var moduleNamePointer unsafe.Pointer
	if len(moduleName) != 0 {
		moduleNamePointer = unsafe.Pointer(unsafe.StringData(moduleName))
	}
	var namesArg, lengthsArg unsafe.Pointer
	if len(exportNames) != 0 {
		namesArg = unsafe.Pointer(&namePointers[0])
		lengthsArg = unsafe.Pointer(&nameLengths[0])
	}
	var out uintptr
	r1, _, _ := syscall.Syscall12(createProc.Addr(), 10,
		isolateHandle, c.handle, scopeHandle,
		uintptr(moduleNamePointer), uintptr(len(moduleName)), uintptr(namesArg),
		uintptr(lengthsArg), uintptr(len(exportNames)), uintptr(callbackID),
		uintptr(unsafe.Pointer(&out)), 0, 0)
	runtime.KeepAlive(moduleName)
	runtime.KeepAlive(exportNames)
	runtime.KeepAlive(namePointers)
	runtime.KeepAlive(nameLengths)
	if int64(r1) < 0 {
		dropSyntheticModuleCallback(callbackID)
		return nil, shimError("NewSyntheticModule", r1)
	}
	module := &Module{
		iso: c.iso, ctx: c, handle: out, syntheticCallbackID: callbackID,
	}
	bindSyntheticModuleCallback(callbackID, module)
	moduleRegMu.Lock()
	moduleByHandle[out] = module
	moduleRegMu.Unlock()
	return module, nil
}

func (m *Module) setSyntheticModuleExport(s *Scope, name string, value Value,
	tc *TryCatch) (bool, error) {
	if err := m.check(); err != nil {
		return false, err
	}
	if m.syntheticCallbackID == 0 {
		return false, errors.New("gov8: module is not synthetic")
	}
	if s == nil || s.iso != m.iso {
		return false, foreignIsolate("scope")
	}
	scopeHandle, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return false, err
	}
	if err := value.check(); err != nil {
		return false, err
	}
	if value.iso != m.iso {
		return false, foreignIsolate("synthetic export value")
	}
	if len(name) > math.MaxInt32 {
		return false, errors.New("gov8: synthetic export name exceeds int32")
	}
	if !utf8.ValidString(name) {
		return false, errors.New("gov8: synthetic export name is not valid UTF-8")
	}
	var tryCatchHandle uintptr
	if tc != nil {
		if tc.iso != m.iso {
			return false, foreignIsolate("trycatch")
		}
		if err := tc.check(); err != nil {
			return false, err
		}
		tryCatchHandle = tc.handle
	}
	nameBytes := []byte(name)
	var updated int32
	r1, _, _ := proc("gov8_synthetic_set_export").Call(
		m.iso.handleAssumingCheck(), scopeHandle, m.handle, tryCatchHandle,
		bytesPtr(nameBytes), uintptr(len(nameBytes)), value.h,
		uintptr(unsafe.Pointer(&updated)))
	runtime.KeepAlive(nameBytes)
	if int64(r1) < 0 {
		return false, shimError("Module.SetSyntheticModuleExport", r1)
	}
	return updated != 0, nil
}

// SetSyntheticModuleExport updates one declared export. An undeclared name
// throws ReferenceError and returns an exception error, recorded in tc when
// supplied.
func (m *Module) SetSyntheticModuleExport(s *Scope, name string, value Value,
	tc *TryCatch) (bool, error) {
	return m.setSyntheticModuleExport(s, name, value, tc)
}

func (m *Module) evaluateSyntheticValue(s *Scope, tc *TryCatch) (Value, error) {
	if err := m.check(); err != nil {
		return Value{}, err
	}
	if m.syntheticCallbackID == 0 {
		return Value{}, errors.New("gov8: module is not synthetic")
	}
	if s == nil || s.iso != m.iso {
		return Value{}, foreignIsolate("scope")
	}
	scopeHandle, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return Value{}, err
	}
	status, err := m.Status()
	if err != nil {
		return Value{}, err
	}
	if status < ModuleInstantiated {
		return Value{}, fmt.Errorf("gov8: Evaluate requires an instantiated module, got %s", status)
	}
	var tryCatchHandle uintptr
	if tc != nil {
		if tc.iso != m.iso {
			return Value{}, foreignIsolate("trycatch")
		}
		if err := tc.check(); err != nil {
			return Value{}, err
		}
		tryCatchHandle = tc.handle
	}
	var out uintptr
	r1, _, _ := proc("gov8_synthetic_evaluate").Call(
		m.iso.handleAssumingCheck(), m.ctx.handle, scopeHandle, m.handle,
		tryCatchHandle, uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, shimError("Module.EvaluateValue", r1)
	}
	return Value{iso: m.iso, sc: s, h: out}, nil
}
