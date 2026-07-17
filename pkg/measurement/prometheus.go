// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package measurement

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

// PrometheusRemoteWriter writes metrics to Prometheus via Remote Write API
type PrometheusRemoteWriter struct {
	prometheusURL string
	benchNo       string
	randomNum     string
	client        *http.Client
}

// NewPrometheusRemoteWriter creates a new Prometheus remote writer
func NewPrometheusRemoteWriter(prometheusURL, benchNo string) *PrometheusRemoteWriter {
	return &PrometheusRemoteWriter{
		prometheusURL: strings.TrimSuffix(prometheusURL, "/"),
		benchNo:       benchNo,
		randomNum:     generateRandomNum(),
		client:        &http.Client{Timeout: 30 * time.Second},
	}
}

// generateRandomNum generates a random 8-digit number
func generateRandomNum() string {
	rand.Seed(time.Now().UnixNano())
	return strconv.Itoa(10000000 + rand.Intn(90000000))
}

// WriteMetrics writes all metrics to Prometheus via Remote Write API
func (w *PrometheusRemoteWriter) WriteMetrics() error {
	if globalMeasure == nil || globalMeasure.measurer == nil {
		return fmt.Errorf("measurement not initialized")
	}

	// Get metrics data from histograms
	hists, ok := globalMeasure.measurer.(*histograms)
	if !ok {
		return fmt.Errorf("prometheus remote write only supports histogram measurement type")
	}

	// Check if this is workloadf scenario
	// For workloadf, we skip READ_MODIFY_WRITE in individual metrics and total calculation
	// We detect workloadf by checking if readmodifywriteproportion is configured (> 0)
	isWorkloadF := false
	if globalMeasure.p != nil {
		rmwProp := globalMeasure.p.GetFloat64("readmodifywriteproportion", 0)
		isWorkloadF = rmwProp > 0
	}

	timeseries := w.buildTimeSeries(hists, isWorkloadF)
	if len(timeseries) == 0 {
		return fmt.Errorf("no metrics to write")
	}

	return w.remoteWrite(timeseries)
}

func (w *PrometheusRemoteWriter) buildTimeSeries(h *histograms, skipReadModifyWrite bool) []prompb.TimeSeries {
	var timeseries []prompb.TimeSeries
	now := time.Now().UnixMilli()

	// Check if we should skip READ_MODIFY_WRITE (for workloadf-like scenarios)
	isWorkloadF := skipReadModifyWrite

	// Get all operations
	operations := make([]string, 0, len(h.histograms))
	for op := range h.histograms {
		// Skip READ_MODIFY_WRITE for workloadf
		if isWorkloadF && op == "READ_MODIFY_WRITE" {
			continue
		}
		operations = append(operations, op)
	}
	sort.Strings(operations)

	// Cache info for each operation to ensure consistency
	// (getInfo() calculates QPS based on current time, so we need to cache it)
	infoCache := make(map[string]map[string]interface{})
	calculatedTotalOps := float64(0)
	for _, op := range operations {
		hist := h.histograms[op]
		info := hist.getInfo()
		infoCache[op] = info
		if op != "TOTAL" {
			calculatedTotalOps += info[QPS].(float64)
		}
	}

	// Helper function to create a time series (moved outside loop)
	createSeries := func(metricName string, value float64, opLabel string) prompb.TimeSeries {
		return prompb.TimeSeries{
			Labels: []prompb.Label{
				{Name: "__name__", Value: metricName},
				{Name: "bench_no", Value: w.benchNo},
				{Name: "operation", Value: opLabel},
			},
			Samples: []prompb.Sample{
				{Timestamp: now, Value: value},
			},
		}
	}

	// Process each operation
	for _, op := range operations {
		info := infoCache[op]

		// Determine metric prefix based on operation type
		// Format: ycsb_<bench_no>_<random_num>_<oper>_xxx
		// Replace hyphens with underscores in benchNo for Prometheus compatibility
		benchNoSafe := strings.ReplaceAll(w.benchNo, "-", "_")
		var metricPrefix string
		if op == "TOTAL" {
			metricPrefix = fmt.Sprintf("ycsb_%s_%s_all", benchNoSafe, w.randomNum)
		} else {
			opLower := strings.ToLower(op)
			metricPrefix = fmt.Sprintf("ycsb_%s_%s_%s", benchNoSafe, w.randomNum, opLower)
		}

		// Build time series for this operation
		// Note: Latency metrics (avg, min, max, p90-p9999) are converted from microseconds to milliseconds
		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_total_time", metricPrefix),
			info[ELAPSED].(float64),
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_oper_count", metricPrefix),
			float64(info[COUNT].(int64)),
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_ops", metricPrefix),
			info[QPS].(float64),
			op,
		))

		// Latency metrics: convert from microseconds to milliseconds
		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_avg", metricPrefix),
			float64(info[AVG].(int64))/1000.0,
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_min", metricPrefix),
			float64(info[MIN].(int64))/1000.0,
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_max", metricPrefix),
			float64(info[MAX].(int64))/1000.0,
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_p90", metricPrefix),
			float64(info[PER90TH].(int64))/1000.0,
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_p95", metricPrefix),
			float64(info[PER95TH].(int64))/1000.0,
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_p99", metricPrefix),
			float64(info[PER99TH].(int64))/1000.0,
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_p999", metricPrefix),
			float64(info[PER999TH].(int64))/1000.0,
			op,
		))

		timeseries = append(timeseries, createSeries(
			fmt.Sprintf("%s_p9999", metricPrefix),
			float64(info[PER9999TH].(int64))/1000.0,
			op,
		))
	}

	// Add calculated_total_ops metric: sum of OPS from all non-TOTAL operations
	// This is useful for validating: READ_OPS + UPDATE_OPS + ... = calculated_total_ops
	benchNoSafe := strings.ReplaceAll(w.benchNo, "-", "_")
	calculatedMetricName := fmt.Sprintf("ycsb_%s_%s_calculated_total_ops", benchNoSafe, w.randomNum)
	timeseries = append(timeseries, createSeries(
		calculatedMetricName,
		calculatedTotalOps,
		"CALCULATED",
	))

	return timeseries
}

