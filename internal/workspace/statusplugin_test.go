package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActivityFromReport(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Second)

	cases := []struct {
		name     string
		report   statusReport
		wantAct  Activity
		wantPend int
	}{
		{"working", statusReport{Activity: "working", UpdatedAt: fresh}, ActivityWorking, 0},
		{"idle maps to waiting", statusReport{Activity: "idle", UpdatedAt: fresh}, ActivityWaiting, 0},
		{"needs-approval maps to approval", statusReport{Activity: "needs-approval", PendingApproval: 2, UpdatedAt: fresh}, ActivityApproval, 2},
		{"error", statusReport{Activity: "error", UpdatedAt: fresh}, ActivityError, 0},
		{"unknown activity is asleep", statusReport{Activity: "weird", UpdatedAt: fresh}, ActivityAsleep, 0},
		{"stale heartbeat is asleep", statusReport{Activity: "working", PendingApproval: 1, UpdatedAt: now.Add(-time.Minute)}, ActivityAsleep, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act, pend := activityFromReport(tc.report, now)
			if act != tc.wantAct {
				t.Fatalf("activity = %q, want %q", act, tc.wantAct)
			}
			if pend != tc.wantPend {
				t.Fatalf("pending = %d, want %d", pend, tc.wantPend)
			}
		})
	}
}

func TestReadActivityMissingFile(t *testing.T) {
	// No status file + container stopped => the workspace has never been used.
	if act, pend := readActivity(t.TempDir(), false); act != ActivityNew || pend != 0 {
		t.Fatalf("readActivity(missing, stopped) = %q,%d; want new,0", act, pend)
	}
	// No status file + container running => opencode is still booting.
	if act, _ := readActivity(t.TempDir(), true); act != ActivityUnknown {
		t.Fatalf("readActivity(missing, running) = %q; want unknown", act)
	}
}

func TestReadActivityStoppedButUsed(t *testing.T) {
	home := writeStatus(t, `{"activity":"working","updatedAt":"`+time.Now().UTC().Format(time.RFC3339)+`"}`)
	// File present but container stopped => used before, nothing live to show.
	if act, _ := readActivity(home, false); act != ActivityUnknown {
		t.Fatalf("readActivity(present, stopped) = %q; want unknown", act)
	}
}

func TestReadActivityParsesFile(t *testing.T) {
	home := writeStatus(t, `{"activity":"needs-approval","pendingApproval":1,"sessions":1,"updatedAt":"`+
		time.Now().UTC().Format(time.RFC3339)+`"}`)
	if act, pend := readActivity(home, true); act != ActivityApproval || pend != 1 {
		t.Fatalf("readActivity = %q,%d; want approval,1", act, pend)
	}
}

func writeStatus(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, statusFileRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}
