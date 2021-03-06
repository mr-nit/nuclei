package progress

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/projectdiscovery/clistats"
	"github.com/projectdiscovery/gologger"
)

// Progress is a progress instance for showing program stats
type Progress struct {
	active       bool
	tickDuration time.Duration
	stats        clistats.StatisticsClient
	server       *http.Server
}

// NewProgress creates and returns a new progress tracking object.
func NewProgress(active, metrics bool, port int) (*Progress, error) {
	var tickDuration time.Duration
	if active {
		tickDuration = 5 * time.Second
	} else {
		tickDuration = -1
	}

	progress := &Progress{}

	stats, err := clistats.New()
	if err != nil {
		return nil, err
	}
	progress.active = active
	progress.stats = stats
	progress.tickDuration = tickDuration

	if metrics {
		http.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
			metrics := progress.getMetrics()
			_ = json.NewEncoder(w).Encode(metrics)
		})
		progress.server = &http.Server{
			Addr:    net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
			Handler: http.DefaultServeMux,
		}
		go func() {
			if err := progress.server.ListenAndServe(); err != nil {
				gologger.Warningf("Could not serve metrics: %s\n", err)
			}
		}()
	}
	return progress, nil
}

// Init initializes the progress display mechanism by setting counters, etc.
func (p *Progress) Init(hostCount int64, rulesCount int, requestCount int64) {
	p.stats.AddStatic("templates", rulesCount)
	p.stats.AddStatic("hosts", hostCount)
	p.stats.AddStatic("startedAt", time.Now())
	p.stats.AddCounter("requests", uint64(0))
	p.stats.AddCounter("errors", uint64(0))
	p.stats.AddCounter("total", uint64(requestCount))

	if p.active {
		if err := p.stats.Start(makePrintCallback(), p.tickDuration); err != nil {
			gologger.Warningf("Couldn't start statistics: %s\n", err)
		}
	}
}

// AddToTotal adds a value to the total request count
func (p *Progress) AddToTotal(delta int64) {
	p.stats.IncrementCounter("total", int(delta))
}

// Update progress tracking information and increments the request counter by one unit.
func (p *Progress) Update() {
	p.stats.IncrementCounter("requests", 1)
}

// Drop drops the specified number of requests from the progress bar total.
// This may be the case when uncompleted requests are encountered and shouldn't be part of the total count.
func (p *Progress) Drop(count int64) {
	// mimic dropping by incrementing the completed requests
	p.stats.IncrementCounter("errors", int(count))
}

const bufferSize = 128

func makePrintCallback() func(stats clistats.StatisticsClient) {
	builder := &strings.Builder{}
	builder.Grow(bufferSize)

	return func(stats clistats.StatisticsClient) {
		builder.WriteRune('[')
		startedAt, _ := stats.GetStatic("startedAt")
		duration := time.Since(startedAt.(time.Time))
		builder.WriteString(fmtDuration(duration))
		builder.WriteRune(']')

		templates, _ := stats.GetStatic("templates")
		builder.WriteString(" | Templates: ")
		builder.WriteString(clistats.String(templates))
		hosts, _ := stats.GetStatic("hosts")
		builder.WriteString(" | Hosts: ")
		builder.WriteString(clistats.String(hosts))

		requests, _ := stats.GetCounter("requests")
		total, _ := stats.GetCounter("total")

		builder.WriteString(" | RPS: ")
		builder.WriteString(clistats.String(uint64(float64(requests) / duration.Seconds())))

		errors, _ := stats.GetCounter("errors")
		builder.WriteString(" | Errors: ")
		builder.WriteString(clistats.String(errors))

		builder.WriteString(" | Requests: ")
		builder.WriteString(clistats.String(requests))
		builder.WriteRune('/')
		builder.WriteString(clistats.String(total))
		builder.WriteRune(' ')
		builder.WriteRune('(')
		//nolint:gomnd // this is not a magic number
		builder.WriteString(clistats.String(uint64(float64(requests) / float64(total) * 100.0)))
		builder.WriteRune('%')
		builder.WriteRune(')')
		builder.WriteRune('\n')

		fmt.Fprintf(os.Stderr, "%s", builder.String())
		builder.Reset()
	}
}

// getMetrics returns a map of important metrics for client
func (p *Progress) getMetrics() map[string]interface{} {
	results := make(map[string]interface{})

	startedAt, _ := p.stats.GetStatic("startedAt")
	duration := time.Since(startedAt.(time.Time))

	results["startedAt"] = startedAt.(time.Time)
	results["duration"] = fmtDuration(duration)
	templates, _ := p.stats.GetStatic("templates")
	results["templates"] = clistats.String(templates)
	hosts, _ := p.stats.GetStatic("hosts")
	results["hosts"] = clistats.String(hosts)
	requests, _ := p.stats.GetCounter("requests")
	results["requests"] = clistats.String(requests)
	total, _ := p.stats.GetCounter("total")
	results["total"] = clistats.String(total)
	results["rps"] = clistats.String(uint64(float64(requests) / duration.Seconds()))
	errors, _ := p.stats.GetCounter("errors")
	results["errors"] = clistats.String(errors)

	//nolint:gomnd // this is not a magic number
	percentData := (float64(requests) * float64(100)) / float64(total)
	percent := clistats.String(uint64(percentData))
	results["percent"] = percent
	return results
}

// fmtDuration formats the duration for the time elapsed
func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

// Stop stops the progress bar execution
func (p *Progress) Stop() {
	if p.active {
		if err := p.stats.Stop(); err != nil {
			gologger.Warningf("Couldn't stop statistics: %s\n", err)
		}
	}
	if p.server != nil {
		_ = p.server.Shutdown(context.Background())
	}
}
