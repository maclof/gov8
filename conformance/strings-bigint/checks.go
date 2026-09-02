//go:build windows && amd64

// The 17 advanced strings/BigInt checks in the fixed oracle order (the
// JSON-lines fixture follows exactly this order): all strings/ checks
// precede all bigint/ checks. Every check builds the fixed expectation the
// pinned oracle binary produces and the Go observation of the same calls.
package main

import (
	"bytes"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// --- strings ------------------------------------------------------------------------

// checkMaxLengthAndEmpty pins String::MAX_LENGTH and the empty-string
// surface (String::empty, empty new, and their view flavors).
func checkMaxLengthAndEmpty(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	maxLength, err := gov8.StringMaxLength()
	if err != nil {
		t.Fatalf("StringMaxLength: %v", err)
	}
	empty, err := r.scope.EmptyString()
	if err != nil {
		t.Fatalf("EmptyString: %v", err)
	}
	newEmpty, err := r.scope.NewString("")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	view, err := r.scope.NewStringView(empty)
	if err != nil {
		t.Fatalf("NewStringView: %v", err)
	}
	oneByte, length, err := view.Info()
	if err != nil {
		t.Fatalf("view.Info: %v", err)
	}
	if err := view.Close(); err != nil {
		t.Fatalf("view.Close: %v", err)
	}
	kind := "twobyte"
	if oneByte {
		kind = "onebyte"
	}
	// The view length must match the empty string's reported length.
	if length != 0 {
		t.Fatalf("empty view length = %d", length)
	}

	return wantGot("strings/max_length_and_empty",
		obj(
			kv("max_length", i(536870888)),
			kv("empty", obj(
				kv("length", i(0)),
				kv("utf8_length", i(0)),
				kv("is_onebyte", b(true)),
				kv("view", obj(kv("kind", s("onebyte")), kv("len", i(0)))),
			)),
			kv("new_empty", obj(kv("length", i(0)))),
		),
		obj(
			kv("max_length", i(int64(maxLength))),
			kv("empty", obj(
				kv("length", i(int64(mustLength(t, empty)))),
				kv("utf8_length", i(int64(mustUtf8Length(t, empty)))),
				kv("is_onebyte", b(mustOneByte(t, empty))),
				kv("view", obj(kv("kind", s(kind)), kv("len", i(int64(length))))),
			)),
			kv("new_empty", obj(kv("length", i(int64(mustLength(t, newEmpty)))))),
		))
}

// checkCreationTypes pins the creation flavors: UTF-8 (lossy on invalid
// input), internalized, Latin-1 one-byte, UTF-16 two-byte with
// Latin-1-collapse and surrogate pairs.
func checkCreationTypes(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	ascii, err := r.scope.NewString("hello oracle")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	invalid, err := r.scope.NewStringFromUTF8([]byte{'a', 'b', 0xFF, 'c', 'd'}, gov8.StringNormal)
	if err != nil {
		t.Fatalf("NewStringFromUTF8: %v", err)
	}
	internalizedA, err := r.scope.NewStringFromUTF8([]byte("intern-me"), gov8.StringInternalized)
	if err != nil {
		t.Fatalf("NewStringFromUTF8 internalized: %v", err)
	}
	internalizedB, err := r.scope.NewStringFromUTF8([]byte("intern-me"), gov8.StringInternalized)
	if err != nil {
		t.Fatalf("NewStringFromUTF8 internalized: %v", err)
	}
	latin1, err := r.scope.NewStringFromOneByte([]byte{0xE9, 0x41}, gov8.StringNormal)
	if err != nil {
		t.Fatalf("NewStringFromOneByte: %v", err)
	}
	twobyteLatin1, err := r.scope.NewStringFromTwoByte([]uint16{0xE9}, gov8.StringNormal)
	if err != nil {
		t.Fatalf("NewStringFromTwoByte: %v", err)
	}
	emoji, err := r.scope.NewStringFromTwoByte([]uint16{0xD83E, 0xDD80, 0x0041}, gov8.StringNormal)
	if err != nil {
		t.Fatalf("NewStringFromTwoByte emoji: %v", err)
	}

	asciiText := valueText(t, r, ascii)
	invalidText := valueText(t, r, invalid)
	internTextA := valueText(t, r, internalizedA)
	latin1Text := valueText(t, r, latin1)
	tbLatin1Text := valueText(t, r, twobyteLatin1)
	emojiText := valueText(t, r, emoji)
	emojiIsOneByte := mustOneByte(t, emoji)
	emojiOnlyOneByte := mustContainsOnlyOneByte(t, emoji)

	return wantGot("strings/creation_types",
		obj(
			kv("ascii", obj(
				kv("length", i(12)), kv("utf8_length", i(12)),
				kv("text", s("hello oracle")),
				kv("is_onebyte", b(true)), kv("contains_only_onebyte", b(true)))),
			kv("invalid_utf8", obj(
				kv("length", i(5)), kv("utf8_length", i(7)),
				kv("text", s("ab\uFFFDcd")))),
			kv("internalized", obj(
				kv("length", i(9)), kv("text", s("intern-me")),
				kv("is_onebyte", b(true)), kv("is_external", b(false)),
				kv("same_content_as_b", b(true)))),
			kv("latin1", obj(
				kv("length", i(2)), kv("utf8_length", i(3)),
				kv("text", s("\u00e9A")),
				kv("is_onebyte", b(true)))),
			kv("twobyte_entry_latin1_content", obj(
				kv("length", i(1)), kv("text", s("\u00e9")),
				kv("is_onebyte", b(true)), kv("contains_only_onebyte", b(true)))),
			kv("emoji_surrogate_pair", obj(
				kv("length", i(3)), kv("utf8_length", i(5)),
				kv("text", s("\U0001F980A")),
				kv("is_onebyte", b(false)), kv("contains_only_onebyte", b(false)))),
		),
		obj(
			kv("ascii", obj(
				kv("length", i(int64(mustLength(t, ascii)))),
				kv("utf8_length", i(int64(mustUtf8Length(t, ascii)))),
				kv("text", s(asciiText)),
				kv("is_onebyte", b(mustOneByte(t, ascii))),
				kv("contains_only_onebyte", b(mustContainsOnlyOneByte(t, ascii))))),
			kv("invalid_utf8", obj(
				kv("length", i(int64(mustLength(t, invalid)))),
				kv("utf8_length", i(int64(mustUtf8Length(t, invalid)))),
				kv("text", s(invalidText)))),
			kv("internalized", obj(
				kv("length", i(int64(mustLength(t, internalizedA)))),
				kv("text", s(internTextA)),
				kv("is_onebyte", b(mustOneByte(t, internalizedA))),
				kv("is_external", b(mustExternalString(t, internalizedA))),
				kv("same_content_as_b", b(internTextA == valueText(t, r, internalizedB))))),
			kv("latin1", obj(
				kv("length", i(int64(mustLength(t, latin1)))),
				kv("utf8_length", i(int64(mustUtf8Length(t, latin1)))),
				kv("text", s(latin1Text)),
				kv("is_onebyte", b(mustOneByte(t, latin1))))),
			kv("twobyte_entry_latin1_content", obj(
				kv("length", i(int64(mustLength(t, twobyteLatin1)))),
				kv("text", s(tbLatin1Text)),
				kv("is_onebyte", b(mustOneByte(t, twobyteLatin1))),
				kv("contains_only_onebyte", b(mustContainsOnlyOneByte(t, twobyteLatin1))))),
			kv("emoji_surrogate_pair", obj(
				kv("length", i(int64(mustLength(t, emoji)))),
				kv("utf8_length", i(int64(mustUtf8Length(t, emoji)))),
				kv("text", s(emojiText)),
				kv("is_onebyte", b(emojiIsOneByte)),
				kv("contains_only_onebyte", b(emojiOnlyOneByte)))),
		))
}

// checkConcatSemantics pins String::concat: content, associativity, empty
// identity, one-byte retention, external interaction, and JS visibility.
func checkConcatSemantics(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	lhs := mustString(t, r, "foo")
	bar := mustString(t, r, "bar")
	tail := mustString(t, r, "baz")
	empty, err := r.scope.EmptyString()
	if err != nil {
		t.Fatalf("EmptyString: %v", err)
	}

	foobar, err := r.scope.ConcatString(lhs, bar)
	if err != nil {
		t.Fatalf("ConcatString: %v", err)
	}
	leftInner, err := r.scope.ConcatString(lhs, bar)
	if err != nil {
		t.Fatal(err)
	}
	leftAssoc, err := r.scope.ConcatString(leftInner, tail)
	if err != nil {
		t.Fatal(err)
	}
	rightInner, err := r.scope.ConcatString(bar, tail)
	if err != nil {
		t.Fatal(err)
	}
	rightAssoc, err := r.scope.ConcatString(lhs, rightInner)
	if err != nil {
		t.Fatal(err)
	}
	emptyLeft, err := r.scope.ConcatString(empty, bar)
	if err != nil {
		t.Fatal(err)
	}
	emptyRight, err := r.scope.ConcatString(bar, empty)
	if err != nil {
		t.Fatal(err)
	}

	chained := mustString(t, r, "x")
	for n := 0; n < 8; n++ {
		next := mustString(t, r, "y"+string(rune('0'+n)))
		chained, err = r.scope.ConcatString(chained, next)
		if err != nil {
			t.Fatalf("chained concat %d: %v", n, err)
		}
	}

	ext, err := r.scope.NewExternalOneByteStringStatic([]byte("EXT"))
	if err != nil {
		t.Fatalf("NewExternalOneByteStringStatic: %v", err)
	}
	extCat, err := r.scope.ConcatString(ext, mustString(t, r, "!"))
	if err != nil {
		t.Fatal(err)
	}

	jsCat, err := r.scope.ConcatString(mustString(t, r, "JS"), mustString(t, r, "SEES"))
	if err != nil {
		t.Fatal(err)
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if ok, serr := global.SetByName(r.scope, r.ctx, "cat", jsCat); serr != nil || !ok {
		t.Fatalf("SetByName cat: %v, %v", ok, serr)
	}

	return wantGot("strings/concat_semantics",
		obj(
			kv("basic", obj(
				kv("length", i(6)), kv("text", s("foobar")),
				kv("is_onebyte", b(true)), kv("contains_only_onebyte", b(true)))),
			kv("associative", b(true)),
			kv("assoc_text", s("foobarbaz")),
			kv("empty_left_text", s("bar")),
			kv("empty_right_text", s("bar")),
			kv("chained", obj(kv("length", i(17)), kv("text", s("xy0y1y2y3y4y5y6y7")))),
			kv("with_external", obj(
				kv("text", s("EXT!")),
				kv("is_onebyte", b(true)), kv("is_external", b(false)))),
			kv("js_sees", s("JSSEES")),
			kv("js_eq", s("EQ")),
		),
		obj(
			kv("basic", obj(
				kv("length", i(int64(mustLength(t, foobar)))),
				kv("text", s(valueText(t, r, foobar))),
				kv("is_onebyte", b(mustOneByte(t, foobar))),
				kv("contains_only_onebyte", b(mustContainsOnlyOneByte(t, foobar))))),
			kv("associative", b(valueText(t, r, leftAssoc) == valueText(t, r, rightAssoc))),
			kv("assoc_text", s(valueText(t, r, leftAssoc))),
			kv("empty_left_text", s(valueText(t, r, emptyLeft))),
			kv("empty_right_text", s(valueText(t, r, emptyRight))),
			kv("chained", obj(
				kv("length", i(int64(mustLength(t, chained)))),
				kv("text", s(valueText(t, r, chained))))),
			kv("with_external", obj(
				kv("text", s(valueText(t, r, extCat))),
				kv("is_onebyte", b(mustOneByte(t, extCat))),
				kv("is_external", b(mustExternalString(t, extCat))))),
			kv("js_sees", s(r.evalText(t, "cat"))),
			kv("js_eq", s(r.evalText(t, "cat === 'JSSEES' ? 'EQ' : 'NEQ'"))),
		))
}

// checkWriteTwoByteViews pins write_v2 (UTF-16 units): full/partial/
// remainder-offset writes, kNullTerminate, and the untouched poison tail.
func checkWriteTwoByteViews(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	s := mustString(t, r, "ab\U0001F980")

	full := make([]uint16, 8)
	poisonU16(full)
	if _, err := s.WriteTwoByte(0, full, 0); err != nil {
		t.Fatalf("WriteTwoByte: %v", err)
	}
	partial := make([]uint16, 2)
	poisonU16(partial)
	if _, err := s.WriteTwoByte(0, partial, 0); err != nil {
		t.Fatal(err)
	}
	remainder := make([]uint16, 3)
	poisonU16(remainder)
	if _, err := s.WriteTwoByte(1, remainder, 0); err != nil {
		t.Fatal(err)
	}
	nullterm := make([]uint16, 8)
	poisonU16(nullterm)
	if _, err := s.WriteTwoByte(0, nullterm, gov8.WriteNullTerminate); err != nil {
		t.Fatal(err)
	}

	fullTail := true
	for _, u := range full[4:] {
		fullTail = fullTail && u == 0xABAB
	}
	nullTail := true
	for _, u := range nullterm[5:] {
		nullTail = nullTail && u == 0xABAB
	}

	return wantGot("strings/write_two_byte_views",
		obj(
			kv("length", i(4)),
			kv("full", arr(i(0x61), i(0x62), i(0xD83E), i(0xDD80))),
			kv("full_tail_untouched", b(true)),
			kv("partial_2", arr(i(0x61), i(0x62))),
			kv("remainder_from_offset_1", arr(i(0x62), i(0xD83E), i(0xDD80))),
			kv("nullterm", arr(i(0x61), i(0x62), i(0xD83E), i(0xDD80), i(0))),
			kv("nullterm_tail_untouched", b(true)),
		),
		obj(
			kv("length", i(int64(mustLength(t, s)))),
			kv("full", unitsJSON(full, 4)),
			kv("full_tail_untouched", b(fullTail)),
			kv("partial_2", unitsJSON(partial, 2)),
			kv("remainder_from_offset_1", unitsJSON(remainder, 3)),
			kv("nullterm", unitsJSON(nullterm, 5)),
			kv("nullterm_tail_untouched", b(nullTail)),
		))
}

// checkWriteOneByteViews pins write_one_byte_v2: Latin-1 verbatim, low-byte
// truncation of two-byte content, offset/remainder, and kNullTerminate.
func checkWriteOneByteViews(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	ab := mustString(t, r, "ab")
	latin1, err := r.scope.NewStringFromOneByte([]byte{0xE9, 0x41}, gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	euro := mustString(t, r, "\u20ACA")
	emoji := mustString(t, r, "\U0001F980")

	write := func(v gov8.Value, offset int, size int, flags gov8.WriteFlags) []byte {
		t.Helper()
		buf := make([]byte, size)
		poison(buf)
		if _, err := v.WriteOneByte(offset, buf, flags); err != nil {
			t.Fatalf("WriteOneByte: %v", err)
		}
		return buf
	}

	asciiBuf := write(ab, 0, 8, 0)
	latin1Buf := write(latin1, 0, 8, 0)
	euroBuf := write(euro, 0, 8, 0)
	emojiBuf := write(emoji, 0, 8, 0)
	remainderBuf := write(ab, 1, 1, 0)
	nulltermBuf := write(ab, 0, 8, gov8.WriteNullTerminate)

	asciiTail := true
	for _, by := range asciiBuf[2:] {
		asciiTail = asciiTail && by == 0xAB
	}

	return wantGot("strings/write_one_byte_views",
		obj(
			kv("ascii", s("6162")),
			kv("ascii_tail_untouched", b(true)),
			kv("latin1", s("e941")),
			kv("euro_low_byte_truncation", s("ac41")),
			kv("emoji_low_byte_truncation", s("3e80")),
			kv("remainder_from_offset_1", s("62")),
			kv("nullterm", s("616200")),
			kv("uninit", s("6162")),
		),
		obj(
			kv("ascii", s(lowerHex(asciiBuf[:2]))),
			kv("ascii_tail_untouched", b(asciiTail)),
			kv("latin1", s(lowerHex(latin1Buf[:2]))),
			kv("euro_low_byte_truncation", s(lowerHex(euroBuf[:2]))),
			kv("emoji_low_byte_truncation", s(lowerHex(emojiBuf[:2]))),
			kv("remainder_from_offset_1", s(lowerHex(remainderBuf))),
			kv("nullterm", s(lowerHex(nulltermBuf[:3]))),
			kv("uninit", s(lowerHex(asciiBuf[:2]))),
		))
}

// checkWriteUTF8Views pins write_utf8_v2: exact counts, processed
// characters, capacity truncation that never splits a sequence,
// kNullTerminate (the NUL counts toward the byte count), and lone
// surrogates with and without kReplaceInvalidUtf8.
func checkWriteUTF8Views(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	abc := mustString(t, r, "abc")
	emoji := mustString(t, r, "a\U0001F980b")
	he := mustString(t, r, "h\u00e9")
	lone, err := r.scope.NewStringFromTwoByte([]uint16{0xD83E, 0x0061}, gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}

	write := func(v gov8.Value, cap int, flags gov8.WriteFlags) ([]byte, int, int) {
		t.Helper()
		buf := make([]byte, 32)
		poison(buf)
		n, processed, err := v.WriteUTF8(buf[:cap], flags)
		if err != nil {
			t.Fatalf("WriteUTF8: %v", err)
		}
		return buf, n, processed
	}

	asciiBuf, asciiN, asciiProcessed := write(abc, 32, 0)
	cap3Buf, cap3N, _ := write(emoji, 3, 0)
	cap4Buf, cap4N, _ := write(emoji, 4, 0)
	cap5Buf, cap5N, _ := write(emoji, 5, 0)
	fullBuf, fullN, _ := write(emoji, 32, gov8.WriteNullTerminate)
	heCap4Buf, heCap4N, _ := write(he, 4, gov8.WriteNullTerminate)
	heCap3Buf, heCap3N, _ := write(he, 3, gov8.WriteNullTerminate)
	heCap2Buf, heCap2N, _ := write(he, 2, gov8.WriteNullTerminate)
	loneBuf, loneN, loneProcessed := write(lone, 32, 0)
	loneFixedBuf, loneFixedN, _ := write(lone, 32, gov8.WriteReplaceInvalidUTF8)
	_, emptyN, _ := write(abc, 0, 0)

	return wantGot("strings/write_utf8_views",
		obj(
			kv("ascii_bytes", i(3)), kv("ascii_processed", i(3)),
			kv("ascii_hex", s("616263")),
			kv("cap3_bytes", i(1)), kv("cap3_hex", s("61")),
			kv("cap4_bytes", i(1)), kv("cap4_hex", s("61")),
			kv("cap5_bytes", i(5)), kv("cap5_hex", s("61f09fa680")),
			kv("full_nullterm_bytes", i(7)), kv("full_nullterm_hex", s("61f09fa6806200")),
			kv("he_utf8_length", i(3)),
			kv("he_cap4_bytes", i(4)), kv("he_cap4_hex", s("68c3a900")),
			kv("he_cap3_bytes", i(2)), kv("he_cap3_hex", s("6800ab")),
			kv("he_cap2_bytes", i(2)), kv("he_cap2_hex", s("6800")),
			kv("lone_surrogate_raw_bytes", i(4)), kv("lone_surrogate_raw_processed", i(2)),
			kv("lone_surrogate_raw_hex", s("eda0be61")),
			kv("lone_surrogate_replaced_bytes", i(4)),
			kv("lone_surrogate_replaced_hex", s("efbfbd61")),
			kv("empty_buffer_bytes", i(0)),
		),
		obj(
			kv("ascii_bytes", i(int64(asciiN))), kv("ascii_processed", i(int64(asciiProcessed))),
			kv("ascii_hex", s(lowerHex(asciiBuf[:3]))),
			kv("cap3_bytes", i(int64(cap3N))), kv("cap3_hex", s(lowerHex(cap3Buf[:1]))),
			kv("cap4_bytes", i(int64(cap4N))), kv("cap4_hex", s(lowerHex(cap4Buf[:1]))),
			kv("cap5_bytes", i(int64(cap5N))), kv("cap5_hex", s(lowerHex(cap5Buf[:5]))),
			kv("full_nullterm_bytes", i(int64(fullN))), kv("full_nullterm_hex", s(lowerHex(fullBuf[:7]))),
			kv("he_utf8_length", i(int64(mustUtf8Length(t, he)))),
			kv("he_cap4_bytes", i(int64(heCap4N))), kv("he_cap4_hex", s(lowerHex(heCap4Buf[:4]))),
			kv("he_cap3_bytes", i(int64(heCap3N))), kv("he_cap3_hex", s(lowerHex(heCap3Buf[:3]))),
			kv("he_cap2_bytes", i(int64(heCap2N))), kv("he_cap2_hex", s(lowerHex(heCap2Buf[:2]))),
			kv("lone_surrogate_raw_bytes", i(int64(loneN))), kv("lone_surrogate_raw_processed", i(int64(loneProcessed))),
			kv("lone_surrogate_raw_hex", s(lowerHex(loneBuf[:4]))),
			kv("lone_surrogate_replaced_bytes", i(int64(loneFixedN))),
			kv("lone_surrogate_replaced_hex", s(lowerHex(loneFixedBuf[:4]))),
			kv("empty_buffer_bytes", i(int64(emptyN))),
		))
}

// checkValueViewFlavors pins the ValueView encoding flavors (OneByte /
// TwoByte per representation), the ASCII-only as_str rule, and the
// borrowed/owned to_cow_lossy split (borrowed == one-byte content usable
// as-is, i.e. ASCII).
func checkValueViewFlavors(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	describe := func(v gov8.Value) (kind string, length int64, content string, asStrIsSome bool) {
		t.Helper()
		view, err := r.scope.NewStringView(v)
		if err != nil {
			t.Fatalf("NewStringView: %v", err)
		}
		defer func() {
			if err := view.Close(); err != nil {
				t.Errorf("view.Close: %v", err)
			}
		}()
		viewOneByte, viewLength, ierr := view.Info()
		if ierr != nil {
			t.Fatalf("view.Info: %v", ierr)
		}
		data, _, err := view.Bytes()
		if err != nil {
			t.Fatalf("view.Bytes: %v", err)
		}
		if viewOneByte {
			kind = "onebyte"
			content = lowerHex(data)
			asStrIsSome = isASCII(data)
			return kind, int64(viewLength), content, asStrIsSome
		}
		kind = "twobyte"
		if len(data) >= 2 {
			// first_unit_hex: the first code unit in big-endian byte order.
			content = lowerHex([]byte{data[1], data[0]})
		}
		return kind, int64(viewLength), content, asStrIsSome
	}

	ascii := mustString(t, r, "hello")
	asciiKind, asciiLen, asciiHex, asciiAsStr := describe(ascii)
	asciiCowBorrowed := asciiAsStr // to_cow_lossy borrows exactly when as_str is Some for one-byte ASCII

	euro := mustString(t, r, "\u20ac")
	euroKind, euroLen, euroFirstUnit, euroAsStr := describe(euro)

	latin1, err := r.scope.NewStringFromOneByte([]byte{0xE9, 0x41}, gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	latin1Kind, latin1Len, latin1Hex, latin1AsStr := describe(latin1)

	empty, err := r.scope.EmptyString()
	if err != nil {
		t.Fatal(err)
	}
	emptyKind, emptyLen, emptyHex, emptyAsStr := describe(empty)

	// euro_cow: the lossy text rendering of the two-byte view contents.
	euroCow := describeText(t, r, euro)

	return wantGot("strings/value_view_flavors",
		obj(
			kv("ascii", obj(
				kv("kind", s("onebyte")), kv("len", i(5)),
				kv("hex", s("68656c6c6f")), kv("as_str_is_some", b(true)))),
			kv("ascii_cow_borrowed", b(true)),
			kv("euro", obj(
				kv("kind", s("twobyte")), kv("len", i(1)),
				kv("first_unit_hex", s("20ac")), kv("as_str_is_some", b(false)))),
			kv("euro_cow", s("\u20ac")),
			kv("latin1", obj(
				kv("kind", s("onebyte")), kv("len", i(2)),
				kv("hex", s("e941")), kv("as_str_is_some", b(false)))),
			kv("empty", obj(
				kv("kind", s("onebyte")), kv("len", i(0)),
				kv("hex", s("")), kv("as_str_is_some", b(true)))),
		),
		obj(
			kv("ascii", obj(
				kv("kind", s(asciiKind)), kv("len", i(asciiLen)),
				kv("hex", s(asciiHex)), kv("as_str_is_some", b(asciiAsStr)))),
			kv("ascii_cow_borrowed", b(asciiCowBorrowed)),
			kv("euro", obj(
				kv("kind", s(euroKind)), kv("len", i(euroLen)),
				kv("first_unit_hex", s(euroFirstUnit)), kv("as_str_is_some", b(euroAsStr)))),
			kv("euro_cow", s(euroCow)),
			kv("latin1", obj(
				kv("kind", s(latin1Kind)), kv("len", i(latin1Len)),
				kv("hex", s(latin1Hex)), kv("as_str_is_some", b(latin1AsStr)))),
			kv("empty", obj(
				kv("kind", s(emptyKind)), kv("len", i(emptyLen)),
				kv("hex", s(emptyHex)), kv("as_str_is_some", b(emptyAsStr)))),
		))
}

// checkExternalStaticAndConst pins static and const external strings:
// predicates, resource data echo, JS visibility, GC survival while held,
// and cross-isolate OneByteConst sharing.
func checkExternalStaticAndConst(t *testing.T) obs {
	t.Helper()
	constData, err := gov8.CreateExternalOneByteConst("konst")
	if err != nil {
		t.Fatalf("CreateExternalOneByteConst: %v", err)
	}

	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}

	var (
		predicates jsonValue
		resStatic  jsonValue
		resConst   jsonValue
		base       jsonValue
		jsEq       string
		held       *gov8.Global
	)
	withContext(t, iso, func(r *runtime) {
		staticStr, err := r.scope.NewExternalOneByteStringStatic([]byte("static_ext"))
		if err != nil {
			t.Fatalf("NewExternalOneByteStringStatic: %v", err)
		}
		k, err := r.scope.NewStringFromOneByteConst(constData)
		if err != nil {
			t.Fatalf("NewStringFromOneByteConst: %v", err)
		}
		twoByte, err := r.scope.NewExternalTwoByteStringStatic([]uint16{0xD83E, 0xDD80, 0x0041})
		if err != nil {
			t.Fatalf("NewExternalTwoByteStringStatic: %v", err)
		}

		echoBuf := make([]uint16, 8)
		poisonU16(echoBuf)
		if _, err := twoByte.WriteTwoByte(0, echoBuf, 0); err != nil {
			t.Fatalf("WriteTwoByte: %v", err)
		}
		echoTail := true
		for _, u := range echoBuf[3:] {
			echoTail = echoTail && u == 0xABAB
		}

		res1Ptr, res1Data, res1OK, err := staticStr.GetExternalOneByteStringResource()
		if err != nil {
			t.Fatalf("GetExternalOneByteStringResource: %v", err)
		}
		_, resKData, resKOK, err := k.GetExternalOneByteStringResource()
		if err != nil {
			t.Fatalf("const resource: %v", err)
		}

		basePtr, baseEnc, baseOK, err := staticStr.GetExternalStringResourceBase()
		if err != nil {
			t.Fatalf("GetExternalStringResourceBase: %v", err)
		}
		enc := "Unknown"
		switch baseEnc {
		case gov8.StringEncodingOneByte:
			enc = "OneByte"
		case gov8.StringEncodingTwoByte:
			enc = "TwoByte"
		}

		predicates = obj(
			kv("static_onebyte", obj(
				kv("is_external", b(mustExternalString(t, staticStr))),
				kv("is_external_onebyte", b(mustExternalOneByte(t, staticStr))),
				kv("is_external_twobyte", b(mustExternalTwoByte(t, staticStr))),
				kv("is_onebyte", b(mustOneByte(t, staticStr))),
				kv("text", s(valueText(t, r, staticStr))),
			)),
			kv("const_onebyte", obj(
				kv("is_external_onebyte", b(mustExternalOneByte(t, k))),
				kv("text", s(valueText(t, r, k))),
				kv("const_as_str", s(constData.Data())),
			)),
			kv("twobyte_static", obj(
				kv("is_external_twobyte", b(mustExternalTwoByte(t, twoByte))),
				kv("is_onebyte", b(mustOneByte(t, twoByte))),
				kv("contains_only_onebyte", b(mustContainsOnlyOneByte(t, twoByte))),
				kv("len", i(int64(mustLength(t, twoByte)))),
				kv("text", s(valueText(t, r, twoByte))),
				kv("units_echo", obj(
					kv("units", unitsJSON(echoBuf, 3)),
					kv("tail_untouched", b(echoTail)),
				)),
			)),
		)
		resStatic = obj(
			kv("resource_is_some", b(res1OK)),
			kv("data", s(string(res1Data))),
			kv("len", i(int64(len(res1Data)))),
		)
		resConst = obj(
			kv("resource_is_some", b(resKOK)),
			kv("resource_data", s(string(resKData))),
		)
		base = obj(
			kv("base_is_some", b(baseOK)),
			kv("base_matches_onebyte_getter", b(baseOK && res1OK && basePtr == res1Ptr)),
			kv("enc", s(enc)),
		)

		global, err := r.ctx.GlobalObject(r.scope)
		if err != nil {
			t.Fatalf("GlobalObject: %v", err)
		}
		if ok, serr := global.SetByName(r.scope, r.ctx, "exts", staticStr); serr != nil || !ok {
			t.Fatalf("SetByName exts: %v, %v", ok, serr)
		}
		jsEq = r.evalText(t, "exts === 'static_ext' ? 'EQ' : 'NEQ'")

		held, err = gov8.NewGlobal(r.scope, staticStr)
		if err != nil {
			t.Fatalf("NewGlobal: %v", err)
		}
	})

	// The external string survives a forced major GC while referenced.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	survived := false
	withContext(t, iso, func(r *runtime) {
		local, err := held.ToLocal(r.scope)
		if err != nil {
			t.Fatalf("ToLocal: %v", err)
		}
		survived = valueText(t, r, local) == "static_ext"
	})
	if err := held.Close(); err != nil {
		t.Errorf("held.Close: %v", err)
	}

	// OneByteConst resources are shareable: the same static creates
	// external strings in a second isolate.
	constShared := false
	iso2, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("second NewIsolate: %v", err)
	}
	withContext(t, iso2, func(r *runtime) {
		k2, err := r.scope.NewStringFromOneByteConst(constData)
		if err != nil {
			t.Fatalf("NewStringFromOneByteConst (isolate 2): %v", err)
		}
		constShared = mustExternalOneByte(t, k2) && valueText(t, r, k2) == "konst"
	})
	if err := iso2.Close(); err != nil {
		t.Errorf("iso2.Close: %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Errorf("iso.Close: %v", err)
	}

	wantPredicates := obj(
		kv("static_onebyte", obj(
			kv("is_external", b(true)),
			kv("is_external_onebyte", b(true)),
			kv("is_external_twobyte", b(false)),
			kv("is_onebyte", b(true)),
			kv("text", s("static_ext")),
		)),
		kv("const_onebyte", obj(
			kv("is_external_onebyte", b(true)),
			kv("text", s("konst")),
			kv("const_as_str", s("konst")),
		)),
		kv("twobyte_static", obj(
			kv("is_external_twobyte", b(true)),
			kv("is_onebyte", b(false)),
			kv("contains_only_onebyte", b(false)),
			kv("len", i(3)),
			kv("text", s("\U0001F980A")),
			kv("units_echo", obj(
				kv("units", arr(i(0xD83E), i(0xDD80), i(0x0041))),
				kv("tail_untouched", b(true)),
			)),
		)),
	)
	wantStaticResource := obj(
		kv("resource_is_some", b(true)),
		kv("data", s("static_ext")),
		kv("len", i(10)),
	)
	wantConstResource := obj(
		kv("resource_is_some", b(true)),
		kv("resource_data", s("konst")),
	)
	wantBase := obj(
		kv("base_is_some", b(true)),
		kv("base_matches_onebyte_getter", b(true)),
		kv("enc", s("OneByte")),
	)
	return wantGot("strings/external_static_and_const",
		obj(
			kv("predicates", wantPredicates),
			kv("static_resource", wantStaticResource),
			kv("const_resource", wantConstResource),
			kv("base", wantBase),
			kv("js_eq", s("EQ")),
			kv("survives_forced_gc_while_held", b(true)),
			kv("const_shared_across_isolates", b(true)),
		),
		obj(
			kv("predicates", predicates),
			kv("static_resource", resStatic),
			kv("const_resource", resConst),
			kv("base", base),
			kv("js_eq", s(jsEq)),
			kv("survives_forced_gc_while_held", b(survived)),
			kv("const_shared_across_isolates", b(constShared)),
		))
}

// checkExternalResourceIdentity pins resource getters across flavors and
// pointer identity/difference (recorded as booleans only).
func checkExternalResourceIdentity(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	s1, err := r.scope.NewExternalOneByteStringStatic([]byte("AAA"))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := r.scope.NewExternalOneByteStringStatic([]byte("BBBB"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := r.scope.NewExternalTwoByteStringStatic([]uint16{0x20AC})
	if err != nil {
		t.Fatal(err)
	}
	plain := mustString(t, r, "plain")

	r1, _, r1OK, err := s1.GetExternalOneByteStringResource()
	if err != nil {
		t.Fatal(err)
	}
	r1Again, _, r1AgainOK, err := s1.GetExternalOneByteStringResource()
	if err != nil {
		t.Fatal(err)
	}
	r2, _, _, err := s2.GetExternalOneByteStringResource()
	if err != nil {
		t.Fatal(err)
	}
	_, _, twoOneByteOK, err := two.GetExternalOneByteStringResource()
	if err != nil {
		t.Fatal(err)
	}
	twoGeneric, twoGenericOK, err := two.GetExternalStringResource()
	if err != nil {
		t.Fatal(err)
	}
	twoBase, _, twoBaseOK, err := two.GetExternalStringResourceBase()
	if err != nil {
		t.Fatal(err)
	}
	oneGeneric, _, err := s1.GetExternalStringResource()
	if err != nil {
		t.Fatal(err)
	}

	plainExternal := mustExternalString(t, plain)
	_, _, plainOneByteOK, _ := plain.GetExternalOneByteStringResource()
	_, plainGenericOK, _ := plain.GetExternalStringResource()
	_, _, plainBaseOK, _ := plain.GetExternalStringResourceBase()
	_ = plainExternal

	return wantGot("strings/external_resource_identity",
		obj(
			kv("s1_is_some", b(true)),
			kv("s1_stable", b(true)),
			kv("distinct_statics_distinct_resources", b(true)),
			kv("twobyte_onebyte_getter_none", b(true)),
			kv("twobyte_generic_is_some", b(true)),
			kv("twobyte_base_is_some", b(true)),
			kv("twobyte_generic_matches_base", b(true)),
			kv("onebyte_generic_is_none", b(true)),
			kv("plain", obj(
				kv("is_external", b(false)),
				kv("onebyte_getter_none", b(true)),
				kv("generic_none", b(true)),
				kv("base_none", b(true)))),
		),
		obj(
			kv("s1_is_some", b(r1OK)),
			kv("s1_stable", b(r1OK && r1AgainOK && r1 == r1Again)),
			kv("distinct_statics_distinct_resources", b(r1 != r2)),
			kv("twobyte_onebyte_getter_none", b(!twoOneByteOK)),
			kv("twobyte_generic_is_some", b(twoGenericOK)),
			kv("twobyte_base_is_some", b(twoBaseOK)),
			kv("twobyte_generic_matches_base", b(twoGenericOK && twoBaseOK && twoGeneric == twoBase)),
			kv("onebyte_generic_is_none", b(oneGeneric == 0)),
			kv("plain", obj(
				kv("is_external", b(plainExternal)),
				kv("onebyte_getter_none", b(!plainOneByteOK)),
				kv("generic_none", b(!plainGenericOK)),
				kv("base_none", b(!plainBaseOK)))),
		))
}

// checkExternalOwned pins owned external strings: content, predicates, and
// survival across a forced GC while referenced (the crate's free functions
// run at finalization; a healthy process is the observable contract).
func checkExternalOwned(t *testing.T) obs {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}

	var (
		held        *gov8.Global
		wasExternal bool
		textBefore  string
	)
	withContext(t, iso, func(r *runtime) {
		s, err := r.scope.NewExternalTwoByteString([]uint16{0x0042, 0x0042, 0x0042, 0x0042, 0x0042})
		if err != nil {
			t.Fatalf("NewExternalTwoByteString: %v", err)
		}
		held, err = gov8.NewGlobal(r.scope, s)
		if err != nil {
			t.Fatal(err)
		}
		wasExternal = mustExternalTwoByte(t, s)
		textBefore = valueText(t, r, s)
	})

	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}

	survived, ownedOneByteOK := false, false
	withContext(t, iso, func(r *runtime) {
		local, err := held.ToLocal(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		survived = valueText(t, r, local) == textBefore
		s2, err := r.scope.NewExternalOneByteString([]byte("owned-1b"))
		if err != nil {
			t.Fatalf("NewExternalOneByteString: %v", err)
		}
		ownedOneByteOK = mustExternalOneByte(t, s2) && valueText(t, r, s2) == "owned-1b"
	})
	if err := held.Close(); err != nil {
		t.Errorf("held.Close: %v", err)
	}
	// Drives the finalization of the released two-byte string's buffer.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Errorf("iso.Close: %v", err)
	}

	return wantGot("strings/external_owned",
		obj(
			kv("owned_twobyte_is_external", b(true)),
			kv("owned_twobyte_survives_gc", b(true)),
			kv("owned_onebyte_ok", b(true)),
		),
		obj(
			kv("owned_twobyte_is_external", b(wasExternal)),
			kv("owned_twobyte_survives_gc", b(survived)),
			kv("owned_onebyte_ok", b(ownedOneByteOK)),
		))
}

// checkExternalDeleterLifetime pins the raw-destructor lifetime: not
// called while alive, exactly once on the first forced major GC after the
// last strong reference drops, with the original pointer and length.
func checkExternalDeleterLifetime(t *testing.T) obs {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() {
		if err := iso.Close(); err != nil {
			t.Errorf("iso.Close: %v", err)
		}
	}()

	var (
		oneCalls, oneLen  atomic.Int64
		onePtr, oneHanded atomic.Int64
		twoCalls, twoLen  atomic.Int64
		twoPtr, twoHanded atomic.Int64
		raw1, raw2        *gov8.Global
		raw1OK, raw2OK    bool
	)
	withContext(t, iso, func(r *runtime) {
		payload := bytes.Repeat([]byte{7}, 9)
		s, handed, err := r.scope.NewExternalOneByteStringRaw(payload, func(data uintptr, length int) {
			oneCalls.Add(1)
			oneLen.Store(int64(length))
			onePtr.Store(int64(data))
		})
		if err != nil {
			t.Fatalf("NewExternalOneByteStringRaw: %v", err)
		}
		oneHanded.Store(int64(handed))
		raw1OK = mustExternalOneByte(t, s) && valueText(t, r, s) == "\a\a\a\a\a\a\a\a\a"
		raw1, err = gov8.NewGlobal(r.scope, s)
		if err != nil {
			t.Fatal(err)
		}
	})
	withContext(t, iso, func(r *runtime) {
		units := []uint16{0x0042, 0x0063}
		s, handed, err := r.scope.NewExternalTwoByteStringRaw(units, func(data uintptr, length int) {
			twoCalls.Add(1)
			twoLen.Store(int64(length))
			twoPtr.Store(int64(data))
		})
		if err != nil {
			t.Fatalf("NewExternalTwoByteStringRaw: %v", err)
		}
		twoHanded.Store(int64(handed))
		raw2OK = mustExternalTwoByte(t, s) && valueText(t, r, s) == "Bc"
		raw2, err = gov8.NewGlobal(r.scope, s)
		if err != nil {
			t.Fatal(err)
		}
	})

	// While the globals hold the strings alive, a forced GC must not fire
	// either deleter.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	notCalledWhileAlive := oneCalls.Load() == 0 && twoCalls.Load() == 0

	if err := raw1.Close(); err != nil {
		t.Errorf("raw1.Close: %v", err)
	}
	if err := raw2.Close(); err != nil {
		t.Errorf("raw2.Close: %v", err)
	}
	// The first forced major GC after the last reference drop finalizes
	// both external strings and runs each deleter exactly once.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	afterOneGC := obj(
		kv("onebyte_calls", i(oneCalls.Load())),
		kv("onebyte_len", i(oneLen.Load())),
		kv("onebyte_ptr_echo", b(onePtr.Load() == oneHanded.Load())),
		kv("twobyte_calls", i(twoCalls.Load())),
		kv("twobyte_len", i(twoLen.Load())),
		kv("twobyte_ptr_echo", b(twoPtr.Load() == twoHanded.Load())),
	)
	// Subsequent GCs are no-ops for the deleters.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	exactlyOnce := oneCalls.Load() == 1 && twoCalls.Load() == 1 &&
		oneLen.Load() == 9 && twoLen.Load() == 2 &&
		onePtr.Load() == oneHanded.Load() && twoPtr.Load() == twoHanded.Load()

	return wantGot("strings/external_deleter_lifetime",
		obj(
			kv("raw_onebyte_ok", b(true)),
			kv("raw_twobyte_ok", b(true)),
			kv("not_called_while_alive", b(true)),
			kv("after_one_gc", obj(
				kv("onebyte_calls", i(1)),
				kv("onebyte_len", i(9)),
				kv("onebyte_ptr_echo", b(true)),
				kv("twobyte_calls", i(1)),
				kv("twobyte_len", i(2)),
				kv("twobyte_ptr_echo", b(true)))),
			kv("exactly_once_across_extra_gcs", b(true)),
		),
		obj(
			kv("raw_onebyte_ok", b(raw1OK)),
			kv("raw_twobyte_ok", b(raw2OK)),
			kv("not_called_while_alive", b(notCalledWhileAlive)),
			kv("after_one_gc", afterOneGC),
			kv("exactly_once_across_extra_gcs", b(exactlyOnce)),
		))
}

// checkLatin1ToUTF8Helper pins the public string::latin1_to_utf8 helper,
// including the ASCII/non-ASCII boundary and every possible Latin-1 byte.
func checkLatin1ToUTF8Helper(t *testing.T) obs {
	t.Helper()
	convert := func(input []byte) ([]byte, int, bool) {
		out := bytes.Repeat([]byte{0xa5}, len(input)*2+3)
		n, err := gov8.Latin1ToUTF8(input, out)
		if err != nil {
			t.Fatalf("Latin1ToUTF8: %v", err)
		}
		tailUntouched := bytes.Equal(out[n:], bytes.Repeat([]byte{0xa5}, len(out)-n))
		return out[:n], n, tailUntouched
	}

	empty, emptyN, _ := convert(nil)
	ascii, asciiN, _ := convert([]byte("01234567"))
	boundaries, boundariesN, _ := convert([]byte{0x00, 0x7f, 0x80, 0xff})
	allInput := make([]byte, 256)
	for index := range allInput {
		allInput[index] = byte(index)
	}
	all, allN, tailUntouched := convert(allInput)

	want := obj(
		kv("empty_written", i(0)), kv("empty_hex", s("")),
		kv("ascii_written", i(8)), kv("ascii_hex", s("3031323334353637")),
		kv("boundaries_written", i(6)), kv("boundaries_hex", s("007fc280c3bf")),
		kv("all_bytes_written", i(384)),
		kv("first_non_ascii_hex", s("c280")), kv("last_hex", s("c3bf")),
		kv("tail_untouched", b(true)),
	)
	got := obj(
		kv("empty_written", i(int64(emptyN))), kv("empty_hex", s(lowerHex(empty))),
		kv("ascii_written", i(int64(asciiN))), kv("ascii_hex", s(lowerHex(ascii))),
		kv("boundaries_written", i(int64(boundariesN))), kv("boundaries_hex", s(lowerHex(boundaries))),
		kv("all_bytes_written", i(int64(allN))),
		kv("first_non_ascii_hex", s(lowerHex(all[128:130]))), kv("last_hex", s(lowerHex(all[len(all)-2:]))),
		kv("tail_untouched", b(tailUntouched)),
	)
	return wantGot("strings/latin1_to_utf8_helper", want, got)
}

// --- bigint ---------------------------------------------------------------------------

// checkBigIntI64U64Views pins BigInt::new_from_i64/new_from_u64 boundaries
// and the u64_value/i64_value truncation semantics.
func checkBigIntI64U64Views(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	i64Of := func(v gov8.Value) (int64, bool) {
		value, lossless, err := v.BigIntInt64()
		if err != nil {
			t.Fatalf("BigIntInt64: %v", err)
		}
		return value, lossless
	}
	u64Of := func(v gov8.Value) (uint64, bool) {
		value, lossless, err := v.BigIntUint64()
		if err != nil {
			t.Fatalf("BigIntUint64: %v", err)
		}
		return value, lossless
	}

	zero := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromInt64(0) })
	negOne := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromInt64(-1) })
	imin := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromInt64(-9223372036854775808) })
	imax := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromInt64(9223372036854775807) })
	neg42 := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromInt64(-42) })
	umax := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromUint64(^uint64(0)) })
	uzero := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromUint64(0) })
	two64 := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) {
		return s.BigIntFromWords(r.ctx, false, []uint64{0, 1}, nil)
	})

	zeroI, zeroL := i64Of(zero)
	negOneI, negOneL := i64Of(negOne)
	negOneU, negOneUL := u64Of(negOne)
	iminI, iminL := i64Of(imin)
	imaxI, imaxL := i64Of(imax)
	neg42U, neg42UL := u64Of(neg42)
	umaxI, umaxL := i64Of(umax)
	umaxU, umaxUL := u64Of(umax)
	uzeroI, uzeroL := i64Of(uzero)
	two64I, two64L := i64Of(two64)
	two64U, two64UL := u64Of(two64)
	zeroWC := mustWordCount(t, zero)
	iminWC := mustWordCount(t, imin)
	two64WC := mustWordCount(t, two64)
	two64Text := valueText(t, r, two64)

	u64Enc := func(value uint64, lossless bool) jsonValue {
		return obj(kv("value", u64JSON(value)), kv("lossless", b(lossless)))
	}

	return wantGot("bigint/i64_u64_views",
		obj(
			kv("zero_i64", obj(kv("value", i(0)), kv("lossless", b(true)))),
			kv("zero_word_count", i(0)),
			kv("neg_one_i64", obj(kv("value", i(-1)), kv("lossless", b(true)))),
			kv("neg_one_u64", obj(kv("value", obj(kv("lo", i(4294967295)), kv("hi", i(4294967295)))), kv("lossless", b(false)))),
			kv("i64_min_i64", obj(kv("value", i(-9223372036854775808)), kv("lossless", b(true)))),
			kv("i64_min_word_count", i(1)),
			kv("i64_max_i64", obj(kv("value", i(9223372036854775807)), kv("lossless", b(true)))),
			kv("neg42_u64", obj(kv("value", obj(kv("lo", i(4294967254)), kv("hi", i(4294967295)))), kv("lossless", b(false)))),
			kv("u64_max_i64", obj(kv("value", i(-1)), kv("lossless", b(false)))),
			kv("u64_max_u64", obj(kv("value", obj(kv("lo", i(4294967295)), kv("hi", i(4294967295)))), kv("lossless", b(true)))),
			kv("u64_zero_i64", obj(kv("value", i(0)), kv("lossless", b(true)))),
			kv("two64_text", s("18446744073709551616")),
			kv("two64_i64", obj(kv("value", i(0)), kv("lossless", b(false)))),
			kv("two64_u64", obj(kv("value", i(0)), kv("lossless", b(false)))),
			kv("two64_word_count", i(2)),
		),
		obj(
			kv("zero_i64", obj(kv("value", i(zeroI)), kv("lossless", b(zeroL)))),
			kv("zero_word_count", i(int64(zeroWC))),
			kv("neg_one_i64", obj(kv("value", i(negOneI)), kv("lossless", b(negOneL)))),
			kv("neg_one_u64", u64Enc(negOneU, negOneUL)),
			kv("i64_min_i64", obj(kv("value", i(iminI)), kv("lossless", b(iminL)))),
			kv("i64_min_word_count", i(int64(iminWC))),
			kv("i64_max_i64", obj(kv("value", i(imaxI)), kv("lossless", b(imaxL)))),
			kv("neg42_u64", u64Enc(neg42U, neg42UL)),
			kv("u64_max_i64", obj(kv("value", i(umaxI)), kv("lossless", b(umaxL)))),
			kv("u64_max_u64", u64Enc(umaxU, umaxUL)),
			kv("u64_zero_i64", obj(kv("value", i(uzeroI)), kv("lossless", b(uzeroL)))),
			kv("two64_text", s(two64Text)),
			kv("two64_i64", obj(kv("value", i(two64I)), kv("lossless", b(two64L)))),
			kv("two64_u64", obj(kv("value", u64JSON(two64U)), kv("lossless", b(two64UL)))),
			kv("two64_word_count", i(int64(two64WC))),
		))
}

