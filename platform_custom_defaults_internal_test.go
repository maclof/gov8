//go:build windows && amd64

package gov8

import (
	"math"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type platformDefaultsProbeEvent struct {
	handle   uintptr
	threadID uint32
	deadline uint64
	idle     bool
	dropped  bool
}

var platformDefaultsProbe struct {
	sync.Mutex
	events []platformDefaultsProbeEvent
}

func platformDefaultsTaskRunProbe(handle uintptr) uintptr {
	platformDefaultsProbe.Lock()
	platformDefaultsProbe.events = append(platformDefaultsProbe.events, platformDefaultsProbeEvent{
		handle: handle, threadID: currentThreadID(),
	})
	platformDefaultsProbe.Unlock()
	return 0
}

func platformDefaultsTaskDropProbe(handle uintptr) uintptr {
	platformDefaultsProbe.Lock()
	platformDefaultsProbe.events = append(platformDefaultsProbe.events, platformDefaultsProbeEvent{
		handle: handle, threadID: currentThreadID(), dropped: true,
	})
	platformDefaultsProbe.Unlock()
	return 0
}

func platformDefaultsIdleRunProbe(handle, deadline uintptr) uintptr {
	platformDefaultsProbe.Lock()
	platformDefaultsProbe.events = append(platformDefaultsProbe.events, platformDefaultsProbeEvent{
		handle: handle, threadID: currentThreadID(), deadline: uint64(deadline), idle: true,
	})
	platformDefaultsProbe.Unlock()
	return 0
}

func platformDefaultsIdleDropProbe(handle uintptr) uintptr {
	platformDefaultsProbe.Lock()
	platformDefaultsProbe.events = append(platformDefaultsProbe.events, platformDefaultsProbeEvent{
		handle: handle, threadID: currentThreadID(), idle: true, dropped: true,
	})
	platformDefaultsProbe.Unlock()
	return 0
}

func installPlatformDefaultsProbes(t *testing.T) {
	t.Helper()
	oldTaskRun := customPlatformTaskRunDeleteAddr
	oldTaskDrop := customPlatformTaskDeleteAddr
	oldIdleRun := customPlatformIdleTaskRunDeleteAddr
	oldIdleDrop := customPlatformIdleTaskDeleteAddr
	customPlatformTaskRunDeleteAddr = syscall.NewCallback(platformDefaultsTaskRunProbe)
	customPlatformTaskDeleteAddr = syscall.NewCallback(platformDefaultsTaskDropProbe)
	customPlatformIdleTaskRunDeleteAddr = syscall.NewCallback(platformDefaultsIdleRunProbe)
	customPlatformIdleTaskDeleteAddr = syscall.NewCallback(platformDefaultsIdleDropProbe)
	platformDefaultsProbe.Lock()
	platformDefaultsProbe.events = nil
	platformDefaultsProbe.Unlock()
	t.Cleanup(func() {
		customPlatformTaskRunDeleteAddr = oldTaskRun
		customPlatformTaskDeleteAddr = oldTaskDrop
		customPlatformIdleTaskRunDeleteAddr = oldIdleRun
		customPlatformIdleTaskDeleteAddr = oldIdleDrop
		platformDefaultsProbe.Lock()
		platformDefaultsProbe.events = nil
		platformDefaultsProbe.Unlock()
	})
}

func platformDefaultsEvents() []platformDefaultsProbeEvent {
	platformDefaultsProbe.Lock()
	defer platformDefaultsProbe.Unlock()
	return append([]platformDefaultsProbeEvent(nil), platformDefaultsProbe.events...)
}

func runPlatformDefaultWithTimeout(t *testing.T, call func()) uint32 {
	t.Helper()
	done := make(chan uint32, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		caller := currentThreadID()
		call()
		done <- caller
	}()
	select {
	case caller := <-done:
		return caller
	case <-time.After(500 * time.Millisecond):
		t.Fatal("PlatformImplDefaults did not run synchronously; a delayed method may have honored its delay")
		return 0
	}
}

func TestPlatformImplDefaultsRunSynchronously(t *testing.T) {
	installPlatformDefaultsProbes(t)
	defaults := PlatformImplDefaults{}
	identity := PlatformIsolate(0x1234)

	type taskCase struct {
		name string
		call func(*Task)
	}
	cases := []taskCase{
		{"task", func(task *Task) { defaults.PostTask(identity, task) }},
		{"non-nestable", func(task *Task) { defaults.PostNonNestableTask(identity, task) }},
		{"delayed", func(task *Task) { defaults.PostDelayedTask(identity, task, 3600) }},
		{"non-nestable-delayed", func(task *Task) { defaults.PostNonNestableDelayedTask(identity, task, 3600) }},
	}
	callers := make(map[uintptr]uint32, len(cases)+1)
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			handle := uintptr(index + 1)
			task := newPlatformTask(identity, handle)
			callers[handle] = runPlatformDefaultWithTimeout(t, func() { test.call(task) })
			if err := task.Close(); err == nil || !strings.Contains(err.Error(), "already consumed") {
				t.Fatalf("Close after default run = %v", err)
			}
		})
	}

	idleHandle := uintptr(len(cases) + 1)
	idle := newPlatformIdleTask(identity, idleHandle)
	callers[idleHandle] = runPlatformDefaultWithTimeout(t, func() { defaults.PostIdleTask(identity, idle) })
	if err := idle.Close(); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("IdleTask.Close after default run = %v", err)
	}

	events := platformDefaultsEvents()
	if len(events) != len(cases)+1 {
		t.Fatalf("native run/delete calls = %d, want %d: %#v", len(events), len(cases)+1, events)
	}
	seen := make(map[uintptr]int, len(events))
	for _, event := range events {
		seen[event.handle]++
		if event.dropped {
			t.Errorf("handle %d was dropped instead of run", event.handle)
		}
		if event.threadID != callers[event.handle] {
			t.Errorf("handle %d ran on thread %d, callback caller was %d", event.handle, event.threadID, callers[event.handle])
		}
		if event.handle == idleHandle {
			if !event.idle {
				t.Error("idle task used ordinary task run path")
			}
			if event.deadline != math.Float64bits(0) {
				t.Errorf("idle deadline bits = %#x, want +0.0 (%#x)", event.deadline, math.Float64bits(0))
			}
		} else if event.idle {
			t.Errorf("ordinary handle %d used idle run path", event.handle)
		}
	}
	for handle := uintptr(1); handle <= idleHandle; handle++ {
		if seen[handle] != 1 {
			t.Errorf("handle %d native run/delete count = %d, want 1", handle, seen[handle])
		}
	}
}

