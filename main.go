package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aybabtme/uniplot/histogram"
	gocitiesjson "github.com/ringsaturn/go-cities.json"
	"github.com/sourcegraph/conc/pool"
	"go.uber.org/ratelimit"
)

var (
	countryCode = flag.String("country", "", "Country code")
	coordsOrder = flag.String("coords", "lat,lon", "Coordinates order, `lat,lon` or `lon,lat`")
	apiTPL      = flag.String("api", "http://localhost:8000/foo?lat=%s&lon=%s", "API URL template")
	qps         = flag.Int("qps", 10, "Queries per second")
	threads     = flag.Int("threads", 10, "Number of threads")
	runs        = flag.Int("runs", 10, "Run how many cities in one batch")
	timeout     = flag.Int("timeout", 10, "Timeout in seconds")

	cities []*gocitiesjson.City

	ok, failed, reqFailed int64
	failedStatusCode      = func() map[int]*int64 {
		m := map[int]*int64{}
		for i := 0; i < 600; i++ {
			m[i] = new(int64)
		}
		return m
	}()

	oklatency = []float64{}
	okmutex   sync.Mutex

	failedlatency = []float64{}
	failedmutex   sync.Mutex

	reqfailedlatency = []float64{}
	reqfailedmutex   sync.Mutex
)

func prepare() {
	flag.Parse()

	if *countryCode != "" {
		for _, city := range gocitiesjson.Cities {
			if city.Country == *countryCode {
				cities = append(cities, city)
			}
		}
	} else {
		cities = gocitiesjson.Cities
	}

	log.Println("countryCode", *countryCode)
	log.Println("coordsOrder", *coordsOrder)
	log.Println("apiTPL", *apiTPL)
	log.Println("qps", *qps)
	log.Println("threads", *threads)
	log.Println("runs", *runs)
	log.Println("timeout", *timeout)

}

func ReportFailedStatus() {
	for code, count := range failedStatusCode {
		if *count != 0 {
			fmt.Printf("http:%v, count:%v\n", code, *count)
		}
	}
}

func mean(numbers []float64) float64 {
	if len(numbers) == 0 {
		return 0
	}
	var total float64 = 0
	for _, v := range numbers {
		total += v
	}
	return total / float64(len(numbers))
}

func calculateP(numbers []float64, rate float64) float64 {
	if len(numbers) == 0 {
		return 0
	}
	sort.Float64s(numbers)                     // 对切片排序
	index := int(float64(len(numbers)) * 0.99) // 计算 P99 的索引位置
	if index == 0 {
		return numbers[0]
	}
	return numbers[index-1] // 获取 P99 的值
}

func ReportLatencyHistogram(latency []float64) {
	// calc P99 latency

	hist := histogram.Hist(9, latency)

	p99 := time.Duration(calculateP(latency, 0.99) * 1000 / 1000).String()
	p95 := time.Duration(calculateP(latency, 0.95) * 1000 / 1000).String()
	mean := time.Duration(mean(latency) * 1000 / 1000).String()

	fmt.Printf("p99:%v, p95:%v, mean:%v\n", p99, p95, mean)
	err := histogram.Fprintf(os.Stdout, hist, histogram.Linear(5), func(v float64) string {
		_v := math.Round(v*1000) / 1000
		return time.Duration(_v).String()
	})
	if err != nil {
		panic(err)
	}
}

func Req(ctx context.Context, city *gocitiesjson.City) {
	var url string
	if *coordsOrder == "lat,lon" {
		url = fmt.Sprintf(*apiTPL, city.Lat, city.Lng)
	} else {
		url = fmt.Sprintf(*apiTPL, city.Lng, city.Lat)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	duration := float64(time.Since(start).Nanoseconds())

	if err != nil {
		atomic.AddInt64(&reqFailed, 1)
		reqfailedmutex.Lock()
		reqfailedlatency = append(reqfailedlatency, duration)
		reqfailedmutex.Unlock()
		return
	}

	if code := resp.StatusCode; code != 200 {
		atomic.AddInt64(&failed, 1)
		atomic.AddInt64(failedStatusCode[code], 1)
		failedmutex.Lock()
		failedlatency = append(failedlatency, duration)
		failedmutex.Unlock()
		return
	}

	atomic.AddInt64(&ok, 1)
	okmutex.Lock()
	oklatency = append(oklatency, duration)
	okmutex.Unlock()
}

func RunConcPool(rootCtx context.Context) {
	rl := ratelimit.New(*qps)
	p := pool.New().WithMaxGoroutines(*threads).WithContext(rootCtx)
	for i := 0; i < *runs; i++ {
		p.Go(
			func(reqctx context.Context) error {
				rl.Take()
				ctx, cancel := context.WithTimeout(reqctx, time.Duration(*timeout)*time.Second)
				defer cancel()

				city := cities[rand.Intn(len(cities))]

				Req(ctx, city)
				return nil
			})
	}
	p.Wait()
}

func main() {
	start := time.Now()
	prepare()
	ctx := context.Background()
	RunConcPool(ctx)
	fmt.Println("total time", time.Since(start))
	fmt.Println("ok", ok)
	fmt.Println("failed", failed)
	fmt.Println("reqFailed", reqFailed)

	ReportFailedStatus()
	fmt.Println()

	fmt.Println("oklatency:")
	ReportLatencyHistogram(oklatency)
	fmt.Println()

	fmt.Println("failedlatency:")
	ReportLatencyHistogram(failedlatency)
	fmt.Println()

	fmt.Println("reqfailedlatency:")
	ReportLatencyHistogram(reqfailedlatency)
	fmt.Println()

	fmt.Println("======================================")
	fmt.Println()
}