// checkBigIntWordsConstruction pins BigInt::new_from_words: zero words
// (both sign bits), single-word, multi-word values, sign semantics, and
// word_count.
func checkBigIntWordsConstruction(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	wordBigInt := func(sign bool, words []uint64) gov8.Value {
		v, err := r.scope.BigIntFromWords(r.ctx, sign, words, nil)
		if err != nil {
			t.Fatalf("BigIntFromWords: %v", err)
		}
		return v
	}

	zero := wordBigInt(false, nil)
	zeroNegSign := wordBigInt(true, nil)
	one := wordBigInt(false, []uint64{1})
	umax := wordBigInt(false, []uint64{^uint64(0)})
	two65Minus1 := wordBigInt(false, []uint64{^uint64(0), 1})
	neg3 := wordBigInt(true, []uint64{3})
	threeWords := wordBigInt(false, []uint64{1, 1, 1})

	return wantGot("bigint/words_construction",
		obj(
			kv("zero_words", obj(kv("text", s("0")), kv("word_count", i(0)))),
			kv("zero_words_negsign_text", s("0")),
			kv("one_word", obj(kv("text", s("1")), kv("word_count", i(1)))),
			kv("u64_max_word", obj(kv("text", s("18446744073709551615")), kv("word_count", i(1)))),
			kv("words_max_plus_one", obj(kv("text", s("36893488147419103231")), kv("word_count", i(2)))),
			kv("negative_words", obj(kv("text", s("-3")), kv("word_count", i(1)))),
			kv("three_words", obj(kv("text", s("340282366920938463481821351505477763073")), kv("word_count", i(3)))),
		),
		obj(
			kv("zero_words", obj(kv("text", s(valueText(t, r, zero))), kv("word_count", i(int64(mustWordCount(t, zero)))))),
			kv("zero_words_negsign_text", s(valueText(t, r, zeroNegSign))),
			kv("one_word", obj(kv("text", s(valueText(t, r, one))), kv("word_count", i(int64(mustWordCount(t, one)))))),
			kv("u64_max_word", obj(kv("text", s(valueText(t, r, umax))), kv("word_count", i(int64(mustWordCount(t, umax)))))),
			kv("words_max_plus_one", obj(kv("text", s(valueText(t, r, two65Minus1))), kv("word_count", i(int64(mustWordCount(t, two65Minus1)))))),
			kv("negative_words", obj(kv("text", s(valueText(t, r, neg3))), kv("word_count", i(int64(mustWordCount(t, neg3)))))),
			kv("three_words", obj(kv("text", s(valueText(t, r, threeWords))), kv("word_count", i(int64(mustWordCount(t, threeWords)))))),
		))
}

