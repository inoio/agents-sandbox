package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestMockSandboxExecDetachedRecordsCall(t *testing.T) {
	calls := &[]MockDetachedCall{}
	sb := &MockSandbox{ExecDetachedCalls: calls}

	got, err := sb.ExecDetached(context.Background(), "dockerd", []string{"-H", "unix:///var/run/docker.sock"})
	if err != nil {
		t.Fatalf("ExecDetached() error = %v", err)
	}
	if got == nil {
		t.Fatal("ExecDetached() returned nil job")
	}
	if len(*calls) != 1 {
		t.Fatalf("ExecDetachedCalls len = %d, want 1", len(*calls))
	}
	if (*calls)[0].Cmd != "dockerd" || len((*calls)[0].Args) != 2 || (*calls)[0].Args[0] != "-H" {
		t.Fatalf("recorded call = %+v, want dockerd -H unix:///var/run/docker.sock", (*calls)[0])
	}
}

func TestMockSandboxExecDetachedReturnsConfiguredJobAndError(t *testing.T) {
	job := &MockJob{ID_: "job-1"}
	sb := &MockSandbox{Job_: job}
	got, err := sb.ExecDetached(context.Background(), "x", nil)
	if err != nil || got != job {
		t.Fatalf("ExecDetached() = (%v, %v), want (job-1, nil)", got, err)
	}

	wantErr := errors.New("exec failed")
	sb = &MockSandbox{ExecDetachedFn: func(
		context.Context, string, []string, ...msbSdk.JobExecOption,
	) (Job, error) {
		return nil, wantErr
	}}
	if _, err := sb.ExecDetached(context.Background(), "x", nil); !errors.Is(err, wantErr) {
		t.Fatalf("ExecDetached() error = %v, want %v", err, wantErr)
	}
}

func TestMockSandboxGetJobAndListJobs(t *testing.T) {
	job := &MockJob{ID_: "job-1"}
	sb := &MockSandbox{Job_: job, Jobs: []msbSdk.JobInfo{{ID: "job-1"}}}

	got, err := sb.GetJob(context.Background(), "job-1")
	if err != nil || got != job {
		t.Fatalf("GetJob() = (%v, %v), want (job-1, nil)", got, err)
	}
	infos, err := sb.ListJobs(context.Background())
	if err != nil || len(infos) != 1 || infos[0].ID != "job-1" {
		t.Fatalf("ListJobs() = (%v, %v), want one job-1", infos, err)
	}
}

func TestMockSandboxGetJobAndListJobsUseCallbacksAndDefaults(t *testing.T) {
	callbackJob := &MockJob{ID_: "fn-job"}
	wantListErr := errors.New("list fn boom")
	sb := &MockSandbox{
		GetJobFn: func(_ context.Context, id string) (Job, error) { return callbackJob, nil },
		ListJobsFn: func(context.Context, ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error) {
			return nil, wantListErr
		},
	}
	got, err := sb.GetJob(context.Background(), "fn-job")
	if err != nil || got != callbackJob {
		t.Fatalf("GetJob() via callback = (%v, %v), want (fn-job, nil)", got, err)
	}
	if _, err := sb.ListJobs(context.Background()); !errors.Is(err, wantListErr) {
		t.Fatalf("ListJobs() via callback error = %v, want %v", err, wantListErr)
	}

	// Neither a callback nor a configured job: GetJob synthesizes one by id.
	defaultSB := &MockSandbox{}
	fallback, err := defaultSB.GetJob(context.Background(), "synthesized")
	if err != nil || fallback == nil || fallback.ID() != "synthesized" {
		t.Fatalf("GetJob() default = (%v, %v), want a job with id synthesized", fallback, err)
	}
}

func TestMockJobMethods(t *testing.T) {
	job := &MockJob{
		ID_:   "job-1",
		Info:  &msbSdk.JobInfo{ID: "job-1"},
		Logs_: []msbSdk.JobLogEntry{{Data: []byte("hello")}},
	}
	ctx := context.Background()

	if job.ID() != "job-1" {
		t.Errorf("ID() = %q, want job-1", job.ID())
	}
	info, err := job.Inspect(ctx)
	if err != nil || info == nil || info.ID != "job-1" {
		t.Fatalf("Inspect() = (%v, %v), want job-1", info, err)
	}
	logs, err := job.Logs(ctx, msbSdk.JobLogOptions{})
	if err != nil || len(logs) != 1 || string(logs[0].Data) != "hello" {
		t.Fatalf("Logs() = (%v, %v), want one hello entry", logs, err)
	}
	if err := job.Kill(ctx); err != nil || !job.DidKill {
		t.Fatalf("Kill() = %v, DidKill=%v; want nil, true", err, job.DidKill)
	}
	if err := job.Close(); err != nil || !job.DidClose {
		t.Fatalf("Close() = %v, DidClose=%v; want nil, true", err, job.DidClose)
	}
}

