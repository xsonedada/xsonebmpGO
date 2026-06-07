package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"nexus-boost/database"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	sessionTrackMu       sync.Mutex
	sessionLastTrack     = make(map[string]time.Time)
	sessionTrackDebounce = 5 * time.Minute
)

func trackUserSessionAsync(sessionUUID string, uid int, ip, ua, device string, isNew bool) {
	sessionTrackMu.Lock()
	last, ok := sessionLastTrack[sessionUUID]
	if ok && time.Since(last) < sessionTrackDebounce {
		sessionTrackMu.Unlock()
		return
	}
	sessionLastTrack[sessionUUID] = time.Now()
	sessionTrackMu.Unlock()

	go func() {
		loc := "Неизвестно"
		if isNew {
			loc = getLocationByIP(ip)
		} else {
			_ = database.DB.QueryRow(
				"SELECT COALESCE(NULLIF(location, ''), 'Неизвестно') FROM user_sessions WHERE session_token = $1",
				sessionUUID,
			).Scan(&loc)
		}

		database.DB.Exec(`
            INSERT INTO user_sessions (session_token, user_id, ip, user_agent, location, device, last_seen)
            VALUES ($1, $2, $3, $4, $5, $6, NOW())
            ON CONFLICT (session_token) DO UPDATE 
                SET ip = EXCLUDED.ip,
                    user_agent = EXCLUDED.user_agent,
                    location = CASE WHEN user_sessions.location = '' OR user_sessions.location IS NULL
                               THEN EXCLUDED.location ELSE user_sessions.location END,
                    device = EXCLUDED.device,
                    last_seen = NOW()
        `, sessionUUID, uid, ip, ua, loc, device)
	}()
}

func getLocationByIP(ip string) string {
	if ip == "::1" || ip == "127.0.0.1" {
		return "Локально"
	}

	resp, err := httpClient.Get(fmt.Sprintf("http://ip-api.com/json/%s?fields=city,country", ip))
	if err != nil {
		return "Неизвестно"
	}
	defer resp.Body.Close()

	var result struct {
		City    string `json:"city"`
		Country string `json:"country"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.City != "" {
		return result.City + ", " + result.Country
	}
	return "Неизвестно"
}

func sessionsPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).
		Scan(&user.ID, &user.Username)

	rows, _ := database.DB.Query(`
        SELECT id, session_token, ip, user_agent, COALESCE(location, ''), COALESCE(device, ''), is_active, created_at, last_seen 
        FROM user_sessions WHERE user_id = $1 AND revoked = false
        ORDER BY last_seen DESC LIMIT 20
    `, userID)
	defer rows.Close()

	type SessionInfo struct {
		ID           int
		SessionToken string
		IP           string
		UA           string
		Location     string
		Device       string
		IsActive     bool
		CreatedAt    time.Time
		LastSeen     time.Time
	}

	var sessions []SessionInfo
	for rows.Next() {
		var s SessionInfo
		rows.Scan(&s.ID, &s.SessionToken, &s.IP, &s.UA, &s.Location, &s.Device, &s.IsActive, &s.CreatedAt, &s.LastSeen)
		sessions = append(sessions, s)
	}

	currentUUID, _ := session.Values["session_uuid"].(string)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":               &user,
		"Active":             "sessions",
		"Sessions":           sessions,
		"CurrentSessionUUID": currentUUID,
	})
}

func getDeviceInfo(ua string) string {
	ua = strings.ToLower(ua)
	if strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") {
		return "📱 iPhone"
	}
	if strings.Contains(ua, "android") {
		return "📱 Android"
	}
	if strings.Contains(ua, "windows") {
		return "💻 Windows"
	}
	if strings.Contains(ua, "mac") {
		return "💻 Mac"
	}
	return "🖥️ Устройство"
}

func revokeSession(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	sessionID := c.Param("id")
	database.DB.Exec("UPDATE user_sessions SET revoked = true WHERE id = $1 AND user_id = $2", sessionID, userID)

	// Если завершаем текущую сессию – разлогиниваем
	currentUUID, _ := session.Values["session_uuid"].(string)
	var revokedUUID string
	database.DB.QueryRow("SELECT session_token FROM user_sessions WHERE id = $1", sessionID).Scan(&revokedUUID)
	if currentUUID == revokedUUID {
		session.Values = make(map[interface{}]interface{})
		session.Save(c.Request, c.Writer)
		c.Redirect(302, "/login")
		return
	}

	c.Redirect(302, "/sessions?success=Сессия+завершена")
}
