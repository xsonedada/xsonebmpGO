package main

import (
	"fmt"
	"io"
	"net/http"
	"nexus-boost/database"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func adminUpdateSettings(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil || user.(*User).ID != 1 {
		c.JSON(403, gin.H{"error": "Доступ запрещён"})
		return
	}

	var updates map[string]string
	if err := c.BindJSON(&updates); err != nil {
		c.JSON(400, gin.H{"error": "Неверный JSON"})
		return
	}

	// Валидация
	for key, newValue := range updates {
		switch key {
		case "platform_fee":
			fee, err := strconv.ParseFloat(newValue, 64)
			if err != nil || fee < 0 || fee > 50 {
				c.JSON(400, gin.H{"error": "Комиссия должна быть от 0 до 50%"})
				return
			}
		case "min_withdraw":
			min, err := strconv.ParseFloat(newValue, 64)
			if err != nil || min < 10 {
				c.JSON(400, gin.H{"error": "Минимальный вывод от 10"})
				return
			}
		case "referral_percent", "referral_level2":
			val, err := strconv.ParseFloat(newValue, 64)
			if err != nil || val < 0 || val > 50 {
				c.JSON(400, gin.H{"error": "Процент рефералки от 0 до 50"})
				return
			}
		}
	}

	// Обновление с логированием
	for key, newValue := range updates {
		var oldValue string
		database.DB.QueryRow("SELECT value FROM settings WHERE key = $1", key).Scan(&oldValue)
		if oldValue != newValue {
			database.DB.Exec(`INSERT INTO settings_history (setting_key, old_value, new_value, changed_by, changed_at) 
                VALUES ($1, $2, $3, $4, NOW())`, key, oldValue, newValue, user.(*User).ID)
		}
		database.DB.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = $2`, key, newValue)
	}

	// Обновляем кэш (если используете)
	go loadSettingsToCache()

	c.JSON(200, gin.H{"success": true})
}

// Верификация продавца

func adminSettingsPage(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil {
		c.Redirect(302, "/admin/login")
		return
	}
	u := user.(*User)
	if u.ID != 1 {
		c.Redirect(302, "/")
		return
	}

	settings := loadAllSettings()
	layoutHTML(c, http.StatusOK, gin.H{
		"User":     user,
		"Active":   "admin-settings",
		"Settings": settings,
	})
}

func adminUploadLogo(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)

	file, err := c.FormFile("logo")
	if err != nil {
		c.JSON(400, gin.H{"error": "Файл не загружен"})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		c.JSON(400, gin.H{"error": "Разрешены только PNG, JPG, JPEG"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(400, gin.H{"error": "Не удалось прочитать файл"})
		return
	}
	defer src.Close()
	header := make([]byte, 8)
	if _, err := io.ReadFull(src, header); err != nil {
		c.JSON(400, gin.H{"error": "Некорректный файл"})
		return
	}
	if !isValidImageHeader(header) {
		c.JSON(400, gin.H{"error": "Файл не является изображением"})
		return
	}

	filename := fmt.Sprintf("logo_%d%s", time.Now().Unix(), ext)
	dst := "./static/" + filename
	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(500, gin.H{"error": "Ошибка сохранения"})
		return
	}

	logoURL := "/static/" + filename
	database.DB.Exec(`INSERT INTO settings (key, value) VALUES ('logo_url', $1) ON CONFLICT (key) DO UPDATE SET value = $1`, logoURL)

	c.JSON(200, gin.H{"success": true, "url": logoURL})
}

func adminUploadFavicon(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)

	file, err := c.FormFile("favicon")
	if err != nil {
		c.JSON(400, gin.H{"error": "Файл не загружен"})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".ico" && ext != ".png" {
		c.JSON(400, gin.H{"error": "Разрешены только .ico или .png"})
		return
	}

	if ext == ".png" {
		src, err := file.Open()
		if err == nil {
			header := make([]byte, 8)
			if _, err := io.ReadFull(src, header); err != nil || !isValidImageHeader(header) {
				src.Close()
				c.JSON(400, gin.H{"error": "Файл не является изображением"})
				return
			}
			src.Close()
		}
	}

	filename := "favicon.ico"
	if ext == ".png" {
		filename = "favicon.png"
	}
	dst := "./static/" + filename
	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(500, gin.H{"error": "Ошибка сохранения"})
		return
	}

	faviconURL := "/static/" + filename
	database.DB.Exec(`INSERT INTO settings (key, value) VALUES ('favicon_url', $1) ON CONFLICT (key) DO UPDATE SET value = $1`, faviconURL)

	c.JSON(200, gin.H{"success": true})
}

func adminResetSettings(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil || user.(*User).ID != 1 {
		c.JSON(403, gin.H{"error": "Доступ запрещён"})
		return
	}

	defaults := getDefaultSettings()
	for key, val := range defaults {
		database.DB.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = $2`, key, val)
	}

	// Очищаем историю изменений (опционально)
	// database.DB.Exec("DELETE FROM settings_history")

	c.JSON(200, gin.H{"success": true})
}