// checkBigIntWordsExtraction pins word_count and to_words_array: exact,
// oversized, and truncated buffers; zero BigInt leaves the buffer
// untouched; negative values report the sign bit and absolute-value words.
func checkBigIntWordsExtraction(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	toWords := func(v gov8.Value, size int, fill uint64) (bool, []uint64, []uint64) {
		t.Helper()
		buf := make([]uint64, size)
		for j := range buf {
			buf[j] = fill
		}
		sign, words, err := v.BigIntToWords(buf)
		if err != nil {
			t.Fatalf("BigIntToWords: %v", err)
		}
		return sign, words, buf
	}

	zero := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromInt64(0) })
	zeroSign, zeroWords, zeroBuf := toWords(zero, 4, 0xDEAD)
	zeroUntouched := zeroBuf[0] == 0xDEAD && zeroBuf[3] == 0xDEAD

	value := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) {
		return s.BigIntFromWords(r.ctx, false, []uint64{1, 1}, nil)
	})
	_, truncWords, _ := toWords(value, 1, 0)
	exactSign, exactWords, _ := toWords(value, 2, 0)
	overSign, overWords, overBuf := toWords(value, 5, 0xEE)
	overTailUntouched := overBuf[2] == 0xEE && overBuf[4] == 0xEE

	negative := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) {
		return s.BigIntFromWords(r.ctx, true, []uint64{3}, nil)
	})
	negSign, negWords, _ := toWords(negative, 4, 0)

	return wantGot("bigint/words_extraction",
		obj(
			kv("zero", obj(
				kv("sign", b(false)), kv("words", arr()), kv("buffer_untouched", b(true)))),
			kv("truncated_to_one_word", obj(
				kv("sign", b(false)), kv("words", arr(i(1))))),
			kv("exact", obj(
				kv("sign", b(false)), kv("words", arr(i(1), i(1))))),
			kv("oversized", obj(
				kv("sign", b(false)), kv("words", arr(i(1), i(1))), kv("tail_untouched", b(true)))),
			kv("negative", obj(
				kv("sign", b(true)), kv("words", arr(i(3))))),
		),
		obj(
			kv("zero", obj(
				kv("sign", b(zeroSign)), kv("words", wordsJSON(zeroWords)), kv("buffer_untouched", b(zeroUntouched)))),
			kv("truncated_to_one_word", obj(
				kv("sign", b(false)), kv("words", wordsJSON(truncWords)))),
			kv("exact", obj(
				kv("sign", b(exactSign)), kv("words", wordsJSON(exactWords)))),
			kv("oversized", obj(
				kv("sign", b(overSign)), kv("words", wordsJSON(overWords)), kv("tail_untouched", b(overTailUntouched)))),
			kv("negative", obj(
				kv("sign", b(negSign)), kv("words", wordsJSON(negWords)))),
		))
}

