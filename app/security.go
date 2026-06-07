package main

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"nexus-boost/database"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

func checkRevokedSession(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	sessionUUID, ok := session.Values["session_uuid"].(string)
	if !ok || sessionUUID == "" {
		c.Next()
		return
	}

	var revoked bool
	err := database.DB.QueryRow("SELECT revoked FROM user_sessions WHERE session_token = $1", sessionUUID).Scan(&revoked)
	if err == nil && revoked {
		session.Values = make(map[interface{}]interface{})
		session.Save(c.Request, c.Writer)
		c.Redirect(302, "/login")
		c.Abort()
		return
	}
	c.Next()
}

func isMaintenanceMode() bool {
	if mode := getCachedSetting("maintenance_mode"); mode != "" {
		return mode == "true"
	}
	var val string
	err := database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_mode'").Scan(&val)
	if err != nil {
		return false
	}
	return val == "true"
}

func getMaintenanceMessage() string {
	if msg := getCachedSetting("maintenance_message"); msg != "" {
		return msg
	}
	var msg string
	err := database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_message'").Scan(&msg)
	if err != nil || msg == "" {
		return "Технические работы. Скоро вернемся!"
	}
	return msg
}

func maintenanceMiddleware(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, "/static/") ||
		strings.HasPrefix(c.Request.URL.Path, "/admin") ||
		strings.HasPrefix(c.Request.URL.Path, "/api/admin") ||
		c.Request.URL.Path == "/maintenance" {
		c.Next()
		return
	}

	session, _ := store.Get(c.Request, "xsonebmp-session")
	isAdmin := false
	if _, ok := session.Values["admin_id"]; ok {
		isAdmin = true
	}
	if userID, ok := session.Values["user_id"]; ok && userID == 1 {
		isAdmin = true
	}
	if isAdmin {
		c.Next()
		return
	}

	mode := getCachedSetting("maintenance_mode")
	if mode == "" {
		mode = "false"
	}
	if mode == "true" {
		message := getCachedSetting("maintenance_message")
		if message == "" {
			message = "Технические работы. Скоро вернемся!"
		}
		reason := getCachedSetting("maintenance_reason")
		endTimeStr := getCachedSetting("maintenance_end_time")

		c.HTML(http.StatusServiceUnavailable, "layout.html", gin.H{
			"Active":             "maintenance",
			"Message":            message,
			"Reason":             reason,
			"MaintenanceEndTime": endTimeStr,
			"User":               nil,
		})
		c.Abort()
		return
	}
	c.Next()
}

func ensureUserCSRF(c *gin.Context) string {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	token, ok := session.Values["csrf_token"].(string)
	if !ok || token == "" {
		token = randomString(32)
		session.Values["csrf_token"] = token
		session.Save(c.Request, c.Writer)
	}
	return token
}

func userCSRFMiddleware(c *gin.Context) {
	method := c.Request.Method
	if method != http.MethodPost && method != http.MethodPut &&
		method != http.MethodDelete && method != http.MethodPatch {
		c.Next()
		return
	}

	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/admin") {
		c.Next()
		return
	}

	exemptPrefixes := []string{
		"/qr/scanned",
		"/qr/reject",
		"/qr/confirm",
	}
	for _, prefix := range exemptPrefixes {
		if strings.HasPrefix(path, prefix) {
			c.Next()
			return
		}
	}

	if !validateUserCSRF(c) {
		if strings.HasPrefix(path, "/api/") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "CSRF token invalid",
				"message": "Ошибка безопасности. Обновите страницу.",
			})
			return
		}
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.Next()
}

func ensureAdminCSRF(c *gin.Context) string {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	token, ok := session.Values["admin_csrf"].(string)
	if !ok || token == "" {
		token = randomString(32)
		session.Values["admin_csrf"] = token
		session.Save(c.Request, c.Writer)
	}
	return token
}

func validateAdminCSRF(c *gin.Context) bool {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	expected, ok := session.Values["admin_csrf"].(string)
	if !ok || expected == "" {
		return false
	}
	token := c.PostForm("csrf_token")
	if token == "" {
		token = c.GetHeader("X-CSRF-Token")
	}
	return token != "" && token == expected
}

func adminCSRFMiddleware(c *gin.Context) {
	if c.Request.Method == "POST" {
		if !validateAdminCSRF(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF token invalid"})
			return
		}
	}
	ensureAdminCSRF(c)
	c.Next()
}

func validateUserCSRF(c *gin.Context) bool {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	expected, ok := session.Values["csrf_token"].(string)
	if !ok || expected == "" {
		return false
	}
	token := c.PostForm("csrf_token")
	if token == "" {
		token = c.GetHeader("X-CSRF-Token")
	}
	return token != "" && token == expected
}

func rateLimitIP(c *gin.Context, attempts map[string][]time.Time, mu *sync.Mutex, limit int, window time.Duration) bool {
	ip := c.ClientIP()
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	var valid []time.Time
	for _, t := range attempts[ip] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= limit {
		return false
	}
	valid = append(valid, now)
	attempts[ip] = valid
	return true
}

func secureRandomCode6() string {
	var n uint32
	if err := binary.Read(cryptorand.Reader, binary.BigEndian, &n); err != nil {
		log.Printf("secureRandomCode6: crypto/rand failed: %v", err)
		return randomString(8)
	}
	return fmt.Sprintf("%06d", n%1000000)
}

func parseQRToken(raw string) (token, signature string, ok bool) {
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func verifyQRSignature(token, signature string) bool {
	mac := hmac.New(sha256.New, []byte(qrSecret))
	mac.Write([]byte(token))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expectedSig))
}

func orderParticipant(userID int, orderID string) bool {
	var buyerID, boosterID int
	err := database.DB.QueryRow(
		"SELECT user_id, booster_id FROM orders WHERE id = $1", orderID,
	).Scan(&buyerID, &boosterID)
	if err != nil {
		return false
	}
	return userID == buyerID || userID == boosterID
}

func disputeParticipant(userID int, disputeID string) bool {
	var buyerID, sellerID int
	err := database.DB.QueryRow(`
		SELECT o.user_id, o.booster_id FROM disputes d
		JOIN orders o ON o.id = d.order_id
		WHERE d.id = $1`, disputeID,
	).Scan(&buyerID, &sellerID)
	if err != nil {
		return false
	}
	return userID == buyerID || userID == sellerID
}
