package collect

import (
	"testing"
	"time"
)

func TestParseTraffic(t *testing.T) {
	got := parseTraffic("requests=8437 s2=3704 s3=4035 s4=697 s5=1")
	if got.Requests != 8437 || got.S2xx != 3704 || got.S3xx != 4035 || got.S4xx != 697 || got.S5xx != 1 {
		t.Errorf("traffic = %+v", got)
	}
	if got.Window != 24*time.Hour {
		t.Errorf("window = %v", got.Window)
	}
	if empty := parseTraffic(""); empty.Requests != 0 {
		t.Errorf("empty traffic = %+v", empty)
	}
}

func TestParseFailedUnits(t *testing.T) {
	if got := parseFailedUnits(""); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	got := parseFailedUnits("foo.service\nbar.service\n\n")
	if len(got) != 2 || got[0] != "foo.service" || got[1] != "bar.service" {
		t.Errorf("failed units = %v", got)
	}
}

func TestParseDatabase(t *testing.T) {
	got := parseDatabase("104 MB 7")
	if !got.OK || got.Size != "104 MB" || got.Connections != 7 {
		t.Errorf("database = %+v", got)
	}
	if got := parseDatabase(""); got.OK {
		t.Errorf("empty database should not be OK: %+v", got)
	}
}

func TestParseNginx(t *testing.T) {
	got := parseNginx("count=3\nlast_epoch=1790395200")
	if got.Count != 3 || !got.LastOK || got.LastAt.Unix() != 1790395200 {
		t.Errorf("nginx = %+v", got)
	}
	if empty := parseNginx("count=0"); empty.Count != 0 || empty.LastOK {
		t.Errorf("nginx without an age = %+v", empty)
	}
}

func TestNginxSummary(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if got := (Nginx{}).Summary(now); got != "0 errors" {
		t.Errorf("clean summary = %q", got)
	}
	one := Nginx{Count: 1, LastAt: now.Add(-6 * time.Hour), LastOK: true}
	if got := one.Summary(now); got != "1 error · 6h ago" {
		t.Errorf("summary = %q", got)
	}
	unknown := Nginx{Count: 2}
	if got := unknown.Summary(now); got != "2 errors" {
		t.Errorf("summary without age = %q", got)
	}
}

func TestParseStatsSections(t *testing.T) {
	out := `@@SM:TRAFFIC@@
requests=10 s2=6 s3=3 s4=1 s5=0
@@SM:FAILED@@
foo.service
@@SM:NGINX@@
count=0
@@SM:DB@@
104 MB 7
`
	got := parseStats(out)
	if got.Traffic.Requests != 10 {
		t.Errorf("traffic = %+v", got.Traffic)
	}
	if len(got.FailedUnits) != 1 || got.FailedUnits[0] != "foo.service" {
		t.Errorf("failed = %v", got.FailedUnits)
	}
	if got.Nginx.Count != 0 {
		t.Errorf("nginx = %+v", got.Nginx)
	}
	if !got.Database.OK || got.Database.Connections != 7 {
		t.Errorf("database = %+v", got.Database)
	}
}