// checkBigIntRoundtripAndJS pins word roundtrip identity and JS interop:
// words -> BigInt -> words, two's-complement i64 truncation, JS-created
// BigInts observed natively, and native BigInts observed from JS.
func checkBigIntRoundtripAndJS(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	source := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) {
		return s.BigIntFromWords(r.ctx, false, []uint64{^uint64(0), 42}, nil)
	})
	echoBuf := make([]uint64, 3)
	echoSign, echoWords, err := source.BigIntToWords(echoBuf)
	if err != nil {
		t.Fatal(err)
	}
	echoMatches := len(echoWords) == 2 && echoWords[0] == ^uint64(0) && echoWords[1] == 42 && !echoSign
	echoWC := mustWordCount(t, source)

	signed := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) {
		return s.BigIntFromWords(r.ctx, true, []uint64{^uint64(0), 42}, nil)
	})
	signedI64, signedLossless, err := signed.BigIntInt64()
	if err != nil {
		t.Fatal(err)
	}

	jsBigintRaw, ok := r.eval(t, "2n ** 64n + 1n")
	if !ok {
		t.Fatal("eval 2n**64n+1n failed")
	}
	jsWords := make([]uint64, 4)
	jsSign, jsWordsOut, err := jsBigintRaw.BigIntToWords(jsWords)
	if err != nil {
		t.Fatal(err)
	}
	jsI64, jsI64Lossless, err := jsBigintRaw.BigIntInt64()
	if err != nil {
		t.Fatal(err)
	}

	jsZero, ok := r.eval(t, "0n")
	if !ok {
		t.Fatal("eval 0n failed")
	}
	jsZeroWC := mustWordCount(t, jsZero)

	native := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) {
		return s.BigIntFromWords(r.ctx, false, []uint64{5, 1}, nil)
	})
	nativeText := valueText(t, r, native)
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if setOK, serr := global.SetByName(r.scope, r.ctx, "nat", native); serr != nil || !setOK {
		t.Fatalf("SetByName nat: %v, %v", setOK, serr)
	}
	jsTypeof := r.evalText(t, "typeof nat")
	jsSum := r.evalText(t, "(nat + 1n).toString()")

	return wantGot("bigint/roundtrip_and_js",
		obj(
			kv("words_roundtrip", obj(
				kv("matches", b(true)),
				kv("word_count", i(2)),
				kv("echoed", arr(i(-1), i(42))))),
			kv("signed_words_i64_truncation", obj(
				kv("i64", i(1)), kv("i64_lossless", b(false)))),
			kv("js_bigint", obj(
				kv("sign", b(false)),
				kv("words", arr(i(1), i(1))),
				kv("i64", i(1)),
				kv("i64_lossless", b(false)))),
			kv("js_zero_word_count", i(0)),
			kv("native_to_js_text", s("18446744073709551621")),
			kv("js_typeof", s("bigint")),
			kv("js_sum", s("18446744073709551622")),
		),
		obj(
			kv("words_roundtrip", obj(
				kv("matches", b(echoMatches)),
				kv("word_count", i(int64(echoWC))),
				kv("echoed", wordsJSON(echoWords)))),
			kv("signed_words_i64_truncation", obj(
				kv("i64", i(signedI64)), kv("i64_lossless", b(signedLossless)))),
			kv("js_bigint", obj(
				kv("sign", b(jsSign)),
				kv("words", wordsJSON(jsWordsOut)),
				kv("i64", i(jsI64)),
				kv("i64_lossless", b(jsI64Lossless)))),
			kv("js_zero_word_count", i(int64(jsZeroWC))),
			kv("native_to_js_text", s(nativeText)),
			kv("js_typeof", s(jsTypeof)),
			kv("js_sum", s(jsSum)),
		))
}