func TestMockJobInspectAndLogsCallbacks(t *testing.T) {
	ctx := context.Background()
	inspected := &msbSdk.JobInfo{ID: "callback"}
	logged := []msbSdk.JobLogEntry{{Data: []byte("from-callback")}}
	job := &MockJob{
		InspectFn: func(context.Context) (*msbSdk.JobInfo, error) { return inspected, nil },
		LogsFn: func(context.Context, msbSdk.JobLogOptions) ([]msbSdk.JobLogEntry, error) {
			return logged, nil
		},
	}

	info, err := job.Inspect(ctx)
	if err != nil || info != inspected {
		t.Fatalf("Inspect() via callback = (%v, %v), want (%v, nil)", info, err, inspected)
	}
	logs, err := job.Logs(ctx, msbSdk.JobLogOptions{})
	if err != nil || len(logs) != 1 || string(logs[0].Data) != "from-callback" {
		t.Fatalf("Logs() via callback = (%v, %v), want one from-callback entry", logs, err)
	}
}

// TestMockJobErrorFields verifies each MockJob method returns its configured
// error, so a caller can simulate a failing Inspect/Kill/Logs/Close.
func TestMockJobErrorFields(t *testing.T) {
	ctx := context.Background()
	inspectErr := errors.New("inspect boom")
	logsErr := errors.New("logs boom")
	killErr := errors.New("kill boom")
	closeErr := errors.New("close boom")
	job := &MockJob{InfoErr: inspectErr, LogsErr: logsErr, KillErr: killErr, CloseErr: closeErr}

	if _, err := job.Inspect(ctx); !errors.Is(err, inspectErr) {
		t.Fatalf("Inspect() error = %v, want %v", err, inspectErr)
	}
	if _, err := job.Logs(ctx, msbSdk.JobLogOptions{}); !errors.Is(err, logsErr) {
		t.Fatalf("Logs() error = %v, want %v", err, logsErr)
	}
	if err := job.Kill(ctx); !errors.Is(err, killErr) {
		t.Fatalf("Kill() error = %v, want %v", err, killErr)
	}
	if err := job.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close() error = %v, want %v", err, closeErr)
	}
}

