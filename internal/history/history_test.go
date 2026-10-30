package history

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMissingAndCumulativeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history", "operations.jsonl")
	if entries, err := Load(path); err != nil || len(entries) != 0 {
		t.Fatalf("missing history = %#v, %v", entries, err)
	}
	first := sampleEntry(0)
	first.Message = "已暂停 ✅\n用户应用：测试"
	if err := Append(path, first); err != nil {
		t.Fatal(err)
	}
	before := readHistoryBytes(t, path)
	second := sampleEntry(1)
	second.OK, second.Message = false, "Failure [POLICY_REJECTED]"
	if err := Append(path, second); err != nil {
		t.Fatal(err)
	}
	after := readHistoryBytes(t, path)
	if !bytes.HasPrefix(after, before) || bytes.Count(after, []byte{'\n'}) != 2 {
		t.Fatal("append modified existing bytes or split an embedded newline")
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, []Entry{first, second}) {
		t.Fatalf("round trip = %#v, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		for target, permission := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != permission {
				t.Fatalf("%s permissions = %v, want %v", target, info.Mode().Perm(), permission)
			}
		}
	}
}

func TestMalformedAndTornTailRecoveryPreservesBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.jsonl")
	first, second, third := sampleEntry(0), sampleEntry(1), sampleEntry(2)
	data := append(encodeEntry(t, first), '\n')
	data = append(data, []byte("malformed middle line\n")...)
	data = append(data, encodeEntry(t, second)...)
	data = append(data, []byte("\n{\"message\":\"torn")...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if !reflect.DeepEqual(got, []Entry{first, second}) || err == nil || !strings.Contains(err.Error(), "2 malformed") || !strings.Contains(err.Error(), "[2 4]") {
		t.Fatalf("recovery = %#v, %v", got, err)
	}
	if !bytes.Equal(readHistoryBytes(t, path), data) {
		t.Fatal("Load changed damaged history")
	}
	if err := Append(path, third); err != nil {
		t.Fatal(err)
	}
	want := append(bytes.Clone(data), '\n')
	want = append(want, encodeEntry(t, third)...)
	want = append(want, '\n')
	if !bytes.Equal(readHistoryBytes(t, path), want) {
		t.Fatal("append did not preserve and separate the torn tail")
	}
	got, err = Load(path)
	if !reflect.DeepEqual(got, []Entry{first, second, third}) || err == nil || !strings.Contains(err.Error(), "recovered 3 valid") {
		t.Fatalf("recovery after append = %#v, %v", got, err)
	}
}

func TestCompleteFinalRecordWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.jsonl")
	first, second := sampleEntry(0), sampleEntry(1)
	if err := os.WriteFile(path, encodeEntry(t, first), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err != nil || !reflect.DeepEqual(got, []Entry{first}) {
		t.Fatalf("complete final record = %#v, %v", got, err)
	}
	if err := Append(path, second); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err != nil || !reflect.DeepEqual(got, []Entry{first, second}) {
		t.Fatalf("append after complete final record = %#v, %v", got, err)
	}
}

func TestLoadHasNoScannerLineLimitAndBoundsWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.jsonl")
	entry := sampleEntry(0)
	entry.Message = strings.Repeat("记录", 100_000)
	data := []byte(strings.Repeat("bad record\n", 100))
	data = append(data, encodeEntry(t, entry)...)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if !reflect.DeepEqual(got, []Entry{entry}) || err == nil || !strings.Contains(err.Error(), "100 malformed") || !strings.Contains(err.Error(), "[1 2 3 4 5 6 7 8]") {
		t.Fatalf("large record recovery failed: count=%d error=%v", len(got), err)
	}
	if len(err.Error()) > 300 {
		t.Fatal("malformed record warning grew without bound")
	}
}

func TestLoadChronologicalOrderIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.jsonl")
	later, earlier, tied := sampleEntry(2), sampleEntry(0), sampleEntry(2)
	later.Message, tied.Message = "first at this time", "second at this time"
	for _, entry := range []Entry{later, earlier, tied} {
		if err := Append(path, entry); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, []Entry{earlier, later, tied}) {
		t.Fatalf("chronological order = %#v, %v", got, err)
	}
}

func TestHistoryIOErrors(t *testing.T) {
	directory := t.TempDir()
	if err := Append(directory, sampleEntry(0)); err == nil {
		t.Fatal("append accepted a directory")
	}
	if _, err := Load(directory); err == nil {
		t.Fatal("load accepted a directory")
	}
	file := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(file, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(filepath.Join(file, "history.jsonl"), sampleEntry(0)); err == nil {
		t.Fatal("append accepted a non-directory parent")
	}
	if string(readHistoryBytes(t, file)) != "unchanged" {
		t.Fatal("failed append modified an existing file")
	}
}

func sampleEntry(minute int) Entry {
	return Entry{Time: time.Date(2026, 10, 30, 12, minute, 0, 0, time.UTC), Serial: "device", UserID: 10, Action: "suspend", Package: "com.example.app", Message: "Success", OK: true}
}

func encodeEntry(t *testing.T, entry Entry) []byte {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readHistoryBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
