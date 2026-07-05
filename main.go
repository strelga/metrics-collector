package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

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

	// Wait for exporters to become available
	time.Sleep(10 * time.Second)

	for {
		start := time.Now()

		scrapeAndPush(client, nodeExporterURL, pushgatewayURL, "node", instance)
		scrapeAndPush(client, cadvisorURL, pushgatewayURL, "cadvisor", instance)

		elapsed := time.Since(start)
		sleepTime := time.Duration(pushInterval)*time.Second - elapsed
		if sleepTime > 0 {
			time.Sleep(sleepTime)
		}
	}
}

func scrapeAndPush(client *http.Client, sourceURL, pushgatewayBase, job, instance string) {
	// Scrape metrics from exporter
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

	// Push metrics to pushgateway via PUT
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
	pushResp.Body.Close()

	if pushResp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(pushResp.Body)
		pushResp.Body.Close()
		log.Printf("ERROR: pushgateway returned status %d for %s: %s", pushResp.StatusCode, job, string(respBody))
		return
	}

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
