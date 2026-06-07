package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
)

func qrScanned(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if err := c.BindJSON(&req); err != nil || req.Token == "" {
		c.JSON(400, gin.H{"error": "Неверный токен"})
		return
	}
	token, sig, ok := parseQRToken(req.Token)
	if !ok || !verifyQRSignature(token, sig) {
		c.JSON(400, gin.H{"error": "Недействительный токен"})
		return
	}
	res, err := database.DB.Exec(
		"UPDATE qr_sessions SET scanned = true WHERE token = $1 AND expires_at > NOW() AND used = false",
		token,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "Ошибка сервера"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(404, gin.H{"error": "Сессия не найдена"})
		return
	}
	c.JSON(200, gin.H{"success": true})
}

func qrGenerate(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}

	// Удаляем старые сессии
	database.DB.Exec("DELETE FROM qr_sessions WHERE user_id = $1", userID)

	token := randomString(32)
	expires := time.Now().Add(5 * time.Minute)

	// Создаём подпись: HMAC-SHA256(token, secret)
	mac := hmac.New(sha256.New, []byte(qrSecret))
	mac.Write([]byte(token))
	signature := hex.EncodeToString(mac.Sum(nil))

	database.DB.Exec(
		"INSERT INTO qr_sessions (token, user_id, ip, user_agent, expires_at, signature) VALUES ($1, $2, $3, $4, $5, $6)",
		token, userID.(int), c.ClientIP(), c.Request.UserAgent(), expires, signature,
	)

	// В QR-код передаём токен и подпись
	qrData := fmt.Sprintf("%s.%s", token, signature)
	c.JSON(200, gin.H{"token": qrData})
}

func qrCheckStatus(c *gin.Context) {
	token := c.Param("token")

	var confirmed, approved, scanned bool
	err := database.DB.QueryRow(
		"SELECT confirmed, COALESCE(approved, false), COALESCE(scanned, false) FROM qr_sessions WHERE token = $1 AND expires_at > NOW()",
		token,
	).Scan(&confirmed, &approved, &scanned)

	if err != nil {
		c.JSON(404, gin.H{"status": "expired"})
		return
	}

	if confirmed {
		c.JSON(200, gin.H{"status": "confirmed"})
	} else if approved {
		c.JSON(200, gin.H{"status": "approved"})
	} else if scanned {
		c.JSON(200, gin.H{"status": "scanned"}) // ← телефон отсканировал, ждёт подтверждения
	} else {
		c.JSON(200, gin.H{"status": "waiting"}) // ← QR сгенерирован, никто ещё не сканировал
	}
}

func qrConfirm(c *gin.Context) {
	var req struct {
		Token  string `json:"token"`
		Action string `json:"action"`
	}
	c.BindJSON(&req)

	if req.Token == "" {
		c.JSON(400, gin.H{"error": "Токен обязателен"})
		return
	}

	// Для approve (компьютер) — только владелец QR-сессии
	if req.Action == "approve" {
		token, sig, ok := parseQRToken(req.Token)
		if !ok || !verifyQRSignature(token, sig) {
			c.JSON(400, gin.H{"error": "Недействительный токен"})
			return
		}
		session, _ := store.Get(c.Request, "xsonebmp-session")
		sessionUserID, ok := session.Values["user_id"].(int)
		if !ok {
			c.JSON(403, gin.H{"error": "Требуется авторизация"})
			return
		}
		var qrUserID int
		err := database.DB.QueryRow(
			"SELECT user_id FROM qr_sessions WHERE token = $1 AND expires_at > NOW() AND used = false",
			token,
		).Scan(&qrUserID)
		if err != nil || qrUserID != sessionUserID {
			c.JSON(403, gin.H{"error": "Нет доступа"})
			return
		}
		_, err = database.DB.Exec("UPDATE qr_sessions SET approved = true WHERE token = $1", token)
		if err != nil {
			c.JSON(500, gin.H{"error": "Ошибка сервера"})
			return
		}
		c.JSON(200, gin.H{"success": true})
		return
	}

	// === LOGIN (телефон) ===
	token, signature, ok := parseQRToken(req.Token)
	if !ok {
		c.JSON(400, gin.H{"error": "Неверный формат токена"})
		return
	}

	if !verifyQRSignature(token, signature) {
		c.JSON(400, gin.H{"error": "Недействительный токен"})
		return
	}

	// Ищем сессию
	var userID int
	var username string
	var approved bool
	err := database.DB.QueryRow(
		`SELECT u.id, u.username, COALESCE(q.approved, false) FROM qr_sessions q 
         JOIN users u ON q.user_id = u.id 
         WHERE q.token = $1 AND q.expires_at > NOW() AND q.used = false`,
		token,
	).Scan(&userID, &username, &approved)

	if err != nil {
		c.JSON(404, gin.H{"error": "QR-код истёк"})
		return
	}

	// Отмечаем, что код был отсканирован
	database.DB.Exec("UPDATE qr_sessions SET scanned = true WHERE token = $1", token)

	if !approved {
		c.JSON(200, gin.H{"error": "Ожидание подтверждения", "waiting": true})
		return
	}

	// Вход разрешён
	database.DB.Exec(
		"UPDATE qr_sessions SET confirmed = true, used = true, confirmed_at = NOW() WHERE token = $1",
		token,
	)

	// Создаём сессию для телефона
	session, _ := store.Get(c.Request, "xsonebmp-session")
	session.Values["user_id"] = userID
	session.Values["username"] = username
	session.Values["session_uuid"] = randomString(32)
	session.Save(c.Request, c.Writer)

	c.JSON(200, gin.H{"success": true, "redirect": "/profile"})
}

func qrReject(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	c.BindJSON(&req)

	// Удаляем токен
	database.DB.Exec(
		"DELETE FROM qr_sessions WHERE token = $1 AND user_id = $2",
		req.Token, userID,
	)

	c.JSON(200, gin.H{"success": true})
}
