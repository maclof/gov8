//go:build windows && amd64

package conformance_icu_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

const fixtureName = "conformance-icu-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

const validCommonDataProbe = "GOV8_ICU_VALID_COMMON_DATA_PROBE"

var (
	validCommonDataSetupErr error
	validCommonDataAlign    int
)

func TestMain(m *testing.M) {
	if os.Getenv(validCommonDataProbe) == "1" {
		source, err := os.ReadFile(filepath.Join("..", "rust-oracle", "tests", "fixtures", "icu", "icudtl-flutter-icu78.dat"))
		if err != nil {
			validCommonDataSetupErr = err
		} else if len(source) != 1_806_192 {
			validCommonDataSetupErr = fmt.Errorf("pinned ICU data length = %d", len(source))
		} else {
			backing := make([]byte, len(source)+15)
			base := uintptr(unsafe.Pointer(&backing[0]))
			offset := int((16 - base%16) % 16)
			aligned := backing[offset : offset+len(source)]
			copy(aligned, source)
			validCommonDataAlign = int(uintptr(unsafe.Pointer(&aligned[0])) % 16)
			validCommonDataSetupErr = gov8.ICUSetCommonData78(aligned)
			for index := range backing {
				backing[index] = 0
			}
			backing, aligned, source = nil, nil, nil
			runtime.GC()
		}
		if validCommonDataSetupErr == nil {
			validCommonDataSetupErr = gov8.Initialize()
		}
	}
	os.Exit(m.Run())
}

type result struct {
	OK    bool   `json:"ok"`
	Error *int32 `json:"error"`
}

type hostOriginal struct {
	Nonempty    bool `json:"nonempty"`
	ContainsNUL bool `json:"contains_nul"`
}

type acceptedUnchanged struct {
	Accepted  bool `json:"accepted"`
	Unchanged bool `json:"unchanged"`
}

type acceptedValue struct {
	Accepted bool   `json:"accepted"`
	Value    string `json:"value"`
}

func commonResult(err error) result {
	if err == nil {
		return result{OK: true}
	}
	var commonErr *gov8.ICUCommonDataError
	if !errors.As(err, &commonErr) {
		panic(err)
	}
	code := commonErr.Code
	return result{Error: &code}
}

func alignedInvalidCommonData() ([]byte, int) {
	backing := make([]byte, 16+15)
	base := uintptr(unsafe.Pointer(&backing[0]))
	offset := int((16 - base%16) % 16)
	data := backing[offset : offset+16]
	data[0], data[1], data[2] = 1, 2, 3
	return data, int(uintptr(unsafe.Pointer(&data[0])) % 16)
}

