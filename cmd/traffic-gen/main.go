package main

import (
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"
)

var paths = []struct {
	path   string
	weight int
}{{"/api/users", 5}, {"/api/orders", 3}, {"/api/checkout", 2}}

func pick() string {
	n := rand.Intn(10)
	for _, p := range paths {
		if n < p.weight {
			return p.path
		}
		n -= p.weight
	}
	return paths[0].path
}

// Request rate follows a sine wave so the metrics have visible peaks and valleys.
func rate() float64 {
	phase := float64(time.Now().Unix()%600) / 600 * 2 * math.Pi
	return 8 + 7*math.Sin(phase) // 1..15 req/s
}

func main() {
	target := os.Getenv("TARGET_URL")
	if target == "" {
		target = "http://localhost:8080"
	}
	log.Println("sending traffic to", target)
	for {
		go func(p string) {
			resp, err := http.Get(target + p)
			if err != nil {
				log.Println("request failed:", err)
				return
			}
			resp.Body.Close()
		}(pick())
		time.Sleep(time.Duration(float64(time.Second) / rate()))
	}
}
