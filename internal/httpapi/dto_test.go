package httpapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDTOsMatchLegacyFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		target  any
	}{
		{name: "server info", fixture: "responses/server-info.json", target: &ServerInfoResponse{}},
		{name: "image metadata", fixture: "responses/image-metadata.json", target: &ImageMetadata{}},
		{name: "image list", fixture: "responses/images-list.json", target: &[]ImageMetadata{}},
		{name: "upload success", fixture: "responses/image-upload-success.json", target: &UploadResponse{}},
		{name: "upload duplicate", fixture: "responses/image-upload-duplicate.json", target: &UploadResponse{}},
		{name: "upload multiple", fixture: "responses/image-upload-multiple-mixed.json", target: &[]UploadResponse{}},
		{name: "HFS roots", fixture: "responses/hfs-roots.json", target: &[]HFSEntry{}},
		{name: "HFS directory", fixture: "responses/hfs-directory.json", target: &[]HFSEntry{}},
		{name: "no error", fixture: "responses/error-none.json", target: &UploadResponse{}},
		{name: "file exists", fixture: "responses/error-file-already-exists.json", target: &UploadResponse{}},
		{name: "folder missing", fixture: "responses/error-folder-not-found.json", target: &UploadResponse{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := readRelativeFixture(t, test.fixture)
			decoder := json.NewDecoder(bytes.NewReader(fixture))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(test.target); err != nil {
				t.Fatalf("decode fixture into DTO: %v", err)
			}

			encoded, err := json.Marshal(test.target)
			if err != nil {
				t.Fatalf("encode DTO: %v", err)
			}
			assertEquivalentJSON(t, encoded, fixture)
		})
	}
}

func TestTimestampUsesLegacyUTCMilliseconds(t *testing.T) {
	value := NewTimestamp(time.Date(2026, time.September, 4, 9, 2, 3, 456789000, time.FixedZone("UTC+8", 8*60*60)))
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal timestamp: %v", err)
	}
	if got, want := string(encoded), `"2026-09-04T01:02:03.456Z"`; got != want {
		t.Fatalf("timestamp = %s, want %s", got, want)
	}

	var decoded Timestamp
	if err := json.Unmarshal([]byte(`"2026-09-04T09:02:03.004+08:00"`), &decoded); err != nil {
		t.Fatalf("unmarshal timestamp: %v", err)
	}
	if got, want := decoded.Time, time.Date(2026, time.September, 4, 1, 2, 3, 4_000_000, time.UTC); !got.Equal(want) {
		t.Fatalf("decoded timestamp = %s, want %s", got, want)
	}
}

func TestTimestampRejectsInvalidValues(t *testing.T) {
	tests := []string{`null`, `123`, `"not-a-timestamp"`}
	for _, test := range tests {
		t.Run(test, func(t *testing.T) {
			var timestamp Timestamp
			if err := json.Unmarshal([]byte(test), &timestamp); err == nil {
				t.Fatalf("unmarshal %s succeeded, want error", test)
			}
		})
	}

	if _, err := json.Marshal(Timestamp{}); err == nil {
		t.Fatal("marshal zero timestamp succeeded, want error")
	}
}

func TestOptionalDTOFieldsAreOmitted(t *testing.T) {
	encoded, err := json.Marshal(UploadResponse{ErrorCode: ErrorFileTooLarge})
	if err != nil {
		t.Fatalf("marshal upload response: %v", err)
	}
	if got, want := string(encoded), `{"errorCode":12}`; got != want {
		t.Fatalf("upload response = %s, want %s", got, want)
	}

	encoded, err = json.Marshal(HFSEntry{Name: "empty", Link: "/hfs/empty"})
	if err != nil {
		t.Fatalf("marshal HFS entry: %v", err)
	}
	if got, want := string(encoded), `{"name":"empty","link":"/hfs/empty"}`; got != want {
		t.Fatalf("HFS entry = %s, want %s", got, want)
	}
}

func TestErrorCodeValues(t *testing.T) {
	want := []ErrorCode{0, 1, 10, 11, 12, 13}
	got := []ErrorCode{
		ErrorNone,
		ErrorUnexpected,
		ErrorInvalidFileType,
		ErrorFileAlreadyExists,
		ErrorFileTooLarge,
		ErrorFolderNotFound,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("error codes = %v, want %v", got, want)
	}
}

func assertEquivalentJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode generated JSON: %v", err)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode fixture JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("generated JSON = %#v, want %#v", gotValue, wantValue)
	}
}