func (w *PrometheusRemoteWriter) remoteWrite(timeseries []prompb.TimeSeries) error {
	// Build WriteRequest
	req := &prompb.WriteRequest{
		Timeseries: timeseries,
	}

	// Marshal to protobuf
	data, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal write request: %v", err)
	}

	// Compress with snappy
	compressed := snappy.Encode(nil, data)

	// Send HTTP POST request
	url := fmt.Sprintf("%s/api/v1/write", w.prometheusURL)
	
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	resp, err := w.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send remote write request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote write returned status %d (expected 204)", resp.StatusCode)
	}

	return nil
}

// QueryAndAggregate queries metrics from Prometheus and aggregates them by bench-no
// It exports raw metrics and aggregated statistics to a CSV file
// Filename format: ycsb-<bench-no>-<timestamp>.csv
func (w *PrometheusRemoteWriter) QueryAndAggregate() (string, error) {
	// Sanitize benchNo for metric name matching (replace hyphens with underscores)
	benchNoSafe := strings.ReplaceAll(w.benchNo, "-", "_")
	
	// Query all metrics for this bench-no
	metrics, err := w.queryMetrics(benchNoSafe)
	if err != nil {
		return "", fmt.Errorf("failed to query metrics: %v", err)
	}

	if len(metrics) == 0 {
		return "", fmt.Errorf("no metrics found for bench-no: %s", w.benchNo)
	}

	// Parse and aggregate metrics
	aggregated := w.aggregateMetrics(metrics, benchNoSafe)

	// Generate CSV filename
	timestamp := time.Now().Format("20060102-150405")
	csvFileName := fmt.Sprintf("ycsb-%s-%s.csv", w.benchNo, timestamp)

	// Write to CSV
	if err := w.writeCSV(csvFileName, metrics, aggregated); err != nil {
		return "", fmt.Errorf("failed to write CSV: %v", err)
	}

	return csvFileName, nil
}

