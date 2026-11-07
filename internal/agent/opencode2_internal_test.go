package agent

import (
	"slices"
	"testing"
)

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
