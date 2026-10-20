package ludusapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

type playbookRun struct {
	playbooks, extraVarsFiles []string
	extraVars                 map[string]any
}

// playbookFixture runs RunPlaybook for OWNER on range 10 with the Ansible
// execution replaced: each run writes logContent to the shared range log.
type playbookFixture struct {
	*hostFixture
	logPath  string
	stateDir string
	runs     []playbookRun
	during   func(e *core.RequestEvent) // runs while the playbook "executes"
	log      string
	fail     bool
}

func newPlaybookFixture(t *testing.T) *playbookFixture {
	f := &playbookFixture{hostFixture: newHostFixture(t)}
	f.logPath = filepath.Join(f.server.LudusInstallPath, "ranges", "OWNER", "ansible.log")
	if err := os.MkdirAll(filepath.Dir(f.logPath), 0700); err != nil {
		t.Fatal(err)
	}
	f.stateDir = filepath.Join(ServerConfiguration.DataDirectory, "plugin-state", "observer")
	for _, name := range []string{"ansible/observer.yml", "ansible/vars.yml"} {
		path := filepath.Join(f.stateDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("- hosts: localhost\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		runningRangeLogIDByRangeID.Delete(f.ranges[10].Id)
	})
	return f
}

func (f *playbookFixture) host(level protocol.Level) *HostService {
	h := newHostService(f.server, testPlugin("observer", level, protocol.SourceUploaded))
	h.runPlaybook = func(e *core.RequestEvent, playbooks, extraVarsFiles []string, extraVars map[string]any, _ string, _ bool, _ string) (string, error) {
		f.runs = append(f.runs, playbookRun{playbooks, extraVarsFiles, extraVars})
		if err := os.WriteFile(f.logPath, []byte(f.log), 0600); err != nil {
			return "", err
		}
		if f.during != nil {
			f.during(e)
		}
		if f.fail {
			return f.log, errors.New("exit status 2")
		}
		return f.log, nil
	}
	return h
}

func (f *playbookFixture) run(t *testing.T, h *HostService, e *core.RequestEvent, request protocol.PlaybookRequest) protocol.PlaybookReply {
	t.Helper()
	call, end := h.beginRequest(e)
	defer end()
	request.Call = call
	var reply protocol.PlaybookReply
	if err := h.RunPlaybook(request, &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func (f *playbookFixture) assertArchived(t *testing.T, logID, status, content string) {
	t.Helper()
	record, err := f.pb.FindRecordById("logs", logID)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := readLogFileFromRecord(f.pb, record)
	if err != nil {
		t.Fatalf("log %s not archived: %v", logID, err)
	}
	if record.GetString("status") != status || string(archived) != content {
		t.Fatalf("log %s: status=%q content=%q; want status=%q content=%q", logID, record.GetString("status"), archived, status, content)
	}
}

func TestRunPlaybookArchivesEachRun(t *testing.T) {
	f := newPlaybookFixture(t)
	h := f.host(protocol.LevelStandard)
	request := protocol.PlaybookRequest{
		Root:           protocol.PlaybookRootStateDir,
		Playbook:       "ansible/observer.yml",
		ExtraVarsFiles: []string{"ansible/vars.yml"},
		ExtraVarsJSON:  []byte(`{"observer_port": 8080}`),
	}

	f.log, f.fail = "TASK [x]\nfatal: [OWNER-vm]: FAILED! => {}\n", true
	failed := f.run(t, h, f.event(t, f.owner, f.owner, 10), request)
	if failed.Err != nil || !failed.Result.Failed || !slices.Contains(failed.Result.Errors, "fatal: [OWNER-vm]: FAILED! => {}") {
		t.Fatalf("failed run: %+v", failed)
	}
	f.assertArchived(t, failed.Result.LogID, "failure", f.log)

	f.log, f.fail = "VM two: plugin configuration applied\n", false
	succeeded := f.run(t, h, f.event(t, f.owner, f.owner, 10), request)
	if succeeded.Err != nil || succeeded.Result.Failed || succeeded.Result.Output != f.log || succeeded.Result.LogID == failed.Result.LogID {
		t.Fatalf("successful run: %+v", succeeded)
	}
	f.assertArchived(t, succeeded.Result.LogID, "success", f.log)
	f.assertArchived(t, failed.Result.LogID, "failure", "TASK [x]\nfatal: [OWNER-vm]: FAILED! => {}\n")

	resolvedState, _ := filepath.EvalSymlinks(f.stateDir)
	last := f.runs[len(f.runs)-1]
	if !slices.Equal(last.playbooks, []string{filepath.Join(resolvedState, "ansible", "observer.yml")}) ||
		!slices.Equal(last.extraVarsFiles, []string{"@" + filepath.Join(resolvedState, "ansible", "vars.yml")}) ||
		last.extraVars["observer_port"] != float64(8080) {
		t.Fatalf("playbook invocation: %+v", last)
	}

	// An abort that archived the run first wins; the later finish must not
	// replace it with the next operation's ansible.log.
	f.log = "VM three: interrupted\n"
	f.during = func(*core.RequestEvent) {
		logID, _ := runningRangeLogIDByRangeID.Load(f.ranges[10].Id)
		if err := finalizeRunningLogHistoryByID(f.pb, logID.(string), "aborted", f.logPath, time.Now()); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(f.logPath, []byte("a later operation overwrote the current log\n"), 0600); err != nil {
			t.Error(err)
		}
	}
	aborted := f.run(t, h, f.event(t, f.owner, f.owner, 10), request)
	f.assertArchived(t, aborted.Result.LogID, "aborted", "VM three: interrupted\n")

	// A restart of the sibling service marks the row aborted without an
	// archive; the run's final output is still archived.
	f.log = "VM four: completed after sibling host restart\n"
	f.during = func(*core.RequestEvent) {
		if err := startupReconcileRunningLogHistory(f.pb); err != nil {
			t.Error(err)
		}
	}
	reconciled := f.run(t, h, f.event(t, f.owner, f.owner, 10), request)
	f.assertArchived(t, reconciled.Result.LogID, "aborted", f.log)

	// The archive survives the caller's HTTP connection going away.
	f.log = "VM five: finished after the client disconnected\n"
	ctx, cancel := context.WithCancel(context.Background())
	e := f.event(t, f.owner, f.owner, 10)
	e.Request = e.Request.WithContext(ctx)
	f.during = func(*core.RequestEvent) { cancel() }
	disconnected := f.run(t, h, e, request)
	if disconnected.Err != nil {
		t.Fatal(disconnected.Err)
	}
	f.assertArchived(t, disconnected.Result.LogID, "success", f.log)

	rangeRecord, err := f.pb.FindRecordById("ranges", f.ranges[10].Id)
	if err != nil || rangeRecord.GetString("rangeState") != "SUCCESS" {
		t.Fatalf("RunPlaybook changed range deployment state: %v, %v", rangeRecord, err)
	}
}

func TestRunPlaybookRejects(t *testing.T) {
	f := newPlaybookFixture(t)
	standard := f.host(protocol.LevelStandard)
	system := f.host(protocol.LevelSystem)
	outside := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.WriteFile(outside, []byte("- hosts: all\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.stateDir, "ansible", "escape.yml")); err != nil {
		t.Fatal(err)
	}
	rangeManagement := filepath.Join(f.server.LudusInstallPath, "ansible", "range-management")
	if err := os.MkdirAll(rangeManagement, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rangeManagement, "setup-kms.yml"), []byte("- hosts: all\n"), 0600); err != nil {
		t.Fatal(err)
	}

	stateDir := func(playbook string, varsFiles ...string) protocol.PlaybookRequest {
		return protocol.PlaybookRequest{Root: protocol.PlaybookRootStateDir, Playbook: playbook, ExtraVarsFiles: varsFiles}
	}
	owner := func() *core.RequestEvent { return f.event(t, f.owner, f.owner, 10) }
	for _, test := range []struct {
		name    string
		host    *HostService
		event   func() *core.RequestEvent
		request protocol.PlaybookRequest
		code    protocol.ErrorCode
	}{
		{"parent traversal", standard, owner, stateDir("../observer/ansible/observer.yml"), protocol.CodeInvalid},
		{"absolute path", standard, owner, stateDir(outside), protocol.CodeInvalid},
		{"symlink escape", standard, owner, stateDir("ansible/escape.yml"), protocol.CodeInvalid},
		{"vars file escape", standard, owner, stateDir("ansible/observer.yml", "../../../../etc/passwd"), protocol.CodeInvalid},
		{"missing playbook", standard, owner, stateDir("ansible/missing.yml"), protocol.CodeNotFound},
		{"extra vars not an object", standard, owner, protocol.PlaybookRequest{Root: protocol.PlaybookRootStateDir, Playbook: "ansible/observer.yml", ExtraVarsJSON: []byte(`[1]`)}, protocol.CodeInvalid},
		{"unknown root", standard, owner, protocol.PlaybookRequest{Root: "install", Playbook: "ansible/observer.yml"}, protocol.CodeInvalid},
		{"range-management for standard", standard, owner, protocol.PlaybookRequest{Root: protocol.PlaybookRootRangeManagement, Playbook: "setup-kms.yml"}, protocol.CodeForbidden},
		{"no range", standard, func() *core.RequestEvent { return f.event(t, f.other, f.other, 0) }, stateDir("ansible/observer.yml"), protocol.CodeInvalid},
		{"superuser has no user", standard, func() *core.RequestEvent { return f.event(t, f.superuser, nil, 10) }, stateDir("ansible/observer.yml"), protocol.CodeForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := len(f.runs)
			reply := f.run(t, test.host, test.event(), test.request)
			if hostCode(reply.Err) != test.code {
				t.Fatalf("error %v, want %q", reply.Err, test.code)
			}
			if len(f.runs) != before {
				t.Fatal("rejected request ran a playbook")
			}
		})
	}

	call, end := system.beginPluginCall(context.Background())
	var reply protocol.PlaybookReply
	system.RunPlaybook(protocol.PlaybookRequest{Call: call, Root: protocol.PlaybookRootStateDir, Playbook: "ansible/observer.yml"}, &reply)
	end()
	if hostCode(reply.Err) != protocol.CodeForbidden {
		t.Fatalf("plugin principal ran a playbook: %v", reply.Err)
	}

	f.log = "ok\n"
	allowed := f.run(t, system, owner(), protocol.PlaybookRequest{Root: protocol.PlaybookRootRangeManagement, Playbook: "setup-kms.yml"})
	resolved, _ := filepath.EvalSymlinks(filepath.Join(rangeManagement, "setup-kms.yml"))
	if allowed.Err != nil || len(f.runs) != 1 || !strings.HasSuffix(f.runs[0].playbooks[0], "setup-kms.yml") || f.runs[0].playbooks[0] != resolved {
		t.Fatalf("system range-management run: %+v %+v", allowed, f.runs)
	}
}