func TestPlatformImplFuncsNilCallbacksStillDrop(t *testing.T) {
	installPlatformDefaultsProbes(t)
	adapter := PlatformImplFuncs{}
	identity := PlatformIsolate(0x5678)
	tasks := []*Task{
		newPlatformTask(identity, 11), newPlatformTask(identity, 12),
		newPlatformTask(identity, 13), newPlatformTask(identity, 14),
	}
	adapter.PostTask(identity, tasks[0])
	adapter.PostNonNestableTask(identity, tasks[1])
	adapter.PostDelayedTask(identity, tasks[2], 3600)
	adapter.PostNonNestableDelayedTask(identity, tasks[3], 3600)
	idle := newPlatformIdleTask(identity, 15)
	adapter.PostIdleTask(identity, idle)

	events := platformDefaultsEvents()
	if len(events) != 5 {
		t.Fatalf("native drop calls = %d, want 5: %#v", len(events), events)
	}
	seen := make(map[uintptr]int, len(events))
	for _, event := range events {
		seen[event.handle]++
		if !event.dropped {
			t.Errorf("handle %d was run by nil-safe adapter", event.handle)
		}
	}
	for handle := uintptr(11); handle <= 15; handle++ {
		if seen[handle] != 1 {
			t.Errorf("handle %d native drop count = %d, want 1", handle, seen[handle])
		}
	}
	for _, task := range tasks {
		if err := task.Close(); err == nil || !strings.Contains(err.Error(), "already consumed") {
			t.Errorf("Task.Close after nil-safe drop = %v", err)
		}
	}
	if err := idle.Close(); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Errorf("IdleTask.Close after nil-safe drop = %v", err)
	}
}

func TestPlatformImplDefaultsRunCloseRaceConsumesOnce(t *testing.T) {
	installPlatformDefaultsProbes(t)
	defaults := PlatformImplDefaults{}
	identity := PlatformIsolate(0x9abc)
	for index := 0; index < 64; index++ {
		handle := uintptr(1000 + index)
		task := newPlatformTask(identity, handle)
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			defaults.PostTask(identity, task)
		}()
		go func() {
			defer wait.Done()
			<-start
			_ = task.Close()
		}()
		close(start)
		wait.Wait()
	}

	events := platformDefaultsEvents()
	counts := make(map[uintptr]int, 64)
	for _, event := range events {
		counts[event.handle]++
	}
	for index := 0; index < 64; index++ {
		handle := uintptr(1000 + index)
		if counts[handle] != 1 {
			t.Errorf("handle %d native run-or-drop count = %d, want 1", handle, counts[handle])
		}
	}
}

var _ PlatformImpl = PlatformImplDefaults{}
