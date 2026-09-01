//go:build windows && amd64

package gov8

import (
	"fmt"
	"unsafe"
)

// Scope owns a v8 HandleScope. Every local handle (Value) created through a
// Scope lives in that scope's slot storage and becomes invalid once the
// scope is closed; the Go wrapper enforces this by refusing to operate on
// values whose scope is closed. Open scopes per isolate must strictly nest
// (that is a V8 rule) and must be closed on the isolate's owning thread.
type Scope struct {
	iso                       *Isolate
	handle                    uintptr
	closed                    bool
	javascriptExecutionGuards []uintptr
}

// NewScope opens a new HandleScope on the isolate.
func (i *Isolate) NewScope() (*Scope, error) {
	h, err := i.handleChecked()
	if err != nil {
		return nil, err
	}
	sh, err := callHandle("Scope.New", proc("gov8_scope_enter"), h)
	if err != nil {
		return nil, err
	}
	return &Scope{iso: i, handle: sh}, nil
}

// check validates the scope's own state and its isolate's thread affinity.
// The affinity check runs first so wrong-thread misuse returns before any
// scope-local state (written by the owning thread) is read.
func (s *Scope) check() error {
	if err := s.iso.check(); err != nil {
		return err
	}
	if s.closed {
		return fmt.Errorf("gov8: scope used after Close")
	}
	return nil
}

func (s *Scope) checkedHandle() (uintptr, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	return s.handle, nil
}

// checkedHandleAssumingIsolate returns the scope's handle for callers that
// already proved the isolate's lifecycle state and thread affinity in the
// same operation (Scope.check transit reach the isolate check; once that has
// passed, re-running it is redundant — the owner thread cannot interleave
// with itself — so only the scope-local closed flag still carries
// information here).
func (s *Scope) checkedHandleAssumingIsolate() (uintptr, error) {
	if s.closed {
		return 0, fmt.Errorf("gov8: scope used after Close")
	}
	return s.handle, nil
}

// Close closes the HandleScope. All Values created through this scope must
// no longer be used afterwards.
func (s *Scope) Close() error {
	if err := s.iso.check(); err != nil {
		return err
	}
	if s.closed {
		return fmt.Errorf("gov8: scope already closed")
	}
	if len(s.javascriptExecutionGuards) != 0 {
		return fmt.Errorf("gov8: scope has active JavaScript execution guards")
	}
	if err := requireInitialized(); err != nil {
		return err
	}
	r1, _, _ := proc("gov8_scope_exit").Call(s.handle)
	s.closed = true
	if int64(r1) < 0 {
		return shimError("Scope.Close", r1)
	}
	return nil
}

// Context is a persistent execution context (a rooted v8::Global<Context>).
// It is valid until Close and, like everything else, only usable on the
// isolate's owning thread.
type Context struct {
	iso            *Isolate
	handle         uintptr
	closed         bool
	microtaskQueue *MicrotaskQueue
}

// NewContext creates a default context on the isolate. A context is
// engine-persistent; it does not require a Scope to create or keep alive.
func (i *Isolate) NewContext() (*Context, error) {
	ih, err := i.handleChecked()
	if err != nil {
		return nil, err
	}
	ch, err := callHandle("Context.New", proc("gov8_context_new"), ih)
	if err != nil {
		return nil, err
	}
	return &Context{iso: i, handle: ch}, nil
}

// check validates the context's own state and its isolate's thread
// affinity; affinity first, so wrong-thread misuse returns before the
// context-local closed flag is read.
func (c *Context) check() error {
	if err := c.iso.check(); err != nil {
		return err
	}
	if c.closed {
		return fmt.Errorf("gov8: context used after Close")
	}
	return nil
}

// checkAssumingIsolate validates the context's closed flag for callers that
// already proved the isolate's lifecycle state and thread affinity in the
// same operation (Context.check transits the isolate check; once that has
// passed, re-running it is redundant — the owner thread cannot interleave
// with itself — so only the context-local closed flag still carries
// information here).
func (c *Context) checkAssumingIsolate() error {
	if c.closed {
		return fmt.Errorf("gov8: context used after Close")
	}
	return nil
}

// Close releases the context. It must not be used afterwards.
func (c *Context) Close() error {
	if err := c.iso.check(); err != nil {
		return err
	}
	if c.closed {
		return fmt.Errorf("gov8: context already closed")
	}
	if err := requireInitialized(); err != nil {
		return err
	}
	r1, _, _ := proc("gov8_context_dispose").Call(c.handle)
	c.closed = true
	if c.microtaskQueue != nil {
		c.microtaskQueue.attachments--
		c.microtaskQueue = nil
	}
	if int64(r1) < 0 {
		return shimError("Context.Close", r1)
	}
	return nil
}

// Object is a value known to be a JS object (used for the global object).
type Object struct {
	Value
}

// GlobalObject returns the context's global object as a scope-local value.
// The scope must belong to the same isolate as the context.
func (c *Context) GlobalObject(s *Scope) (*Object, error) {
	// c.check proves isolate state and affinity for this operation, so the
	// scope's and context's own checks below skip the (identical, and on
	// this thread immutable) isolate validation the old code re-ran twice.
	if err := c.check(); err != nil {
		return nil, err
	}
	sh, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return nil, err
	}
	if s.iso != c.iso {
		return nil, foreignIsolate("scope")
	}
	var out uintptr
	r1, _, _ := proc("gov8_context_global_object").Call(
		c.iso.handleAssumingCheck(), c.handle, sh, uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return nil, shimError("Context.GlobalObject", r1)
	}
	return &Object{Value{iso: c.iso, sc: s, h: out}}, nil
}
