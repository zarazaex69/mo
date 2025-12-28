package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"
)

// ansi colors
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
)

type Model struct {
	ID string `json:"id"`
}

type ModelsResponse struct {
	Data []Model `json:"data"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type BenchResult struct {
	Model        string
	Duration     time.Duration
	Tokens       int
	TokensPerSec float64
	Error        error
}

type ModelStats struct {
	Model       string
	Runs        int
	AvgDuration time.Duration
	MinDuration time.Duration
	MaxDuration time.Duration
	AvgTokens   float64
	AvgTPS      float64
	Errors      int
}

var (
	baseURL = flag.String("url", "http://localhost:8804", "API base URL")
	runs    = flag.Int("runs", 6, "number of runs per model")
	prompt  = flag.String("prompt", "напиши короткую историю в 50 слов", "test prompt")
)

var httpClient *http.Client

func init() {
	transport := &http.Transport{}

	// check ALL_PROXY env
	if proxy := os.Getenv("ALL_PROXY"); proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	httpClient = &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
}

func main() {
	flag.Parse()

	fmt.Printf("%smo-bench%s\n", bold, reset)
	fmt.Printf("  url:  %s\n", *baseURL)
	fmt.Printf("  runs: %d\n", *runs)
	fmt.Println()

	models, err := getModels(*baseURL)
	if err != nil {
		fmt.Printf("%serror:%s %v\n", red, reset, err)
		return
	}

	fmt.Printf("found %d models, running benchmarks...\n\n", len(models))

	statsChan := make(chan ModelStats, len(models))
	var wg sync.WaitGroup

	for _, model := range models {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			stats := benchmarkModel(*baseURL, m, *runs, *prompt)
			statsChan <- stats
		}(model)
	}

	go func() {
		wg.Wait()
		close(statsChan)
	}()

	allStats := make([]ModelStats, 0, len(models))
	for stats := range statsChan {
		allStats = append(allStats, stats)
	}

	printResults(allStats)
}

func getModels(baseURL string) ([]string, error) {
	resp, err := httpClient.Get(baseURL + "/v1/models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

func benchmarkModel(baseURL, model string, runs int, prompt string) ModelStats {
	var durations []time.Duration
	var tokens []int
	var tps []float64
	errors := 0

	// run requests sequentially
	for i := 0; i < runs; i++ {
		r := runSingleBench(baseURL, model, prompt)
		if r.Error != nil {
			errors++
			continue
		}
		durations = append(durations, r.Duration)
		tokens = append(tokens, r.Tokens)
		tps = append(tps, r.TokensPerSec)
	}

	stats := ModelStats{
		Model:  model,
		Runs:   runs,
		Errors: errors,
	}

	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

		var totalDur time.Duration
		var totalTokens, totalTPS float64
		for i := range durations {
			totalDur += durations[i]
			totalTokens += float64(tokens[i])
			totalTPS += tps[i]
		}

		n := len(durations)
		stats.AvgDuration = totalDur / time.Duration(n)
		stats.MinDuration = durations[0]
		stats.MaxDuration = durations[n-1]
		stats.AvgTokens = totalTokens / float64(n)
		stats.AvgTPS = totalTPS / float64(n)
	}

	return stats
}

func runSingleBench(baseURL, model, prompt string) BenchResult {
	req := ChatRequest{
		Model:  model,
		Stream: false,
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
	}

	body, _ := json.Marshal(req)

	start := time.Now()
	resp, err := httpClient.Post(
		baseURL+"/v1/chat/completions",
		"application/json",
		bytes.NewReader(body),
	)
	duration := time.Since(start)

	if err != nil {
		return BenchResult{Model: model, Error: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return BenchResult{
			Model: model,
			Error: fmt.Errorf("status %d: %s", resp.StatusCode, string(bodyBytes)),
		}
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return BenchResult{Model: model, Error: err}
	}

	tokens := chatResp.Usage.CompletionTokens
	tps := float64(tokens) / duration.Seconds()

	return BenchResult{
		Model:        model,
		Duration:     duration,
		Tokens:       tokens,
		TokensPerSec: tps,
	}
}

func printResults(stats []ModelStats) {
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].AvgTPS > stats[j].AvgTPS
	})

	fmt.Printf("%s%-20s %10s %10s %10s %10s %6s%s\n",
		bold, "MODEL", "AVG", "MIN", "MAX", "TOK/S", "ERR", reset)
	fmt.Println("----------------------------------------------------------------------")

	for _, s := range stats {
		color := green
		if s.Errors > 0 {
			color = yellow
		}
		if s.Errors == s.Runs {
			color = red
		}

		if s.Errors == s.Runs {
			fmt.Printf("%s%-20s %10s %10s %10s %10s %6d%s\n",
				color, truncate(s.Model, 20), "-", "-", "-", "-", s.Errors, reset)
		} else {
			fmt.Printf("%s%-20s %10v %10v %10v %10.1f %6d%s\n",
				color,
				truncate(s.Model, 20),
				s.AvgDuration.Round(time.Millisecond),
				s.MinDuration.Round(time.Millisecond),
				s.MaxDuration.Round(time.Millisecond),
				s.AvgTPS,
				s.Errors,
				reset)
		}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
