//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// The root TestMain initializes V8 before ordinary tests run. This opt-in init
// mode executes in a fresh test process before TestMain to pin the standalone
// allocator-factory lifecycle and post-platform BackingStore operations.
func init() {
	if os.Getenv("GOV8_ALLOCATOR_PREINIT_CHILD") != "1" {
		return
	}
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "preinit allocator failure: "+format+"\n", args...)
		os.Exit(90)
	}
	fmt.Fprintln(os.Stderr, "marker:preinit-entered")
	defaultAllocator, err := gov8.NewDefaultArrayBufferAllocator()
	if err != nil {
		fail("default factory: %v", err)
	}
	if err := defaultAllocator.Close(); err != nil {
		fail("default close: %v", err)
	}
	var preinitDrops atomic.Int32
	preinitAllocator, err := gov8.NewArrayBufferAllocator(gov8.ArrayBufferAllocatorCallbacks{
		Drop: func() { preinitDrops.Add(1) },
	})
	if err != nil {
		fail("callback factory: %v", err)
	}
	if err := preinitAllocator.Close(); err != nil {
		fail("callback close: %v", err)
	}
	if preinitDrops.Load() != 1 {
		fail("preinit drops = %d", preinitDrops.Load())
	}
	fmt.Fprintln(os.Stderr, "marker:preinit-factories-closed")

	if err := gov8.Initialize(); err != nil {
		fail("Initialize: %v", err)
	}
	var lifecycleDrops atomic.Int32
	lifecycleAllocator, err := gov8.NewArrayBufferAllocator(gov8.ArrayBufferAllocatorCallbacks{
		Drop: func() { lifecycleDrops.Add(1) },
	})
	if err != nil {
		fail("lifecycle allocator: %v", err)
	}
	params := gov8.NewCreateParams()
	if err := params.SetArrayBufferAllocator(lifecycleAllocator); err != nil {
		fail("SetArrayBufferAllocator: %v", err)
	}
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		fail("NewIsolateWithParams: %v", err)
	}
	store, err := iso.NewBackingStore(3)
	if err != nil {
		fail("NewBackingStore: %v", err)
	}
	if _, err := store.WriteAt([]byte{4, 2, 1}, 0); err != nil {
		fail("WriteAt: %v", err)
	}
	if err := lifecycleAllocator.Close(); err != nil {
		fail("allocator owner Close: %v", err)
	}
	if err := iso.Close(); err != nil {
		fail("isolate Close: %v", err)
	}
	if err := gov8.Shutdown(); err != nil {
		fail("Shutdown: %v", err)
	}
	got := make([]byte, 3)
	if _, err := store.ReadAt(got, 0); err != nil {
		fail("post-shutdown ReadAt: %v", err)
	}
	if fmt.Sprint(got) != "[4 2 1]" {
		fail("post-shutdown bytes = %v", got)
	}
	if err := store.Close(); err != nil {
		fail("post-shutdown Close: %v", err)
	}
	if lifecycleDrops.Load() != 1 {
		fail("post-shutdown drops = %d", lifecycleDrops.Load())
	}
	fmt.Fprintln(os.Stderr, "marker:post-shutdown-store-closed")
	os.Exit(0)
}

func TestArrayBufferAllocatorFactoriesBeforeInitialize(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GOV8_ALLOCATOR_PREINIT_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pre-initialize allocator child: %v\n%s", err, output)
	}
	text := string(output)
	for _, marker := range []string{
		"marker:preinit-entered",
		"marker:preinit-factories-closed",
		"marker:post-shutdown-store-closed",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("missing %q in child output:\n%s", marker, text)
		}
	}
}

type allocatorTestEvents struct {
	mu            sync.Mutex
	initialized   []int
	uninitialized []int
	freed         []int
	first         []byte
	drops         atomic.Int32
}

