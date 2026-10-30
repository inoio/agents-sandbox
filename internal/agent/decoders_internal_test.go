package agent

import (
	"slices"
	"testing"
)

func TestParseOpencodeV1Event(t *testing.T) {
	sessionDiff := `{"directory":"/workspace","project":"cb78d2f0","payload":{"id":"evt_a","type":"session.diff","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v","diff":[]}}}`
	sessionIdle := `{"payload":{"id":"evt_b","type":"session.idle","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v"}}}`
	serverConnected := `{"payload":{"id":"evt_c","type":"server.connected","properties":{}}}`
	childCreated := `{"payload":{"type":"session.created","properties":{"sessionID":"ses_child","info":{"id":"ses_child","parentID":"ses_parent"}}}}`
	topLevelCreated := `{"payload":{"type":"session.created","properties":{"sessionID":"ses_root","info":{"id":"ses_root"}}}}`
	permissionAsked := `{"payload":{"id":"evt_p","type":"permission.asked","properties":{"sessionID":"ses_1","id":"per_1"}}}`

	cases := []struct {
		name string
		in   string
		want StreamEvent
		ok   bool
	}{
		{
			"v1 diff",
			sessionDiff,
			StreamEvent{Type: "session.diff", SessionID: "ses_f987482bcffeiws2F2jQHsI18v", EventID: "evt_a"},
			true,
		},
		{
			"v1 idle",
			sessionIdle,
			StreamEvent{Type: "session.idle", SessionID: "ses_f987482bcffeiws2F2jQHsI18v", EventID: "evt_b"},
			true,
		},
		{
			"v1 permission carries envelope id",
			permissionAsked,
			StreamEvent{Type: "permission.asked", SessionID: "ses_1", EventID: "evt_p"},
			true,
		},
		{"no session id", serverConnected, StreamEvent{Type: "server.connected", EventID: "evt_c"}, true},
		{
			"child parent",
			childCreated,
			StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: "ses_parent"},
			true,
		},
		{"top-level parent empty", topLevelCreated, StreamEvent{Type: "session.created", SessionID: "ses_root"}, true},
		{"undecodable", `not json`, StreamEvent{}, false},
		{"missing type", `{"payload":{"properties":{"sessionID":"ses_1"}}}`, StreamEvent{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseOpencodeV1Event([]byte(tc.in))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseOpencodeV2Event(t *testing.T) {
	childCreated := `{"id":"evt_1","created":1,"type":"session.created","data":{"sessionID":"ses_child","parentID":"ses_root","title":"probe"}}`
	topLevelCreated := `{"type":"session.created","data":{"sessionID":"ses_root"}}`
	execStarted := `{"type":"session.execution.started","data":{"sessionID":"ses_1"}}`
	execSucceeded := `{"type":"session.execution.succeeded","data":{"sessionID":"ses_1"}}`
	formCreated := `{"id":"evt_3","type":"form.created","data":{"form":{"id":"frm_1","sessionID":"ses_1","title":"q"}}}`
	permissionAsked := `{"id":"evt_2","type":"permission.asked","data":{"sessionID":"ses_1","id":"per_1"}}`
	connected := `{"type":"server.connected","data":{}}`
	infoShape := `{"type":"session.updated","data":{"sessionID":"ses_9","info":{"parentID":"ses_root"}}}`

	cases := []struct {
		name string
		in   string
		want StreamEvent
		ok   bool
	}{
		{
			"child",
			childCreated,
			StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: "ses_root", EventID: "evt_1"},
			true,
		},
		{"top-level", topLevelCreated, StreamEvent{Type: "session.created", SessionID: "ses_root"}, true},
		{"exec started", execStarted, StreamEvent{Type: "session.execution.started", SessionID: "ses_1"}, true},
		{"exec succeeded", execSucceeded, StreamEvent{Type: "session.execution.succeeded", SessionID: "ses_1"}, true},
		{
			"form nests session id",
			formCreated,
			StreamEvent{Type: "form.created", SessionID: "ses_1", EventID: "evt_3"},
			true,
		},
		{
			"permission carries envelope id",
			permissionAsked,
			StreamEvent{Type: "permission.asked", SessionID: "ses_1", EventID: "evt_2"},
			true,
		},
		{"no session id", connected, StreamEvent{Type: "server.connected"}, true},
		{
			"info parent fallback",
			infoShape,
			StreamEvent{Type: "session.updated", SessionID: "ses_9", ParentID: "ses_root"},
			true,
		},
		{"undecodable", `not json`, StreamEvent{}, false},
		{"missing type", `{"data":{"sessionID":"ses_1"}}`, StreamEvent{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseOpencodeV2Event([]byte(tc.in))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseOpencodeV1SessionList(t *testing.T) {
	payload := `[
		{"id":"ses_top","parentID":null},
		{"id":"ses_child","parentID":"ses_top"},
		{"id":"ses_bare"},
		{"id":"ses_blank","parentID":""}
	]`
	got := parseOpencodeV1SessionList([]byte(payload))
	if want := []string{"ses_child"}; !slices.Equal(got, want) {
		t.Errorf("parseOpencodeV1SessionList() = %v, want %v", got, want)
	}
	if got := parseOpencodeV1SessionList([]byte("not json")); got != nil {
		t.Errorf("malformed payload = %v, want nil", got)
	}
}

func TestParseOpencodeV2SessionList(t *testing.T) {
	payload := `{"data":[
		{"id":"ses_top","parentID":null},
		{"id":"ses_child","parentID":"ses_top"},
		{"id":"ses_bare"}
	]}`
	got := parseOpencodeV2SessionList([]byte(payload))
	if want := []string{"ses_child"}; !slices.Equal(got, want) {
		t.Errorf("parseOpencodeV2SessionList() = %v, want %v", got, want)
	}
	if got := parseOpencodeV2SessionList([]byte("not json")); got != nil {
		t.Errorf("malformed payload = %v, want nil", got)
	}
}
