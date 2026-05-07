package npm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientCoalescesPackumentFetches(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		time.Sleep(25 * time.Millisecond)
		fmt.Fprint(w, `{"name":"demo","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"demo","version":"1.0.0"}}}`)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Packument(context.Background(), "demo"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("registry hits = %d, want 1", got)
	}
}
