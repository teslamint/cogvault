package trial

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

const selectedFixtureURL = "https://journal.example.com/paper.pdf"

func selectedReportSource(t *testing.T, s *Service, raw string) string {
	t.Helper()
	m := manifestFixture()
	m.Sources[0].AcquireURL = raw
	r, err := s.ImportManifest(ctx, manifestJSON(t, m))
	if err != nil {
		t.Fatal(err)
	}
	return r.CaptureID
}

func TestSelectedPublicReportAdmissionAndWorker(t *testing.T) {
	for _, tc := range []struct {
		name        string
		afterSubmit string
		mixedDNS    bool
		wantResolve int
		wantDial    int
	}{
		{name: "selected admission reaches checked public dial", afterSubmit: ReportURLPolicySelected, wantResolve: 1, wantDial: 1},
		{name: "mixed DNS is blocked before dialing", afterSubmit: ReportURLPolicySelected, mixedDNS: true, wantResolve: 1},
		{name: "rolling back policy stops queued job before network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reportService(t)
			s.Owner, s.Lease = "test-worker", time.Minute
			resolves, dials := 0, 0
			s.Acquirer.Resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
				resolves++
				if host != "journal.example.com" {
					t.Fatalf("unexpected host: %s", host)
				}
				addr := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
				if tc.mixedDNS {
					addr = append(addr, netip.MustParseAddr("10.0.0.2"))
				}
				return addr, nil
			}
			s.Acquirer.Dial = func(_ context.Context, _, address string) (net.Conn, error) {
				dials++
				if address != "93.184.216.34:443" {
					t.Fatalf("dial was not pinned to checked IP: %s", address)
				}
				return nil, errors.New("synthetic dial stopped before I/O")
			}
			capID := selectedReportSource(t, s, selectedFixtureURL)
			if j, _, err := s.RunOnce(ctx); j != nil || err != nil || resolves != 0 || dials != 0 {
				t.Fatalf("manifest import initiated acquisition: %v %v", j, err)
			}
			if _, _, err := s.SubmitSource(ctx, selectedFixtureURL, "test", capID, "public"); !errors.Is(err, ErrPolicy) {
				t.Fatalf("default must refuse unlisted report URL: %v", err)
			}
			s.Cfg.ReportURLPolicy = ReportURLPolicySelected
			if _, _, err := s.Submit(ctx, selectedFixtureURL, "test"); !errors.Is(err, ErrPolicy) {
				t.Fatalf("unlinked submission broadened: %v", err)
			}
			if _, _, err := s.SubmitSource(ctx, selectedFixtureURL+"-other", "test", capID, "public"); !errors.Is(err, ErrPolicy) {
				t.Fatalf("unselected URL accepted: %v", err)
			}
			if _, _, err := s.SubmitSource(ctx, selectedFixtureURL, "test", capID, "kci"); !errors.Is(err, ErrPolicy) {
				t.Fatalf("manual source accepted: %v", err)
			}
			if _, _, err := s.SubmitSource(ctx, selectedFixtureURL, "test", capID, "public"); err != nil {
				t.Fatalf("selected URL not queued: %v", err)
			}
			s.Cfg.ReportURLPolicy = tc.afterSubmit
			if got, _, err := s.RunOnce(ctx); got == nil || err != nil {
				t.Fatalf("worker did not handle queued job: %v %v", got, err)
			}
			if resolves != tc.wantResolve || dials != tc.wantDial {
				t.Fatalf("resolve/dial = %d/%d, want %d/%d", resolves, dials, tc.wantResolve, tc.wantDial)
			}
		})
	}
}

func TestSelectedPublicReportRedirectAndOldExactPolicy(t *testing.T) {
	s := reportService(t)
	s.Cfg.ReportURLPolicy = ReportURLPolicySelected
	q := s.reportAcquirerFor(selectedFixtureURL)
	for raw, want := range map[string]bool{
		"https://journal.example.com/final.pdf":     true,
		"https://new-cdn.example.com/final.pdf":     false,
		"http://journal.example.com/final.pdf":      false,
		"https://journal.example.com/final.pdf?x=1": false,
		"https://journal.example.com:8443/final":    false,
		"https://x:y@journal.example.com/final":     false,
		"https://journal.example.com/final#section": false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := q.AllowURL(u); got != want {
			t.Errorf("%s: got %v, want %v", raw, got, want)
		}
	}
	for _, raw := range []string{"https://nas.local/a", "https://device.home.arpa/a", "https://10.0.0.2/a", "https://localhost/a", "https://journal.example.com/a#"} {
		if s.reportURLAllowed(raw) {
			t.Errorf("unsafe URL was admitted: %s", raw)
		}
	}
	first := "https://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&fileSn=2"
	if s.reportURLAllowed(first) {
		t.Fatal("unconfigured query URL was admitted")
	}
	s.Cfg.AllowedHosts = []string{"www.bok.or.kr", "file-cdn.bok.or.kr"}
	s.Cfg.AllowedURLs = []string{first}
	s.Cfg.RedirectURLs = []string{"https://file-cdn.bok.or.kr/cdn/a.pdf"}
	r, err := url.Parse(s.Cfg.RedirectURLs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !s.reportURLAllowed(first) || !s.reportAcquirerFor(first).AllowURL(r) {
		t.Fatal("existing exact operator policy must keep its audited redirect handling")
	}
}
