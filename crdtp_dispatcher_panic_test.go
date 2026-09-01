//go:build windows && amd64

package gov8

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type crdtpPanicChannel struct{ mode string }

func (c *crdtpPanicChannel) SendProtocolResponse(_ int32, message *CRDTPSerializable) {
	if c.mode == "channel" {
		fmt.Fprintln(os.Stderr, "marker:entered-channel")
		panic("CRDTP channel panic marker")
	}
	_ = message.Close()
}
func (*crdtpPanicChannel) SendProtocolNotification(message *CRDTPSerializable) {
	_ = message.Close()
}
func (*crdtpPanicChannel) FlushProtocolNotifications() {}
func (c *crdtpPanicChannel) CRDTPCallbackDropped() {
	if c.mode == "channel-drop" {
		fmt.Fprintln(os.Stderr, "marker:entered-channel-drop")
		panic("CRDTP channel-drop panic marker")
	}
}

type crdtpPanicDomain struct{ mode string }

func (d *crdtpPanicDomain) Dispatch([]byte, *CRDTPDispatchRequest, *CRDTPDomainResponder) bool {
	if d.mode == "domain" {
		fmt.Fprintln(os.Stderr, "marker:entered-domain")
		panic("CRDTP domain panic marker")
	}
	return false
}
func (d *crdtpPanicDomain) CRDTPCallbackDropped() {
	if d.mode == "domain-drop" {
		fmt.Fprintln(os.Stderr, "marker:entered-domain-drop")
		panic("CRDTP domain-drop panic marker")
	}
}

type crdtpPanicFallthrough struct{ mode string }

func (f *crdtpPanicFallthrough) FallThrough(int32, []byte, []byte, []byte) {
	if f.mode == "fallthrough" {
		fmt.Fprintln(os.Stderr, "marker:entered-fallthrough")
		panic("CRDTP fallthrough panic marker")
	}
}
func (f *crdtpPanicFallthrough) CRDTPCallbackDropped() {
	if f.mode == "fallthrough-drop" {
		fmt.Fprintln(os.Stderr, "marker:entered-fallthrough-drop")
		panic("CRDTP fallthrough-drop panic marker")
	}
}

func TestCRDTPDispatcherCallbackPanicsFailFast(t *testing.T) {
	if mode := os.Getenv("GOV8_CRDTP_DISPATCHER_PANIC_CHILD"); mode != "" {
		channel, err := NewCRDTPFrontendChannel(&crdtpPanicChannel{mode})
		if err != nil {
			t.Fatal(err)
		}
		dispatcher, err := NewCRDTPUberDispatcher(channel)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stderr, "marker:before-"+mode)
		switch mode {
		case "domain", "domain-drop":
			if err := dispatcher.WireDomain("Panic", &crdtpPanicDomain{mode}); err != nil {
				t.Fatal(err)
			}
			if mode == "domain" {
				message, _ := NewCRDTPDispatchable(mustCRDTPCBORT(t, `{"id":1,"method":"Panic.run"}`), nil)
				_ = dispatcher.Dispatch(message)
			} else {
				_ = dispatcher.Close()
			}
		case "channel":
			message, _ := NewCRDTPDispatchable(mustCRDTPCBORT(t, `{"id":1,"method":"Missing.run"}`), nil)
			_ = dispatcher.Dispatch(message)
		case "channel-drop":
			_ = dispatcher.Close()
			_ = channel.Close()
		case "fallthrough", "fallthrough-drop":
			message, _ := NewCRDTPDispatchableWithFallthrough(
				mustCRDTPCBORT(t, `{"id":1,"method":"Missing.run"}`), nil,
				&crdtpPanicFallthrough{mode})
			if mode == "fallthrough" {
				_ = dispatcher.Dispatch(message)
			} else {
				_ = message.Close()
			}
		}
		fmt.Fprintln(os.Stderr, "marker:after-"+mode)
		os.Exit(91)
	}

	for _, mode := range []string{"domain", "channel", "fallthrough", "domain-drop", "channel-drop", "fallthrough-drop"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCRDTPDispatcherCallbackPanicsFailFast$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_CRDTP_DISPATCHER_PANIC_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s exit=%v, want 0xC0000409; output:\n%s", mode, err, output)
		}
		text := string(output)
		for _, marker := range []string{"marker:before-" + mode, "marker:entered-" + mode, "panic marker"} {
			if !strings.Contains(text, marker) {
				t.Fatalf("%s missing %q:\n%s", mode, marker, text)
			}
		}
		if strings.Contains(text, "marker:after-"+mode) {
			t.Fatalf("%s returned after panic:\n%s", mode, text)
		}
	}
}
