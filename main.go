package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Metrics we want from cadvisor. Everything else is dropped to avoid
// inconsistent-label-set errors in pushgateway.
var cadvisorMetricPrefixes = []string{
	"container_cpu_usage_seconds_total",
	"container_memory_usage_bytes",
	"container_network_receive_bytes_total",
	"container_network_transmit_bytes_total",
}

func main() {
	nodeExporterURL := os.Getenv("NODE_EXPORTER_URL")
	cadvisorURL := os.Getenv("CADVISOR_URL")
	pushgatewayURL := os.Getenv("PUSHGATEWAY_URL")
	instance := os.Getenv("INSTANCE")

	if nodeExporterURL == "" || cadvisorURL == "" || pushgatewayURL == "" || instance == "" {
		log.Fatalf("FATAL: required env vars: NODE_EXPORTER_URL, CADVISOR_URL, PUSHGATEWAY_URL, INSTANCE")
	}

	pushInterval := getEnvInt("PUSH_INTERVAL", 15)

	log.Printf("Metrics collector starting")
	log.Printf("  node-exporter: %s", nodeExporterURL)
	log.Printf("  cadvisor:      %s", cadvisorURL)
	log.Printf("  pushgateway:   %s", pushgatewayURL)
	log.Printf("  instance:      %s", instance)
	log.Printf("  interval:      %ds", pushInterval)

	client := &http.Client{Timeout: 10 * time.Second}

	time.Sleep(10 * time.Second)

	for {
		start := time.Now()

		scrapeAndPush(client, nodeExporterURL, pushgatewayURL, "node", instance, nil)
		scrapeAndPush(client, cadvisorURL, pushgatewayURL, "cadvisor", instance, filterCadvisorMetrics)

		elapsed := time.Since(start)
		sleepTime := time.Duration(pushInterval)*time.Second - elapsed
		if sleepTime > 0 {
			time.Sleep(sleepTime)
		}
	}
}

// filterCadvisorMetrics keeps only the metric families we need from cadvisor,
// drops metrics without a "name" label, and strips timestamps.
// Pushgateway rejects metrics with timestamps — it assigns its own timestamp
// on receive, which is effectively the same as the scrape time (~ms difference).
func filterCadvisorMetrics(body []byte) []byte {
	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "#") {
			for _, prefix := range cadvisorMetricPrefixes {
				if strings.Contains(line, prefix) {
					out.WriteString(line)
					out.WriteByte('\n')
					break
				}
			}
			continue
		}

		for _, prefix := range cadvisorMetricPrefixes {
			if strings.HasPrefix(line, prefix+"{") || strings.HasPrefix(line, prefix+" ") {
				if strings.Contains(line, `name="`) && !strings.Contains(line, `name=""`) {
					out.WriteString(stripTimestamp(line))
					out.WriteByte('\n')
				}
				break
			}
		}
	}

	return out.Bytes()
}

// stripTimestamp removes trailing timestamp from a Prometheus metric line.
// "metric{labels} value timestamp" -> "metric{labels} value"
func stripTimestamp(line string) string {
	lastSpace := strings.LastIndex(line, " ")
	if lastSpace > 0 {
		potential := line[lastSpace+1:]
		if _, err := strconv.ParseInt(potential, 10, 64); err == nil {
			return line[:lastSpace]
		}
	}
	return line
}

func scrapeAndPush(client *http.Client, sourceURL, pushgatewayBase, job, instance string, filter func([]byte) []byte) {
	resp, err := client.Get(sourceURL)
	if err != nil {
		log.Printf("ERROR: failed to scrape %s: %v", sourceURL, err)
		return
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Printf("ERROR: failed to read response from %s: %v", sourceURL, err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("ERROR: %s returned status %d", sourceURL, resp.StatusCode)
		return
	}

	if filter != nil {
		body = filter(body)
	}

	if len(body) == 0 {
		log.Printf("WARN: no metrics to push for %s after filtering", job)
		return
	}

	pushURL := fmt.Sprintf("%s/metrics/job/%s/instance/%s", pushgatewayBase, job, instance)
	req, err := http.NewRequest(http.MethodPut, pushURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("ERROR: failed to create push request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "text/plain; version=0.0.4")

	pushResp, err := client.Do(req)
	if err != nil {
		log.Printf("ERROR: failed to push %s metrics to %s: %v", job, pushURL, err)
		return
	}

	if pushResp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(pushResp.Body)
		pushResp.Body.Close()
		log.Printf("ERROR: pushgateway returned status %d for %s: %s", pushResp.StatusCode, job, strings.TrimSpace(string(respBody)))
		return
	}
	pushResp.Body.Close()

	log.Printf("Pushed %s metrics (%d bytes) to pushgateway", job, len(body))
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
