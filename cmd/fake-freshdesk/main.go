// Command fake-freshdesk serves invented Freshdesk data for demos and tests.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"freshdesk-mcp-connector/internal/fakefd"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8089", "address to listen on")
	key := flag.String("key", "demo-key", "the only API key the fake accepts")
	every429 := flag.Int("every-429", 0, "answer every Nth request with 429 (0 = never)")
	every500 := flag.Int("every-500", 0, "answer every Nth request with 500 (0 = never)")
	outage := flag.Duration("outage", 0, "answer 500 to everything for this long after start, e.g. 8s")
	limit := flag.Int("limit-per-minute", 0, "enforce a real rate limit of N requests per minute (0 = none)")
	flag.Parse()

	h := fakefd.New(fakefd.Options{APIKey: *key, Every429: *every429, Every500: *every500, Outage: *outage, LimitPerMinute: *limit})
	log.Printf("fake Freshdesk (invented data) at http://%s/api/v2 , key=%q", *addr, *key)
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
