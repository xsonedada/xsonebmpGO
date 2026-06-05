package security

import (
    "net/http"
    "strings"
    "sync"
    "time"
    
    "github.com/gin-gonic/gin"
)

// RateLimiter защита от брутфорса (без внешних зависимостей)
type RateLimiter struct {
    attempts map[string][]time.Time
    mu       sync.RWMutex
    Limit    int
    Window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
    limiter := &RateLimiter{
        attempts: make(map[string][]time.Time),
        Limit:    limit,
        Window:   window,
    }
    
    // Автоочистка старых записей каждые 5 минут
    go limiter.cleanupLoop()
    
    return limiter
}

func (rl *RateLimiter) cleanupLoop() {
    ticker := time.NewTicker(5 * time.Minute)
    for range ticker.C {
        rl.mu.Lock()
        now := time.Now()
        for ip, times := range rl.attempts {
            var valid []time.Time
            for _, t := range times {
                if now.Sub(t) < rl.Window {
                    valid = append(valid, t)
                }
            }
            if len(valid) == 0 {
                delete(rl.attempts, ip)
            } else {
                rl.attempts[ip] = valid
            }
        }
        rl.mu.Unlock()
    }
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        ip := c.ClientIP()
        
        rl.mu.Lock()
        now := time.Now()
        cutoff := now.Add(-rl.Window)
        
        // Очищаем старые попытки
        var valid []time.Time
        for _, t := range rl.attempts[ip] {
            if t.After(cutoff) {
                valid = append(valid, t)
            }
        }
        
        if len(valid) >= rl.Limit {
            rl.mu.Unlock()
            
            // Логируем попытку атаки
            if SecurityLogger != nil {
                SecurityLogger.Warn("Rate limit exceeded - IP: " + ip)
            }
            
            c.JSON(http.StatusTooManyRequests, gin.H{
                "error": "Слишком много запросов. Попробуйте позже.",
                "retry_after": int(rl.Window.Seconds()),
            })
            c.Abort()
            return
        }
        
        valid = append(valid, now)
        rl.attempts[ip] = valid
        rl.mu.Unlock()
        
        c.Next()
    }
}

// IPBlocker блокировка IP после превышения порога
type IPBlocker struct {
    blockedIPs map[string]time.Time
    attempts   map[string]int
    mu         sync.RWMutex
    Threshold  int
    BlockTime  time.Duration
}

func NewIPBlocker(threshold int, blockTime time.Duration) *IPBlocker {
    blocker := &IPBlocker{
        blockedIPs: make(map[string]time.Time),
        attempts:   make(map[string]int),
        Threshold:  threshold,
        BlockTime:  blockTime,
    }
    
    // Автоочистка заблокированных IP
    go blocker.cleanupLoop()
    
    return blocker
}

func (b *IPBlocker) cleanupLoop() {
    ticker := time.NewTicker(10 * time.Minute)
    for range ticker.C {
        b.mu.Lock()
        now := time.Now()
        for ip, blockedUntil := range b.blockedIPs {
            if now.After(blockedUntil) {
                delete(b.blockedIPs, ip)
                delete(b.attempts, ip)
            }
        }
        b.mu.Unlock()
    }
}

func (b *IPBlocker) IsBlocked(ip string) bool {
    b.mu.RLock()
    defer b.mu.RUnlock()
    
    if blockedUntil, exists := b.blockedIPs[ip]; exists {
        return time.Now().Before(blockedUntil)
    }
    return false
}

func (b *IPBlocker) RecordAttempt(ip string) bool {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    // Проверяем блокировку
    if blockedUntil, exists := b.blockedIPs[ip]; exists {
        if time.Now().Before(blockedUntil) {
            return false
        }
        delete(b.blockedIPs, ip)
        delete(b.attempts, ip)
    }
    
    b.attempts[ip]++
    if b.attempts[ip] >= b.Threshold {
        b.blockedIPs[ip] = time.Now().Add(b.BlockTime)
        if SecurityLogger != nil {
            SecurityLogger.Critical("IP blocked: " + ip)
        }
        return false
    }
    
    return true
}

func (b *IPBlocker) Middleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        ip := c.ClientIP()
        
        if !b.RecordAttempt(ip) {
            c.JSON(http.StatusForbidden, gin.H{
                "error": "Доступ заблокирован из-за подозрительной активности",
                "blocked_until": time.Now().Add(b.BlockTime).Format(time.RFC3339),
            })
            c.Abort()
            return
        }
        
        c.Next()
    }
}

// SecurityHeaders добавляет все защитные заголовки
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		
		// Правильный Content Security Policy
		csp := []string{
			"default-src 'self'",
			"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com https://cdn.jsdelivr.net",
			"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net",
			"font-src 'self' data: https://fonts.gstatic.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net",
			"img-src 'self' data: https: http:",
			"connect-src 'self' https: http:",
			"frame-ancestors 'none'",
			"form-action 'self'",
			"base-uri 'self'",
			"object-src 'none'",
			"style-src-elem 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net",
			"script-src-elem 'self' 'unsafe-inline' https://cdnjs.cloudflare.com https://cdn.jsdelivr.net",
		}
		c.Header("Content-Security-Policy", strings.Join(csp, "; "))
		
		c.Next()
	}
}

// LoggerMiddleware логирует подозрительную активность
func LoggerMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.Request.URL.Path
        method := c.Request.Method
        ip := c.ClientIP()
        
        // Логируем доступ к админке
        if strings.HasPrefix(path, "/admin") && method != "GET" {
            if SecurityLogger != nil {
                SecurityLogger.Info("Admin action - " + method + " " + path + " from " + ip)
            }
        }
        
        c.Next()
        
        // Логируем ошибки
        status := c.Writer.Status()
        if status >= 400 {
            if SecurityLogger != nil {
                SecurityLogger.Warn("HTTP " + string(rune(status)) + " - " + method + " " + path + " from " + ip + 
                    " (" + time.Since(start).String() + ")")
            }
        }
        
        // Логируем подозрительно долгие запросы
        if time.Since(start) > 5*time.Second {
            if SecurityLogger != nil {
                SecurityLogger.Warn("Slow request - " + method + " " + path + " from " + ip + 
                    " (" + time.Since(start).String() + ")")
            }
        }
    }
}

// RequestSanitizer очищает входящие запросы от вредоносных данных
func RequestSanitizer() gin.HandlerFunc {
    return func(c *gin.Context) {
        // Очищаем query параметры
        query := c.Request.URL.Query()
        for key, values := range query {
            for i, value := range values {
                query[key][i] = strings.TrimSpace(value)
            }
        }
        
        // Проверяем заголовки на инъекции
        for key, values := range c.Request.Header {
            for _, value := range values {
                if strings.Contains(strings.ToLower(value), "script") ||
                   strings.Contains(strings.ToLower(value), "alert(") {
                    if SecurityLogger != nil {
                        SecurityLogger.Critical("XSS attempt in headers from " + c.ClientIP() + 
                            " header: " + key)
                    }
                    c.AbortWithStatus(http.StatusBadRequest)
                    return
                }
            }
        }
        
        c.Next()
    }
}

// RecoveryMiddleware восстанавливается после паники и логирует ошибку
func RecoveryMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        defer func() {
            if err := recover(); err != nil {
                if SecurityLogger != nil {
                    SecurityLogger.Critical("Panic recovered: " + 
                        string(err.(string)) + " at " + c.Request.URL.Path)
                }
                
                c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
                    "error": "Внутренняя ошибка сервера",
                })
            }
        }()
        
        c.Next()
    }
}