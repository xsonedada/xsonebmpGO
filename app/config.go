package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Секреты — загружаются из .env при старте, не хранятся в коде
var (
	vapidPublicKey  string
	vapidPrivateKey string
	adminUsername   string
	adminPassHash   string
	sessionSecret   string
	qrSecret        string
	startingBalance float64
	cookieSecure    bool
)

var (
	adminLoginAttempts = make(map[string][]time.Time)
	adminLoginMu       sync.Mutex
	resetAttempts      = make(map[string][]time.Time)
	resetMu            sync.Mutex
)

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("обязательная переменная окружения не задана: %s", key)
	}
	return v
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	return v == "1" || v == "true" || v == "yes"
}