func (e *allocatorTestEvents) callbacks() gov8.ArrayBufferAllocatorCallbacks {
	return gov8.ArrayBufferAllocatorCallbacks{
		Allocate: func(length int) bool {
			e.mu.Lock()
			e.initialized = append(e.initialized, length)
			e.mu.Unlock()
			return true
		},
		AllocateUninitialized: func(length int) bool {
			e.mu.Lock()
			e.uninitialized = append(e.uninitialized, length)
			e.mu.Unlock()
			return true
		},
		Free: func(length int, first byte) {
			e.mu.Lock()
			e.freed = append(e.freed, length)
			e.first = append(e.first, first)
			e.mu.Unlock()
		},
		Drop: func() { e.drops.Add(1) },
	}
}

func TestArrayBufferAllocatorCallbacksAndBackingStoreLifetime(t *testing.T) {
	events := &allocatorTestEvents{}
	allocator, err := gov8.NewArrayBufferAllocator(events.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	params := gov8.NewCreateParams()
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		t.Fatal(err)
	}
	if !params.HasSetArrayBufferAllocator() {
		t.Fatal("custom allocator did not set CreateParams flag")
	}
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	buffer, err := gov8.NewArrayBuffer(scope, ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	store, err := buffer.GetBackingStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteAt([]byte{77}, 0); err != nil {
		t.Fatal(err)
	}
	zero, err := gov8.NewArrayBuffer(scope, ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := zero.Data(); err != nil || present {
		t.Fatalf("zero Data = present=%v err=%v", present, err)
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
	var got [1]byte
	if _, err := store.ReadAt(got[:], 0); err != nil || got[0] != 77 {
		t.Fatalf("store after isolate = %v, %v", got, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	if events.drops.Load() != 1 {
		t.Fatalf("allocator drops = %d", events.drops.Load())
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	if fmt.Sprint(events.initialized) != "[7]" || fmt.Sprint(events.freed) != "[7]" || fmt.Sprint(events.first) != "[77]" {
		t.Fatalf("events initialized=%v freed=%v first=%v", events.initialized, events.freed, events.first)
	}
}

func TestArrayBufferAllocatorValidation(t *testing.T) {
	if err := (*gov8.CreateParams)(nil).SetArrayBufferAllocator(nil); err == nil {
		t.Fatal("nil params/allocator accepted")
	}
	allocator, err := gov8.NewDefaultArrayBufferAllocator()
	if err != nil {
		t.Fatal(err)
	}
	if count, err := allocator.UseCount(); err != nil || count != 1 {
		t.Fatalf("initial use count = %d, %v", count, err)
	}
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gov8.NewCreateParams().SetArrayBufferAllocator(allocator); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed allocator Set = %v", err)
	}
	if err := allocator.Close(); err == nil {
		t.Fatal("double Close accepted")
	}
	if _, err := allocator.UseCount(); err == nil {
		t.Fatal("UseCount after Close accepted")
	}
}

func TestArrayBufferAllocatorOwnerMayCloseBeforeIsolate(t *testing.T) {
	events := &allocatorTestEvents{}
	allocator, err := gov8.NewArrayBufferAllocator(events.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	params := gov8.NewCreateParams()
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		t.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if events.drops.Load() != 1 {
		t.Fatalf("allocator drops after isolate = %d", events.drops.Load())
	}
}

func TestBackingStoreConcurrentReadAndClose(t *testing.T) {
	events := &allocatorTestEvents{}
	allocator, err := gov8.NewArrayBufferAllocator(events.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	params := gov8.NewCreateParams()
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		t.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		t.Fatal(err)
	}
	store, err := iso.NewBackingStore(32)
	if err != nil {
		t.Fatal(err)
	}
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errors := make(chan error, 24)
	var closes atomic.Int32
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			var data [32]byte
			_, err := store.ReadAt(data[:], 0)
			errors <- err
		}()
	}
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if err := store.Close(); err == nil {
				closes.Add(1)
			} else {
				errors <- err
			}
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil && !strings.Contains(err.Error(), "active") && !strings.Contains(err.Error(), "Close") && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if closes.Load() == 0 {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		closes.Add(1)
	}
	if closes.Load() != 1 {
		t.Fatalf("successful closes = %d", closes.Load())
	}
	if _, err := store.ByteLength(); err == nil {
		t.Fatal("closed backing store remained usable")
	}
	if events.drops.Load() != 1 {
		t.Fatalf("allocator drops = %d", events.drops.Load())
	}
}

func TestArrayBufferAllocatorFatalBoundaries(t *testing.T) {
	if mode := os.Getenv("GOV8_ALLOCATOR_FATAL_CHILD"); mode != "" {
		chRawAbortExit()
		callbacks := gov8.ArrayBufferAllocatorCallbacks{}
		switch mode {
		case "refuse":
			callbacks.Allocate = func(int) bool { return false }
		case "allocate-panic":
			callbacks.Allocate = func(int) bool { panic("allocator allocate panic marker") }
		case "uninitialized-panic":
			callbacks.AllocateUninitialized = func(int) bool { panic("allocator uninitialized panic marker") }
		case "free-panic":
			callbacks.Free = func(int, byte) { panic("allocator free panic marker") }
		case "drop-panic":
			callbacks.Drop = func() { panic("allocator drop panic marker") }
		default:
			os.Exit(92)
		}
		allocator, err := gov8.NewArrayBufferAllocator(callbacks)
		if err != nil {
			panic(err)
		}
		if mode == "drop-panic" {
			fmt.Fprintln(os.Stderr, "marker:before-"+mode)
			_ = allocator.Close()
			fmt.Fprintln(os.Stderr, "marker:after-"+mode)
			os.Exit(91)
		}
		params := gov8.NewCreateParams()
		if err := params.SetArrayBufferAllocator(allocator); err != nil {
			panic(err)
		}
		iso, err := gov8.NewIsolateWithParams(params)
		if err != nil {
			panic(err)
		}
		if mode == "free-panic" {
			store, err := iso.NewBackingStore(8)
			if err != nil {
				panic(err)
			}
			fmt.Fprintln(os.Stderr, "marker:before-"+mode)
			_ = store.Close()
			fmt.Fprintln(os.Stderr, "marker:after-"+mode)
			os.Exit(91)
		}
		ctx, _ := iso.NewContext()
		scope, _ := iso.NewScope()
		if mode == "uninitialized-panic" {
			source, err := gov8.NewArrayBuffer(scope, ctx, 4)
			if err != nil {
				panic(err)
			}
			global, err := ctx.GlobalObject(scope)
			if err != nil {
				panic(err)
			}
			if ok, err := global.SetByName(scope, ctx, "source", source.Value); err != nil || !ok {
				panic(fmt.Sprintf("set source: %v, %v", ok, err))
			}
			fmt.Fprintln(os.Stderr, "marker:before-"+mode)
			script, err := ctx.Compile(scope, "source.transfer(6)", nil)
			if err != nil {
				panic(err)
			}
			_, _ = script.Run(scope, nil)
			fmt.Fprintln(os.Stderr, "marker:after-"+mode)
			os.Exit(91)
		}
		fmt.Fprintln(os.Stderr, "marker:before-"+mode)
		_, _ = gov8.NewArrayBuffer(scope, ctx, 8)
		fmt.Fprintln(os.Stderr, "marker:after-"+mode)
		os.Exit(91)
	}

	for _, mode := range []string{"refuse", "allocate-panic", "uninitialized-panic", "free-panic", "drop-panic"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestArrayBufferAllocatorFatalBoundaries$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_ALLOCATOR_FATAL_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s unexpectedly survived: %v\n%s", mode, err, output)
		}
		wantExit := uint32(0x80000003)
		if mode != "refuse" {
			wantExit = 0xC0000409
		}
		if uint32(exit.ExitCode()) != wantExit {
			t.Fatalf("%s exit = %#x, want %#x; output:\n%s", mode, uint32(exit.ExitCode()), wantExit, output)
		}
		text := string(output)
		if !strings.Contains(text, "marker:before-"+mode) || strings.Contains(text, "marker:after-"+mode) {
			t.Fatalf("%s markers invalid:\n%s", mode, text)
		}
		if mode == "refuse" {
			if !strings.Contains(text, "Fatal process out of memory: v8::ArrayBuffer::New") {
				t.Fatalf("refusal fatal text missing:\n%s", text)
			}
		} else if !strings.Contains(text, "panic in ArrayBuffer allocator callback") {
			t.Fatalf("%s panic text missing:\n%s", mode, text)
		}
	}
}