// checkBigIntMaxWordsRangeError pins the new_from_words over-limit
// failure: more than i::BigInt::kMaxLength words fails with a pending JS
// RangeError, observed and reset by a TryCatch, isolate fully usable.
func checkBigIntMaxWordsRangeError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer closeRuntime(t, r)

	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() {
		if err := tc.Close(); err != nil {
			t.Errorf("tc.Close: %v", err)
		}
	}()

	// One word beyond kMaxLength: 16777216 words (128 MiB transient).
	const overLimitWords = 16_777_216
	overWords := make([]uint64, overLimitWords)
	for j := range overWords {
		overWords[j] = 1
	}
	_, overErr := r.scope.BigIntFromWords(r.ctx, false, overWords, tc)
	returnsNone := overErr != nil
	caught, _ := tc.HasCaught()
	message, _ := tc.MessageText(r.scope, r.ctx)
	exception, _ := tc.ExceptionText(r.scope, r.ctx)
	hasTerminated, _ := tc.HasTerminated()
	if err := tc.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	usable := r.evalText(t, "1 + 1")
	big := mustBigInt(t, r, func(s *gov8.Scope) (gov8.Value, error) { return s.BigIntFromInt64(7) })
	bigI, bigLossless, _ := big.BigIntInt64()
	bigintsStillUsable := bigI == 7 && bigLossless

	return wantGot("bigint/max_words_range_error",
		obj(
			kv("returns_none", b(true)),
			kv("caught", b(true)),
			kv("has_terminated", b(false)),
			kv("message", s("Uncaught RangeError: Maximum BigInt size exceeded")),
			kv("exception", s("RangeError: Maximum BigInt size exceeded")),
			kv("usable_after_reset", s("2")),
			kv("bigints_still_usable", b(true)),
		),
		obj(
			kv("returns_none", b(returnsNone)),
			kv("caught", b(caught)),
			kv("has_terminated", b(hasTerminated)),
			kv("message", s(message)),
			kv("exception", s(exception)),
			kv("usable_after_reset", s(usable)),
			kv("bigints_still_usable", b(bigintsStillUsable)),
		))
}

