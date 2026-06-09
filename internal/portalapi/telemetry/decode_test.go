package telemetry

import (
	"reflect"
	"strings"
	"testing"
)

// TestDecodeEvent_ValidInstall proves a schema-valid install event decodes into
// the closed Event shape with the exact field values.
func TestDecodeEvent_ValidInstall(t *testing.T) {
	body := `{
		"event":"install","kind":"skill","namespace":"forge","name":"design-system",
		"version":"1.2.0","content_hash":"` + strings.Repeat("a", 64) + `",
		"scope":"project","registry":"forge-development-hub",
		"os":"darwin","locale":"en","install_id":"` + strings.Repeat("b", 64) + `",
		"timestamp":"2026-06-08T12:00:00Z"
	}`
	ev, err := DecodeEvent(strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Event != "install" || ev.Kind != "skill" || ev.Name != "design-system" {
		t.Fatalf("unexpected event fields: %+v", ev)
	}
	if ev.OS != "darwin" || ev.Locale != "en" || ev.Scope != "project" {
		t.Fatalf("unexpected coarse fields: %+v", ev)
	}
	if ev.Timestamp.IsZero() {
		t.Fatal("timestamp not parsed")
	}
}

// TestDecodeEvent_RejectsUnknownField is the core no-PII enforcement: a stray
// identity-ish field (hostname) makes strict decode fail → the handler returns
// 400 invalid_event and nothing is stored. (§11.3 ingest-rejects-unknown-fields)
func TestDecodeEvent_RejectsUnknownField(t *testing.T) {
	for _, pii := range []string{"hostname", "username", "email", "ip", "path", "user"} {
		body := `{"event":"install","` + pii + `":"x"}`
		if _, err := DecodeEvent(strings.NewReader(body)); err == nil {
			t.Fatalf("expected strict decode to reject unknown field %q", pii)
		}
	}
}

// TestDecodeEvent_RejectsBadEnum proves out-of-enum coarse values are refused.
func TestDecodeEvent_RejectsBadEnum(t *testing.T) {
	cases := []string{
		`{"event":"install","os":"freebsd"}`,           // os not in {darwin,linux,windows}
		`{"event":"install","locale":"fr"}`,            // locale not in {es,en}
		`{"event":"teleport"}`,                         // unknown event type
		`{"event":""}`,                                 // empty event type
		`{"event":"install","timestamp":"not-a-time"}`, // bad RFC3339
	}
	for _, body := range cases {
		if _, err := DecodeEvent(strings.NewReader(body)); err == nil {
			t.Fatalf("expected decode error for body %q", body)
		}
	}
}

// TestDecodeEvent_RejectsTrailingData guards against smuggling a second JSON
// value after the event.
func TestDecodeEvent_RejectsTrailingData(t *testing.T) {
	if _, err := DecodeEvent(strings.NewReader(`{"event":"resolve"}{"event":"install"}`)); err == nil {
		t.Fatal("expected error on trailing data")
	}
}

// TestEventStruct_HasNoIdentityField is a structural guardrail (§11.3 no-PII
// schema): the Event type must carry no field whose name suggests identity/PII.
// This catches an accidental future addition at compile/test time.
func TestEventStruct_HasNoIdentityField(t *testing.T) {
	banned := map[string]bool{
		"User": true, "Username": true, "Email": true, "Host": true,
		"Hostname": true, "IP": true, "IPAddress": true, "Path": true,
		"FilePath": true, "Subject": true, "Sub": true, "Account": true,
	}
	tp := reflect.TypeOf(Event{})
	for i := 0; i < tp.NumField(); i++ {
		if banned[tp.Field(i).Name] {
			t.Fatalf("Event must not carry identity/PII field %q", tp.Field(i).Name)
		}
	}
}