func TestRealSandboxDetachedDelegates(t *testing.T) {
	ctx := context.Background()

	t.Run("ExecDetached", func(t *testing.T) {
		original := realSandboxExecDetached
		t.Cleanup(func() { realSandboxExecDetached = original })
		called := false
		realSandboxExecDetached = func(
			_ *msbSdk.Sandbox, _ context.Context, cmd string, args []string, _ ...msbSdk.JobExecOption,
		) (*msbSdk.Job, error) {
			called = true
			if cmd != "dockerd" || len(args) != 1 {
				t.Errorf("delegated (%q, %v), want dockerd [x]", cmd, args)
			}
			return &msbSdk.Job{}, nil
		}
		got, err := (realSandbox{}).ExecDetached(ctx, "dockerd", []string{"x"})
		if err != nil || !called {
			t.Fatalf("ExecDetached() = %v, called=%v; want nil, true", err, called)
		}
		if got == nil {
			t.Fatal("ExecDetached() returned a nil job")
		}
	})

	t.Run("ExecDetached error", func(t *testing.T) {
		original := realSandboxExecDetached
		t.Cleanup(func() { realSandboxExecDetached = original })
		wantErr := errors.New("boom")
		realSandboxExecDetached = func(
			_ *msbSdk.Sandbox, _ context.Context, _ string, _ []string, _ ...msbSdk.JobExecOption,
		) (*msbSdk.Job, error) {
			return nil, wantErr
		}
		if _, err := (realSandbox{}).ExecDetached(ctx, "x", nil); !errors.Is(err, wantErr) {
			t.Fatalf("ExecDetached() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("GetJob", func(t *testing.T) {
		original := realSandboxGetJob
		t.Cleanup(func() { realSandboxGetJob = original })
		called := false
		realSandboxGetJob = func(_ *msbSdk.Sandbox, _ context.Context, id string) (*msbSdk.Job, error) {
			called = true
			if id != "job-1" {
				t.Errorf("delegated id = %q, want job-1", id)
			}
			return &msbSdk.Job{}, nil
		}
		got, err := (realSandbox{}).GetJob(ctx, "job-1")
		if err != nil || !called {
			t.Fatalf("GetJob() = %v, called=%v; want nil, true", err, called)
		}
		if got == nil {
			t.Fatal("GetJob() returned a nil job")
		}
	})

	t.Run("GetJob error", func(t *testing.T) {
		original := realSandboxGetJob
		t.Cleanup(func() { realSandboxGetJob = original })
		wantErr := errors.New("get boom")
		realSandboxGetJob = func(_ *msbSdk.Sandbox, _ context.Context, _ string) (*msbSdk.Job, error) {
			return nil, wantErr
		}
		if _, err := (realSandbox{}).GetJob(ctx, "job-1"); !errors.Is(err, wantErr) {
			t.Fatalf("GetJob() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("ListJobs paginates", func(t *testing.T) {
		original := realSandboxListJobs
		t.Cleanup(func() { realSandboxListJobs = original })
		var optsSeen []int
		realSandboxListJobs = func(_ *msbSdk.Sandbox, _ context.Context, opts ...msbSdk.JobListOption) (*msbSdk.JobPage, error) {
			optsSeen = append(optsSeen, len(opts))
			switch len(optsSeen) {
			case 1:
				next := "c1"
				return &msbSdk.JobPage{Items: []msbSdk.JobInfo{{ID: "a"}}, NextCursor: &next}, nil
			default:
				return &msbSdk.JobPage{Items: []msbSdk.JobInfo{{ID: "b"}}}, nil
			}
		}
		got, err := (realSandbox{}).ListJobs(ctx)
		if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
			t.Fatalf("ListJobs() = (%v, err=%v), want [a b] over 2 pages", got, err)
		}
		// Page 1 carries no cursor option; page 2 must carry exactly the cursor.
		if len(optsSeen) != 2 || optsSeen[0] != 0 || optsSeen[1] != 1 {
			t.Fatalf("per-page option counts = %v, want [0 1] so page 2 sends the cursor", optsSeen)
		}
	})

	t.Run("ListJobs stops on an empty cursor", func(t *testing.T) {
		original := realSandboxListJobs
		t.Cleanup(func() { realSandboxListJobs = original })
		empty := ""
		calls := 0
		realSandboxListJobs = func(_ *msbSdk.Sandbox, _ context.Context, _ ...msbSdk.JobListOption) (*msbSdk.JobPage, error) {
			calls++
			return &msbSdk.JobPage{Items: []msbSdk.JobInfo{{ID: "a"}}, NextCursor: &empty}, nil
		}
		got, err := (realSandbox{}).ListJobs(ctx)
		if err != nil || len(got) != 1 || got[0].ID != "a" {
			t.Fatalf("ListJobs() = (%v, err=%v), want [a]", got, err)
		}
		if calls != 1 {
			t.Fatalf("ListJobs made %d calls; an empty cursor must terminate after the first page", calls)
		}
	})

	t.Run("ListJobs error", func(t *testing.T) {
		original := realSandboxListJobs
		t.Cleanup(func() { realSandboxListJobs = original })
		wantErr := errors.New("list failed")
		realSandboxListJobs = func(
			_ *msbSdk.Sandbox, _ context.Context, _ ...msbSdk.JobListOption,
		) (*msbSdk.JobPage, error) {
			return nil, wantErr
		}
		if _, err := (realSandbox{}).ListJobs(ctx); !errors.Is(err, wantErr) {
			t.Fatalf("ListJobs() error = %v, want %v", err, wantErr)
		}
	})
}

func TestRealJobDelegates(t *testing.T) {
	ctx := context.Background()

	t.Run("ID", func(t *testing.T) {
		original := realJobID
		t.Cleanup(func() { realJobID = original })
		realJobID = func(_ *msbSdk.Job) string { return "job-1" }
		if got := (realJob{}).ID(); got != "job-1" {
			t.Fatalf("ID() = %q, want job-1", got)
		}
	})

	t.Run("Inspect", func(t *testing.T) {
		original := realJobInspect
		t.Cleanup(func() { realJobInspect = original })
		want := &msbSdk.JobInfo{ID: "job-1"}
		realJobInspect = func(_ *msbSdk.Job, _ context.Context) (*msbSdk.JobInfo, error) {
			return want, nil
		}
		got, err := (realJob{}).Inspect(ctx)
		if err != nil || got != want {
			t.Fatalf("Inspect() = (%v, %v), want (%v, nil)", got, err, want)
		}
	})

	t.Run("Kill", func(t *testing.T) {
		original := realJobKill
		t.Cleanup(func() { realJobKill = original })
		called := false
		realJobKill = func(_ *msbSdk.Job, _ context.Context) error { called = true; return nil }
		if err := (realJob{}).Kill(ctx); err != nil || !called {
			t.Fatalf("Kill() = %v, called=%v; want nil, true", err, called)
		}
	})

	t.Run("Logs", func(t *testing.T) {
		original := realJobLogs
		t.Cleanup(func() { realJobLogs = original })
		called := false
		realJobLogs = func(
			_ *msbSdk.Job, _ context.Context, _ msbSdk.JobLogOptions,
		) ([]msbSdk.JobLogEntry, error) {
			called = true
			return []msbSdk.JobLogEntry{{Data: []byte("x")}}, nil
		}
		got, err := (realJob{}).Logs(ctx, msbSdk.JobLogOptions{})
		if err != nil || !called || len(got) != 1 {
			t.Fatalf("Logs() = (%v, %v), called=%v; want one entry", got, err, called)
		}
	})

	t.Run("Close", func(t *testing.T) {
		original := realJobClose
		t.Cleanup(func() { realJobClose = original })
		called := false
		realJobClose = func(_ *msbSdk.Job) error { called = true; return nil }
		if err := (realJob{}).Close(); err != nil || !called {
			t.Fatalf("Close() = %v, called=%v; want nil, true", err, called)
		}
	})
}
