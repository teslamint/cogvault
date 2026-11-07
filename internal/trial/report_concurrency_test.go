package trial

import (
	"sync"
	"testing"
	"time"
)

func TestConcurrentSameReportSourceAtQueueCap(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
	s.Cfg.MaxQueued = 1
	second, err := Open(s.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	unlock, err := s.claimLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		id       string
		existing bool
		err      error
	}
	results := make(chan result, 12)
	var entered sync.WaitGroup
	entered.Add(12)
	for i := 0; i < 12; i++ {
		go func(i int) {
			svc := s
			if i%2 != 0 {
				svc = second
			}
			entered.Done()
			j, existing, err := svc.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public")
			results <- result{j.JobID, existing, err}
		}(i)
	}
	entered.Wait()
	// Let competing callers reach the held admission lock. Before the fix
	// their duplicate queries ran outside it and all observed an empty queue.
	time.Sleep(100 * time.Millisecond)
	unlock()
	created := 0
	id := ""
	for i := 0; i < 12; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !r.existing {
			created++
		}
		if id != "" && r.id != id {
			t.Fatal("duplicate submissions produced different jobs")
		}
		id = r.id
	}
	if created != 1 {
		t.Fatalf("created %d jobs", created)
	}
}