func checkedLanguageTag(t *testing.T) string {
	t.Helper()
	value, err := gov8.ICUGetLanguageTag()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func checkedSetLocale(t *testing.T, locale string) {
	t.Helper()
	if err := gov8.ICUSetDefaultLocale(locale); err != nil {
		t.Fatal(err)
	}
}

func checkedTimeZone(t *testing.T) string {
	t.Helper()
	value, err := gov8.ICUGetDefaultTimeZone()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func checkedSetTimeZone(t *testing.T, value string) bool {
	t.Helper()
	accepted, err := gov8.ICUSetDefaultTimeZone(value)
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func fixtureOutput(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	emit := func(value any) {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}

	data, pointerMod16 := alignedInvalidCommonData()
	emit(struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value struct {
			PointerMod16 int    `json:"pointer_mod_16"`
			Length       int    `json:"length"`
			First        result `json:"first"`
			Repeat       result `json:"repeat"`
		} `json:"value"`
	}{
		Check: "icu/common_data_invalid",
		OK:    true,
		Value: struct {
			PointerMod16 int    `json:"pointer_mod_16"`
			Length       int    `json:"length"`
			First        result `json:"first"`
			Repeat       result `json:"repeat"`
		}{pointerMod16, len(data), commonResult(gov8.ICUSetCommonData78(data)), commonResult(gov8.ICUSetCommonData78(data))},
	})

	originalLocale := checkedLanguageTag(t)
	checkedSetLocale(t, "nb_NO")
	nbNO := checkedLanguageTag(t)
	checkedSetLocale(t, "en_US_POSIX")
	posix := checkedLanguageTag(t)
	checkedSetLocale(t, "fr-FR")
	fr := checkedLanguageTag(t)
	checkedSetLocale(t, "")
	emptyLocale := checkedLanguageTag(t)
	checkedSetLocale(t, "zz_ZZ")
	unknownLocale := checkedLanguageTag(t)
	checkedSetLocale(t, "@")
	malformedLocale := checkedLanguageTag(t)
	checkedSetLocale(t, strings.Repeat("a", 2048))
	overlongLocale := checkedLanguageTag(t)
	checkedSetLocale(t, originalLocale)
	restoredLocale := checkedLanguageTag(t)
	emit(struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value struct {
			HostOriginal hostOriginal `json:"host_original_normalized"`
			NbNO         string       `json:"nb_NO"`
			Posix        string       `json:"en_US_POSIX"`
			FR           string       `json:"fr-FR"`
			Empty        string       `json:"empty"`
			Unknown      string       `json:"zz_ZZ"`
			Malformed    string       `json:"malformed"`
			Overlong     string       `json:"overlong"`
			Restored     bool         `json:"restored_exactly"`
		} `json:"value"`
	}{
		Check: "icu/locale_roundtrip_and_restore",
		OK:    true,
		Value: struct {
			HostOriginal hostOriginal `json:"host_original_normalized"`
			NbNO         string       `json:"nb_NO"`
			Posix        string       `json:"en_US_POSIX"`
			FR           string       `json:"fr-FR"`
			Empty        string       `json:"empty"`
			Unknown      string       `json:"zz_ZZ"`
			Malformed    string       `json:"malformed"`
			Overlong     string       `json:"overlong"`
			Restored     bool         `json:"restored_exactly"`
		}{hostOriginal{originalLocale != "", strings.ContainsRune(originalLocale, 0)}, nbNO, posix, fr, emptyLocale, unknownLocale, malformedLocale, overlongLocale, restoredLocale == originalLocale},
	})

	originalZone := checkedTimeZone(t)
	before := checkedTimeZone(t)
	invalid := checkedSetTimeZone(t, "Not/AZone")
	invalidUnchanged := checkedTimeZone(t) == before
	emptyZone := checkedSetTimeZone(t, "")
	emptyUnchanged := checkedTimeZone(t) == before
	nul := checkedSetTimeZone(t, "America/New\x00_York")
	nulUnchanged := checkedTimeZone(t) == before
	unknownZone := checkedSetTimeZone(t, "Etc/Unknown")
	unknownUnchanged := checkedTimeZone(t) == before
	utcSet := checkedSetTimeZone(t, "UTC")
	utc := checkedTimeZone(t)
	ianaSet := checkedSetTimeZone(t, "America/New_York")
	iana := checkedTimeZone(t)
	offsetSet := checkedSetTimeZone(t, "GMT+05:00")
	offset := checkedTimeZone(t)
	overlongSet := checkedSetTimeZone(t, strings.Repeat("A", 2048))
	overlongUnchanged := checkedTimeZone(t) == offset
	directRestore := checkedSetTimeZone(t, originalZone)
	if !directRestore {
		if !checkedSetTimeZone(t, "UTC") {
			t.Fatal("UTC restore rejected")
		}
	}
	restoredZone := checkedTimeZone(t)
	emit(struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value struct {
			HostOriginal hostOriginal      `json:"host_original_normalized"`
			Invalid      acceptedUnchanged `json:"invalid"`
			Empty        acceptedUnchanged `json:"empty"`
			InteriorNUL  acceptedUnchanged `json:"interior_nul"`
			ICUUnknown   acceptedUnchanged `json:"icu_unknown"`
			UTC          acceptedValue     `json:"utc"`
			IANA         acceptedValue     `json:"iana"`
			CustomOffset acceptedValue     `json:"custom_offset"`
			Overlong     acceptedUnchanged `json:"overlong_invalid"`
			Direct       bool              `json:"direct_restore_accepted"`
			Restore      bool              `json:"restore_matches_normalized"`
		} `json:"value"`
	}{
		Check: "icu/time_zone_validation_and_restore",
		OK:    true,
		Value: struct {
			HostOriginal hostOriginal      `json:"host_original_normalized"`
			Invalid      acceptedUnchanged `json:"invalid"`
			Empty        acceptedUnchanged `json:"empty"`
			InteriorNUL  acceptedUnchanged `json:"interior_nul"`
			ICUUnknown   acceptedUnchanged `json:"icu_unknown"`
			UTC          acceptedValue     `json:"utc"`
			IANA         acceptedValue     `json:"iana"`
			CustomOffset acceptedValue     `json:"custom_offset"`
			Overlong     acceptedUnchanged `json:"overlong_invalid"`
			Direct       bool              `json:"direct_restore_accepted"`
			Restore      bool              `json:"restore_matches_normalized"`
		}{
			hostOriginal{originalZone != "", strings.ContainsRune(originalZone, 0)},
			acceptedUnchanged{invalid, invalidUnchanged},
			acceptedUnchanged{emptyZone, emptyUnchanged},
			acceptedUnchanged{nul, nulUnchanged},
			acceptedUnchanged{unknownZone, unknownUnchanged},
			acceptedValue{utcSet, utc},
			acceptedValue{ianaSet, iana},
			acceptedValue{offsetSet, offset},
			acceptedUnchanged{overlongSet, overlongUnchanged},
			directRestore,
			map[bool]string{true: originalZone, false: "UTC"}[directRestore] == restoredZone,
		},
	})

	emit(struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}{Summary: struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	}{3, 3, 0}})
	return output.Bytes()
}

