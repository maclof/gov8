//go:build windows && amd64

package gov8_test

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

func restoreICULocale(t *testing.T) string {
	t.Helper()
	original, err := gov8.ICUGetLanguageTag()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := gov8.ICUSetDefaultLocale(original); err != nil {
			t.Errorf("restore ICU locale: %v", err)
		}
	})
	return original
}

func restoreICUTimeZone(t *testing.T) string {
	t.Helper()
	original, err := gov8.ICUGetDefaultTimeZone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		accepted, err := gov8.ICUSetDefaultTimeZone(original)
		if err != nil {
			t.Errorf("restore ICU time zone: %v", err)
			return
		}
		if !accepted {
			accepted, err = gov8.ICUSetDefaultTimeZone("UTC")
			if err != nil || !accepted {
				t.Errorf("fallback ICU time-zone restore = %v, %v", accepted, err)
			}
		}
	})
	return original
}

func TestICUCommonDataInvalidAndSafeBoundaries(t *testing.T) {
	invalid := []byte{1, 2, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for range 2 {
		err := gov8.ICUSetCommonData78(invalid)
		var commonErr *gov8.ICUCommonDataError
		if !errors.As(err, &commonErr) || commonErr.Code != 3 {
			t.Fatalf("invalid common data = %#v", err)
		}
	}

	// rusty_v8's empty slice access-violates because ICU has no length
	// parameter. gov8 pads its private native copy and returns ICU's error.
	err := gov8.ICUSetCommonData78(nil)
	var commonErr *gov8.ICUCommonDataError
	if !errors.As(err, &commonErr) || commonErr.Code != 3 {
		t.Fatalf("empty common data = %#v", err)
	}

	// The Go API also accepts an arbitrarily aligned input because ICU only sees
	// the aligned native copy retained by the shim.
	misalignedBacking := make([]byte, len(invalid)+16)
	base := uintptr(unsafe.Pointer(&misalignedBacking[0]))
	offset := int((1 + 16 - base%16) % 16)
	misaligned := misalignedBacking[offset : offset+len(invalid)]
	copy(misaligned, invalid)
	if uintptr(unsafe.Pointer(&misaligned[0]))%16 == 0 {
		t.Fatal("failed to construct misaligned test input")
	}
	err = gov8.ICUSetCommonData78(misaligned)
	if !errors.As(err, &commonErr) || commonErr.Code != 3 {
		t.Fatalf("misaligned common data = %#v", err)
	}
	for i := range invalid {
		invalid[i] = 0xff
	}
	runtime.GC()
	if tag, err := gov8.ICUGetLanguageTag(); err != nil || tag == "" {
		t.Fatalf("ICU after input release = %q, %v", tag, err)
	}
}

func TestICULocaleRoundTripNULAndLifecycle(t *testing.T) {
	original := restoreICULocale(t)
	if original == "" || strings.ContainsRune(original, 0) {
		t.Fatalf("original locale = %q", original)
	}

	cases := []struct {
		input string
		want  string
	}{
		{"nb_NO", "nb-NO"},
		{"en_US_POSIX", "en-US-u-va-posix"},
		{"fr-FR", "fr-FR"},
		{"", "und"},
		{"zz_ZZ", "zz-ZZ"},
		{"@", "und"},
		{strings.Repeat("a", 2048), "und"},
	}
	for _, tc := range cases {
		if err := gov8.ICUSetDefaultLocale(tc.input); err != nil {
			t.Fatalf("set locale %q: %v", tc.input, err)
		}
		got, err := gov8.ICUGetLanguageTag()
		if err != nil || got != tc.want {
			t.Fatalf("locale %q = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}

	before, err := gov8.ICUGetLanguageTag()
	if err != nil {
		t.Fatal(err)
	}
	if err := gov8.ICUSetDefaultLocale("en\x00US"); err == nil {
		t.Fatal("locale containing NUL was accepted")
	}
	if err := gov8.ICUSetDefaultLocale(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 locale was accepted")
	}
	after, err := gov8.ICUGetLanguageTag()
	if err != nil || after != before {
		t.Fatalf("rejected locale mutated default: before=%q after=%q err=%v", before, after, err)
	}

	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if tag, err := gov8.ICUGetLanguageTag(); err != nil || tag != before {
		t.Fatalf("locale after isolate lifecycle = %q, %v", tag, err)
	}
}

func TestICUTimeZoneValidationAndRestore(t *testing.T) {
	original := restoreICUTimeZone(t)
	if original == "" || strings.ContainsRune(original, 0) {
		t.Fatalf("original time zone = %q", original)
	}
	before := original
	for _, value := range []string{"Not/AZone", "", "America/New\x00_York", "Etc/Unknown"} {
		accepted, err := gov8.ICUSetDefaultTimeZone(value)
		if err != nil || accepted {
			t.Fatalf("invalid time zone %q = %v, %v", value, accepted, err)
		}
		got, err := gov8.ICUGetDefaultTimeZone()
		if err != nil || got != before {
			t.Fatalf("invalid time zone %q changed default to %q, %v", value, got, err)
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"UTC", "UTC"},
		{"America/New_York", "America/New_York"},
		{"GMT+05:00", "GMT+05:00"},
	} {
		accepted, err := gov8.ICUSetDefaultTimeZone(tc.input)
		if err != nil || !accepted {
			t.Fatalf("set time zone %q = %v, %v", tc.input, accepted, err)
		}
		got, err := gov8.ICUGetDefaultTimeZone()
		if err != nil || got != tc.want {
			t.Fatalf("time zone %q = %q, %v", tc.input, got, err)
		}
		before = got
	}
	accepted, err := gov8.ICUSetDefaultTimeZone(strings.Repeat("A", 2048))
	if err != nil || accepted {
		t.Fatalf("overlong time zone = %v, %v", accepted, err)
	}
	if got, err := gov8.ICUGetDefaultTimeZone(); err != nil || got != before {
		t.Fatalf("overlong time zone changed default = %q, %v", got, err)
	}
	if accepted, err := gov8.ICUSetDefaultTimeZone(string([]byte{0xff})); err == nil || accepted {
		t.Fatalf("invalid UTF-8 time zone = %v, %v", accepted, err)
	}
}

func TestICUConcurrentProcessGlobalAccess(t *testing.T) {
	restoreICULocale(t)
	restoreICUTimeZone(t)
	locales := []string{"en_US", "fr_FR", "nb_NO", "de_DE"}
	zones := []string{"UTC", "America/New_York", "GMT+05:00", "Europe/London"}
	var wg sync.WaitGroup
	errorsOut := make(chan error, len(locales)*2)
	for index := range locales {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for range 25 {
				if err := gov8.ICUSetDefaultLocale(locales[index]); err != nil {
					errorsOut <- err
					return
				}
				if tag, err := gov8.ICUGetLanguageTag(); err != nil || tag == "" {
					errorsOut <- errors.New("concurrent ICU locale read failed")
					return
				}
				accepted, err := gov8.ICUSetDefaultTimeZone(zones[index])
				if err != nil || !accepted {
					errorsOut <- errors.New("concurrent ICU time-zone set failed")
					return
				}
				if zone, err := gov8.ICUGetDefaultTimeZone(); err != nil || zone == "" {
					errorsOut <- errors.New("concurrent ICU time-zone read failed")
					return
				}
			}
		}(index)
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Error(err)
	}
}
