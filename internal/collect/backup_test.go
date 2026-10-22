package collect

import (
	"testing"
	"time"
)

func TestParseS3Backup(t *testing.T) {
	got := parseS3Backup("seasoning_production_20261021_000028.dump.gz\t2026-10-22T00:52:35+00:00\t12860484")
	if !got.OK {
		t.Fatalf("backup = %+v", got)
	}
	if !got.FromS3 {
		t.Errorf("backup should be marked as coming from S3")
	}
	if got.At.Unix() != time.Date(2026, 10, 22, 0, 52, 35, 0, time.UTC).Unix() {
		t.Errorf("backup at = %v", got.At)
	}
	if got.Key != s3BackupBucket+"/seasoning_production_20261021_000028.dump.gz" {
		t.Errorf("backup key = %q", got.Key)
	}
	if got.Size != 12860484 {
		t.Errorf("backup size = %d", got.Size)
	}

	if got := parseS3Backup("None"); got.OK {
		t.Errorf("an empty bucket should not parse as a backup")
	}
	if got := parseS3Backup(""); got.OK {
		t.Errorf("empty output should not be OK")
	}
	if got := parseS3Backup("key not-a-timestamp 12"); got.OK {
		t.Errorf("malformed output should not be OK")
	}
}

func TestParseBackupScriptPrefersS3(t *testing.T) {
	out := `@@SM:S3BACKUP@@
seasoning_production_20261021_000028.dump.gz	2026-10-22T00:52:35+00:00	12860484
@@SM:JOURNALBACKUP@@
2026-10-21T00:00:31-04:00 host backup-db[1]: Backed up to s3://bucket/older.dump.gz
`
	got := parseBackupScript(out)
	if !got.FromS3 || got.Size != 12860484 {
		t.Errorf("expected the S3 evidence to win, got %+v", got)
	}
}

func TestParseBackupScriptFallsBackToJournal(t *testing.T) {
	out := `@@SM:S3BACKUP@@
@@SM:JOURNALBACKUP@@
2026-10-21T00:00:31-04:00 host backup-db[1]: Backed up to s3://bucket/seasoning_production_20261021_000028.dump.gz
`
	got := parseBackupScript(out)
	if !got.OK || got.FromS3 {
		t.Fatalf("expected the journal fallback, got %+v", got)
	}
	if got.Key != "bucket/seasoning_production_20261021_000028.dump.gz" {
		t.Errorf("backup key = %q", got.Key)
	}
	if got.At.Unix() != time.Date(2026, 10, 21, 4, 0, 31, 0, time.UTC).Unix() {
		t.Errorf("backup at = %v", got.At)
	}
}

func TestParseBackupScriptNothingFound(t *testing.T) {
	out := `@@SM:S3BACKUP@@
@@SM:JOURNALBACKUP@@
`
	if got := parseBackupScript(out); got.OK {
		t.Errorf("no evidence anywhere should not be OK, got %+v", got)
	}
}

func TestParseBackup(t *testing.T) {
	line := "2026-09-25T00:00:11-04:00 maxwells-raspberry-pi-5 backup-db[105670]: Backed up to s3://bucket/seasoning_production_20260925_000007.dump.gz"
	got := parseBackup(line)
	if !got.OK {
		t.Fatalf("backup = %+v", got)
	}
	if got.At.Unix() != time.Date(2026, 9, 25, 4, 0, 11, 0, time.UTC).Unix() {
		t.Errorf("backup at = %v", got.At)
	}
	if got.Key != "bucket/seasoning_production_20260925_000007.dump.gz" {
		t.Errorf("backup key = %q", got.Key)
	}

	if got := parseBackup(""); got.OK {
		t.Errorf("empty backup should not be OK")
	}
	if got := parseBackup("not a timestamp"); got.OK {
		t.Errorf("malformed backup should not be OK")
	}
}

func TestBackupStale(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	fresh := Backup{At: now.Add(-12 * time.Hour), OK: true}
	if fresh.Stale(now) {
		t.Errorf("12h old backup should be fresh")
	}
	old := Backup{At: now.Add(-30 * time.Hour), OK: true}
	if !old.Stale(now) {
		t.Errorf("30h old backup should be stale")
	}
	if (Backup{}).Stale(now) {
		t.Errorf("a missing backup should not be reported stale, just missing")
	}
}