// queryMetrics queries all metrics from Prometheus for the given bench-no
func (w *PrometheusRemoteWriter) queryMetrics(benchNoSafe string) (map[string]float64, error) {
	// Use a single instant query with regex matcher to fetch all metrics at once.
	// This reduces N+1 HTTP requests to a single request, significantly improving
	// --statistic query performance.
	query := fmt.Sprintf("{__name__=~\"ycsb_%s_.+\"}", benchNoSafe)
	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s", w.prometheusURL, url.QueryEscape(query))

	resp, err := w.client.Get(queryURL)
	if err != nil {
		return nil, fmt.Errorf("failed to query Prometheus: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus returned status %d", resp.StatusCode)
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	metrics := make(map[string]float64)
	for _, r := range result.Data.Result {
		name := r.Metric["__name__"]
		if name == "" {
			continue
		}
		if len(r.Value) >= 2 {
			if valueStr, ok := r.Value[1].(string); ok {
				if value, err := strconv.ParseFloat(valueStr, 64); err == nil && value > 0 {
					metrics[name] = value
				}
			}
		}
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("no metrics found for bench-no: %s", w.benchNo)
	}

	return metrics, nil
}

// queryMetricValue queries a specific metric value from Prometheus
func (w *PrometheusRemoteWriter) queryMetricValue(metricName string) (float64, error) {
	url := fmt.Sprintf("%s/api/v1/query?query=%s", w.prometheusURL, metricName)
	
	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := w.client.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	if len(result.Data.Result) == 0 {
		return 0, fmt.Errorf("no data")
	}

	// Extract value from the result
	if len(result.Data.Result[0].Value) >= 2 {
		valueStr, ok := result.Data.Result[0].Value[1].(string)
		if ok {
			return strconv.ParseFloat(valueStr, 64)
		}
	}

	return 0, fmt.Errorf("invalid value format")
}

// MetricInfo holds parsed metric information
type MetricInfo struct {
	Name       string
	BenchNo    string
	RandomNum  string
	Oper       string
	Metric     string
	Value      float64
}

// aggregateMetrics aggregates metrics by bench-no dimension
// Sum for count/time/ops metrics, Min for min metrics, Max for max metrics, Avg for other latency metrics
func (w *PrometheusRemoteWriter) aggregateMetrics(metrics map[string]float64, benchNoSafe string) map[string]float64 {
	aggregated := make(map[string]float64)
	counts := make(map[string]int) // for calculating average
	minValues := make(map[string]float64) // track min values
	maxValues := make(map[string]float64) // track max values

	// Regex to parse metric name: ycsb_<bench-no>_<random>_<oper>_<metric>
	// Note: bench-no may contain underscores (converted from hyphens)
	pattern := fmt.Sprintf(`^ycsb_%s_(\d+)_(\w+)_(\w+)$`, regexp.QuoteMeta(benchNoSafe))
	re := regexp.MustCompile(pattern)

	for metricName, value := range metrics {
		matches := re.FindStringSubmatch(metricName)
		if len(matches) != 4 {
			continue
		}

		randomNum := matches[1]
		oper := matches[2]
		metric := matches[3]

		// Skip calculated_total metrics in aggregation
		if oper == "calculated" {
			continue
		}

		// Create aggregation key: <oper>_<metric>
		aggKey := fmt.Sprintf("%s_%s", oper, metric)

		// Determine aggregation method
		switch metric {
		case "oper_count", "total_time", "ops":
			// Sum for count, time, and OPS metrics
			aggregated[aggKey] += value
		case "min":
			// Min for min metrics: take the minimum value across all instances
			if currentMin, exists := minValues[aggKey]; !exists || value < currentMin {
				minValues[aggKey] = value
			}
			aggregated[aggKey] = minValues[aggKey]
		case "max":
			// Max for max metrics: take the maximum value across all instances
			if currentMax, exists := maxValues[aggKey]; !exists || value > currentMax {
				maxValues[aggKey] = value
			}
			aggregated[aggKey] = maxValues[aggKey]
		case "avg", "p90", "p95", "p99", "p999", "p9999":
			// Average for other latency metrics (weighted by instance count)
			aggregated[aggKey] += value
			counts[aggKey]++
		}

		_ = randomNum // randomNum is used for identifying instances but not in aggregation
	}

	// Calculate averages for latency metrics (excluding min/max)
	for key, count := range counts {
		if count > 0 {
			aggregated[key] = aggregated[key] / float64(count)
		}
	}

	return aggregated
}

// writeCSV writes raw metrics and aggregated statistics to a CSV file
func (w *PrometheusRemoteWriter) writeCSV(filename string, metrics map[string]float64, aggregated map[string]float64) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	writer.Write([]string{"Type", "Metric Name", "Value"})

	// Write section header for raw metrics
	writer.Write([]string{"RAW METRICS", "", ""})

	// Sort metric names for consistent output
	var metricNames []string
	for name := range metrics {
		metricNames = append(metricNames, name)
	}
	sort.Strings(metricNames)

	// Write raw metrics
	for _, name := range metricNames {
		writer.Write([]string{"raw", name, fmt.Sprintf("%.6f", metrics[name])})
	}

	// Write empty line as separator
	writer.Write([]string{"", "", ""})

	// Write section header for aggregated metrics
	writer.Write([]string{"AGGREGATED METRICS", "", ""})

	// Sort aggregated keys
	var aggKeys []string
	for key := range aggregated {
		aggKeys = append(aggKeys, key)
	}
	sort.Strings(aggKeys)

	// Write aggregated metrics
	for _, key := range aggKeys {
		writer.Write([]string{"aggregated", key, fmt.Sprintf("%.6f", aggregated[key])})
	}

	return nil
}

// PushToPrometheus is a convenience function to write metrics (alias for Remote Write)
func PushToPrometheus(prometheusURL, benchNo string) error {
	writer := NewPrometheusRemoteWriter(prometheusURL, benchNo)
	return writer.WriteMetrics()
}

// QueryAndAggregateMetrics queries metrics from Prometheus and aggregates them by bench-no
// It exports raw metrics and aggregated statistics to a CSV file
func QueryAndAggregateMetrics(prometheusURL, benchNo string) (string, error) {
	writer := NewPrometheusRemoteWriter(prometheusURL, benchNo)
	return writer.QueryAndAggregate()
}

// QueryOnly only queries and aggregates metrics from Prometheus without writing
// This is used for --statistic mode to avoid generating new metrics
func QueryOnly(prometheusURL, benchNo string) (string, error) {
	// Create a minimal writer without random number generation
	writer := &PrometheusRemoteWriter{
		prometheusURL: strings.TrimSuffix(prometheusURL, "/"),
		benchNo:       benchNo,
		randomNum:     "", // No random number needed for query only
		client:        &http.Client{Timeout: 30 * time.Second},
	}
	return writer.QueryAndAggregate()
}
