// healthcheck 用于精简运行镜像，不依赖 shell、curl 或应用凭据。
package main

import (
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	timeout := 2 * time.Second
	if value, err := time.ParseDuration(os.Getenv("DBHUB_READINESS_TIMEOUT_SECONDS") + "s"); err == nil && value > 0 && value <= time.Minute {
		timeout = value + time.Second
	}
	client := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/api/ready")
	if err != nil {
		os.Exit(1)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