func TestRustOracleFixture(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "rust-oracle", "tests", "fixtures", fixtureName))
	if err != nil {
		t.Fatal(err)
	}
	got := fixtureOutput(t)
	if !bytes.Equal(got, want) {
		t.Fatalf("ICU fixture mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestValidCommonDataFreshProcess(t *testing.T) {
	if os.Getenv(validCommonDataProbe) == "1" {
		if validCommonDataSetupErr != nil {
			t.Fatal(validCommonDataSetupErr)
		}
		checkedSetLocale(t, "nb_NO")
		locale := checkedLanguageTag(t)
		timeZoneSet := checkedSetTimeZone(t, "UTC")
		timeZone := checkedTimeZone(t)

		iso, err := gov8.NewIsolate()
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
		t.Cleanup(func() {
			if err := scope.Close(); err != nil {
				t.Errorf("close scope: %v", err)
			}
			if err := ctx.Close(); err != nil {
				t.Errorf("close context: %v", err)
			}
			if err := gov8.ReleaseIsolateHostState(iso); err != nil {
				t.Errorf("release isolate host state: %v", err)
			}
			if err := iso.Close(); err != nil {
				t.Errorf("close isolate: %v", err)
			}
		})
		script, err := ctx.Compile(scope, "new Intl.NumberFormat('en-US').format(1234.5)", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := script.Close(); err != nil {
				t.Errorf("close script: %v", err)
			}
		}()
		value, err := script.Run(scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		intl, err := value.ToString(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("common=Ok(());align=%d;locale=%s;timezone_set=%t;timezone=%s;intl=%s\n",
			validCommonDataAlign, locale, timeZoneSet, timeZone, intl)
		const want = "common=Ok(());align=0;locale=nb-NO;timezone_set=true;timezone=UTC;intl=1,234.5\n"
		if got != want {
			t.Fatalf("valid common-data observation\nwant: %q\ngot:  %q", want, got)
		}
		return
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run", "^TestValidCommonDataFreshProcess$", "-test.count=1")
	command.Env = append(os.Environ(), validCommonDataProbe+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("valid common-data subprocess: %v\n%s", err, output)
	}
}
