package collect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Backup records the most recent successful database dump. FromS3 is true
// when the evidence came from S3 itself: the object provably exists, with its
// real upload time and size. The alternative is the nightly job's journal
// line, which only proves the job *claimed* to back up, and the Pi's journal
// is volatile, so a reboot erases that evidence entirely. S3 is therefore
// preferred whenever the listing works.
type Backup struct {
	At  time.Time
	Key string
	// Size is the dump's size in bytes; only S3 knows it.
	Size int64
	// FromS3 reports whether At and Key came from the S3 listing rather than
	// the journal.
	FromS3 bool
	OK     bool
}

// Stale reports whether the newest backup is older than expected for a nightly
// job.
func (b Backup) Stale(now time.Time) bool {
	return b.OK && now.Sub(b.At) > 26*time.Hour
}

// s3BackupBucket is where the nightly job uploads production dumps. The
// seasoning-pi-backer-upper IAM user has s3:ListBucket scoped to exactly this
// bucket, which is what lets the monitor verify the newest dump exists.
const s3BackupBucket = "seasoning-backups-874432646837-us-east-1-an"

// Backup reports the newest database dump: from S3 when the listing works,
// otherwise from the nightly job's journal. The S3 call's stderr is silenced
// so an IAM hiccup degrades to the journal line instead of an error.
func (c Collector) Backup(ctx context.Context) (Backup, error) {
	out, err := c.Runner.Run(ctx, backupScript)
	if err != nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			return Backup{}, fmt.Errorf("%w: %s", err, trimmed)
		}
		return Backup{}, err
	}
	return parseBackupScript(out), nil
}

const backupScript = `echo '@@SM:S3BACKUP@@'
aws s3api list-objects-v2 --bucket ` + s3BackupBucket + ` --query 'sort_by(Contents, &LastModified)[-1].[Key,LastModified,Size]' --output text 2>/dev/null || true
echo '@@SM:JOURNALBACKUP@@'
journalctl -u seasoning-nightly --since "7 days ago" -o short-iso --no-pager 2>/dev/null | grep "Backed up to" | tail -1 || true
true`

func parseBackupScript(out string) Backup {
	s := parseSections(out)
	if b := parseS3Backup(s.plain["S3BACKUP"]); b.OK {
		return b
	}
	return parseBackup(s.plain["JOURNALBACKUP"])
}

// parseS3Backup reads the aws CLI's text output for the newest object:
// key, RFC3339 LastModified, and size in bytes, whitespace-separated.
func parseS3Backup(s string) Backup {
	fields := strings.Fields(s)
	if len(fields) != 3 {
		return Backup{}
	}
	at, err := time.Parse(time.RFC3339, fields[1])
	if err != nil {
		return Backup{}
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return Backup{}
	}
	return Backup{At: at, Key: s3BackupBucket + "/" + fields[0], Size: size, FromS3: true, OK: true}
}

func parseBackup(s string) Backup {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Backup{}
	}
	at, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return Backup{}
	}
	key := ""
	for _, field := range fields {
		if rest, ok := strings.CutPrefix(field, "s3://"); ok {
			key = rest
			break
		}
	}
	return Backup{At: at, Key: key, OK: true}
}
