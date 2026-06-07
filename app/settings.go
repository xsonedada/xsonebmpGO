package main

import (
	"nexus-boost/database"
	"sync"
	"time"
)

var (
	cachedStats     map[string]interface{}
	cachedStatsTime time.Time
	statsMutex      sync.Mutex
)

func getCachedStats() map[string]interface{} {
	statsMutex.Lock()
	defer statsMutex.Unlock()

	if time.Since(cachedStatsTime) < 5*time.Minute && cachedStats != nil {
		return cachedStats
	}

	var usersCount, ordersCount int
	var avgRating float64
	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&ordersCount)
	database.DB.QueryRow("SELECT COALESCE(AVG(rating), 0) FROM users WHERE rating > 0").Scan(&avgRating)

	cachedStats = map[string]interface{}{
		"users":  usersCount,
		"orders": ordersCount,
		"rating": avgRating,
	}
	cachedStatsTime = time.Now()
	return cachedStats
}

var (
	settingsCache   map[string]string
	settingsCacheMu sync.RWMutex
)

func loadSettingsToCache() {
	settingsCacheMu.Lock()
	defer settingsCacheMu.Unlock()
	settingsCache = loadAllSettings() // ваша существующая функция
}

// Вместо прямых запросов к БД в middleware используйте:

func getCachedSetting(key string) string {
	settingsCacheMu.RLock()
	defer settingsCacheMu.RUnlock()
	if val, ok := settingsCache[key]; ok {
		return val
	}
	return ""
}

func loadAllSettings() map[string]string {
	rows, err := database.DB.Query("SELECT key, value FROM settings")
	if err != nil {
		// если таблицы ещё нет – создадим позже, но пока вернём дефолты
		return getDefaultSettings()
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, val string
		rows.Scan(&key, &val)
		settings[key] = val
	}

	// Заполняем недостающие ключи значениями по умолчанию
	defaults := getDefaultSettings()
	for k, v := range defaults {
		if _, ok := settings[k]; !ok {
			settings[k] = v
			// сразу сохраняем в БД, чтобы потом не проверять каждый раз
			database.DB.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, k, v)
		}
	}
	return settings
}

func getDefaultSettings() map[string]string {
	return map[string]string{
		"site_name":            "XSoneBMP",
		"site_description":     "Маркетплейс игровых услуг",
		"site_keywords":        "boost, mmr, игра, услуги",
		"contact_email":        "support@xsonebmp.ru",
		"platform_fee":         "5",
		"min_withdraw":         "100",
		"currency":             "₽",
		"referral_percent":     "5",
		"referral_level2":      "2",
		"session_hours":        "720",
		"max_orders_per_day":   "0",
		"enable_registration":  "true",
		"recaptcha_site_key":   "",
		"recaptcha_secret_key": "",
		"smtp_host":            "",
		"smtp_port":            "587",
		"smtp_user":            "",
		"smtp_password":        "",
		"smtp_from":            "",
		"ga_id":                "",
		"ym_id":                "",
		"telegram_bot_token":   "",
		"telegram_chat_id":     "",
		"webhook_url":          "",
		"logo_url":             "/static/logo.png",
		"favicon_url":          "/static/favicon.ico",
		"primary_color":        "#ef4444",
		"secondary_color":      "#8b5cf6",
		"cache_timeout":        "300",
		"maintenance_mode":     "false",
		"maintenance_message":  "Технические работы. Скоро вернемся!",
	}
}
