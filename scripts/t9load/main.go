// Command t9load offers a fixed RPS against one URL and never follows
// redirects. Used by scripts/traffic-runtime-t9.sh for cloud T9.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "", "target URL")
	rps := flag.Int("rps", 100, "offered requests per second")
	duration := flag.Duration("duration", time.Minute, "how long to offer load")
	headerFile := flag.String("header-file", "", "curl-style header file (Authorization: Bearer …)")
	workers := flag.Int("workers", 0, "client goroutines (default max(32, rps/4))")
	flag.Parse()
	if *url == "" || *rps <= 0 || *duration <= 0 {
		fmt.Fprintln(os.Stderr, "t9load: -url, -rps>0, -duration>0 required")
		os.Exit(2)
	}
	nWorkers := *workers
	if nWorkers <= 0 {
		nWorkers = *rps / 4
		if nWorkers < 32 {
			nWorkers = 32
		}
	}

	hdr := http.Header{}
	if *headerFile != "" {
		f, err := os.Open(*headerFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "t9load: header-file: %v\n", err)
			os.Exit(2)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				continue
			}
			key, val, ok := splitHeader(line)
			if !ok {
				fmt.Fprintf(os.Stderr, "t9load: bad header line\n")
				os.Exit(2)
			}
			hdr.Set(key, val)
		}
		_ = f.Close()
		if err := sc.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "t9load: header-file: %v\n", err)
			os.Exit(2)
		}
	}

	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			MaxIdleConns:        nWorkers * 2,
			MaxIdleConnsPerHost: nWorkers * 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	var (
		ok302   atomic.Int64
		other   atomic.Int64
		fail    atomic.Int64
		started atomic.Int64
		latMu   sync.Mutex
		lats    []time.Duration
	)
	lats = make([]time.Duration, 0, int((*duration).Seconds())**rps)

	jobs := make(chan struct{}, nWorkers*2)
	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				started.Add(1)
				t0 := time.Now()
				req, err := http.NewRequest(http.MethodGet, *url, nil)
				if err != nil {
					fail.Add(1)
					continue
				}
				req.Header = hdr.Clone()
				resp, err := client.Do(req)
				dt := time.Since(t0)
				latMu.Lock()
				lats = append(lats, dt)
				latMu.Unlock()
				if err != nil {
					fail.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusFound {
					ok302.Add(1)
				} else {
					other.Add(1)
				}
			}
		}()
	}

	start := time.Now()
	deadline := start.Add(*duration)
	interval := time.Second / time.Duration(*rps)
	ticker := time.NewTicker(interval)
	offered := 0
	for time.Now().Before(deadline) {
		<-ticker.C
		select {
		case jobs <- struct{}{}:
			offered++
		default:
			// saturated workers: still count as offered, drop the slot
			offered++
			fail.Add(1)
		}
	}
	ticker.Stop()
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)

	latMu.Lock()
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p50 := pct(lats, 50)
	p95 := pct(lats, 95)
	p99 := pct(lats, 99)
	latMu.Unlock()

	completed := ok302.Load() + other.Load() + fail.Load()
	achieved := 0.0
	if elapsed > 0 {
		achieved = float64(ok302.Load()) / elapsed.Seconds()
	}
	fmt.Printf("offered_rps=%d\n", *rps)
	fmt.Printf("duration_s=%.1f\n", elapsed.Seconds())
	fmt.Printf("offered=%d\n", offered)
	fmt.Printf("started=%d\n", started.Load())
	fmt.Printf("completed=%d\n", completed)
	fmt.Printf("status_302=%d\n", ok302.Load())
	fmt.Printf("status_other=%d\n", other.Load())
	fmt.Printf("errors=%d\n", fail.Load())
	fmt.Printf("achieved_rps=%.1f\n", achieved)
	fmt.Printf("p50_ms=%.2f\n", float64(p50)/float64(time.Millisecond))
	fmt.Printf("p95_ms=%.2f\n", float64(p95)/float64(time.Millisecond))
	fmt.Printf("p99_ms=%.2f\n", float64(p99)/float64(time.Millisecond))
	fmt.Printf("followed_redirect=no\n")
}

func splitHeader(line string) (string, string, bool) {
	for i := 0; i < len(line); i++ {
		if line[i] == ':' {
			k := line[:i]
			v := line[i+1:]
			for len(v) > 0 && (v[0] == ' ' || v[0] == '\t') {
				v = v[1:]
			}
			return k, v, k != ""
		}
	}
	return "", "", false
}

func pct(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := (p * (len(sorted) - 1)) / 100
	return sorted[i]
}