// --- registry ---------------------------------------------------------------------------

// checkFn is one check.
type checkFn func(t *testing.T) obs

// allChecks is the fixed oracle order (the fixture follows this order).
func allChecks() []checkFn {
	return []checkFn{
		checkMaxLengthAndEmpty,
		checkCreationTypes,
		checkConcatSemantics,
		checkWriteTwoByteViews,
		checkWriteOneByteViews,
		checkWriteUTF8Views,
		checkValueViewFlavors,
		checkExternalStaticAndConst,
		checkExternalResourceIdentity,
		checkExternalOwned,
		checkExternalDeleterLifetime,
		checkLatin1ToUTF8Helper,
		checkBigIntI64U64Views,
		checkBigIntWordsConstruction,
		checkBigIntWordsExtraction,
		checkBigIntRoundtripAndJS,
		checkBigIntMaxWordsRangeError,
	}
}

// --- small assertion helpers --------------------------------------------------------

func mustString(t *testing.T, r *runtime, text string) gov8.Value {
	t.Helper()
	v, err := r.scope.NewString(text)
	if err != nil {
		t.Fatalf("NewString(%q): %v", text, err)
	}
	return v
}

func mustBigInt(t *testing.T, r *runtime, fn func(*gov8.Scope) (gov8.Value, error)) gov8.Value {
	t.Helper()
	v, err := fn(r.scope)
	if err != nil {
		t.Fatalf("BigInt constructor: %v", err)
	}
	return v
}

