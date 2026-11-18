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
			StreamEvent{
				Type:      "session.created",
				SessionID: "ses_child",
				ParentID:  "ses_root",
				EventID:   "evt_1",
				Title:     "probe",
			},
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
		{
			"title on a lifecycle event",
			`{"type":"session.created","data":{"sessionID":"ses_root","title":"probe run"}}`,
			StreamEvent{Type: "session.created", SessionID: "ses_root", Title: "probe run"},
			true,
		},
		{
			"renamed carries the id and title flat",
			`{"type":"session.renamed","data":{"sessionID":"ses_root","title":"new name"}}`,
			StreamEvent{Type: "session.renamed", SessionID: "ses_root", Title: "new name"},
			true,
		},
		{
			"background shell",
			`{"type":"session.shell.started","data":{"sessionID":"ses_root","shell":{"id":"sh_1","metadata":{"background":true}}}}`,
			StreamEvent{Type: "session.shell.started", SessionID: "ses_root", ShellID: "sh_1", ShellBackground: true},
			true,
		},
		{
			"foreground shell leaves background false",
			`{"type":"session.shell.started","data":{"sessionID":"ses_root","shell":{"id":"sh_2","metadata":{"background":false}}}}`,
			StreamEvent{Type: "session.shell.started", SessionID: "ses_root", ShellID: "sh_2"},
			true,
		},
		{
			"ended carries the id too",
			`{"type":"session.shell.ended","data":{"sessionID":"ses_root","shell":{"id":"sh_1"}}}`,
			StreamEvent{Type: "session.shell.ended", SessionID: "ses_root", ShellID: "sh_1"},
			true,
		},
		{
			// The agent's shell tool emits shell.created with the shell id and
			// session id nested under info; a still-running tool shell at the
			// top-level idle is a background command.
			"tool shell created reads info",
			`{"type":"shell.created","data":{"info":{"id":"sh_9","status":"running","metadata":{"sessionID":"ses_root"}}}}`,
			StreamEvent{Type: "shell.created", SessionID: "ses_root", ShellID: "sh_9", ShellBackground: true},
			true,
		},
		{
			// shell.exited carries no session id, only the shell id at data.id.
			"tool shell exited has no session",
			`{"type":"shell.exited","data":{"id":"sh_9","exit":0,"status":"exited"}}`,
			StreamEvent{Type: "shell.exited", ShellID: "sh_9"},
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
		{"id":"ses_child","parentID":"ses_top","title":"c"},
		{"id":"ses_top","title":"t"}
	]}`
	got := parseOpencodeV2SessionList([]byte(payload))
	want := []SessionRef{
		{ID: "ses_child", ParentID: "ses_top", Title: "c"},
		{ID: "ses_top", Title: "t"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseOpencodeV2SessionList() = %v, want %v", got, want)
	}
	if got := parseOpencodeV2SessionList([]byte("not json")); got != nil {
		t.Errorf("malformed payload = %v, want nil", got)
	}
}