func mustLength(t *testing.T, v gov8.Value) int {
	t.Helper()
	n, err := v.Length()
	if err != nil {
		t.Fatalf("Length: %v", err)
	}
	return n
}

func mustUtf8Length(t *testing.T, v gov8.Value) int {
	t.Helper()
	n, err := v.Utf8Length()
	if err != nil {
		t.Fatalf("Utf8Length: %v", err)
	}
	return n
}

func mustOneByte(t *testing.T, v gov8.Value) bool {
	t.Helper()
	ok, err := v.IsOneByte()
	if err != nil {
		t.Fatalf("IsOneByte: %v", err)
	}
	return ok
}

func mustContainsOnlyOneByte(t *testing.T, v gov8.Value) bool {
	t.Helper()
	ok, err := v.ContainsOnlyOneByte()
	if err != nil {
		t.Fatalf("ContainsOnlyOneByte: %v", err)
	}
	return ok
}

func mustExternalString(t *testing.T, v gov8.Value) bool {
	t.Helper()
	ok, err := v.IsExternalString()
	if err != nil {
		t.Fatalf("IsExternalString: %v", err)
	}
	return ok
}

func mustExternalOneByte(t *testing.T, v gov8.Value) bool {
	t.Helper()
	ok, err := v.IsExternalOneByte()
	if err != nil {
		t.Fatalf("IsExternalOneByte: %v", err)
	}
	return ok
}

func mustExternalTwoByte(t *testing.T, v gov8.Value) bool {
	t.Helper()
	ok, err := v.IsExternalTwoByte()
	if err != nil {
		t.Fatalf("IsExternalTwoByte: %v", err)
	}
	return ok
}

func mustWordCount(t *testing.T, v gov8.Value) int {
	t.Helper()
	n, err := v.BigIntWordCount()
	if err != nil {
		t.Fatalf("BigIntWordCount: %v", err)
	}
	return n
}

// isASCII reports whether every byte is ASCII (the as_str rule).
func isASCII(data []byte) bool {
	for _, by := range data {
		if by > 0x7F {
			return false
		}
	}
	return true
}

// describeText renders a string value lossily through the engine (the
// euro_cow analog of the view-based to_cow_lossy text).
func describeText(t *testing.T, r *runtime, v gov8.Value) string {
	t.Helper()
	return valueText(t, r, v)
}
