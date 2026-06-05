package handlers

import (
	"nexus-boost/database"

	"github.com/gin-gonic/gin"
)

// Получить все уведомления пользователя
func GetNotifications(c *gin.Context) {
	session, _ := Store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(200, []gin.H{})
		return
	}

	rows, err := database.DB.Query(
		"SELECT id, text, link, is_read, created_at FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT 20",
		userID,
	)
	if err != nil {
		c.JSON(200, []gin.H{})
		return
	}
	defer rows.Close()

	var notifications []gin.H
	for rows.Next() {
		var id int
		var text, link string
		var isRead bool
		var createdAt interface{}
		rows.Scan(&id, &text, &link, &isRead, &createdAt)
		notifications = append(notifications, gin.H{
			"id":         id,
			"text":       text,
			"link":       link,
			"is_read":    isRead,
			"created_at": createdAt,
		})
	}

	c.JSON(200, notifications)
}

// Получить количество непрочитанных
func GetNotificationsCount(c *gin.Context) {
	session, _ := Store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(200, gin.H{"count": 0})
		return
	}

	var count int
	database.DB.QueryRow(
		"SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND is_read = false",
		userID,
	).Scan(&count)

	c.JSON(200, gin.H{"count": count})
}

// Отметить все прочитанными
func MarkAllNotificationsRead(c *gin.Context) {
	session, _ := Store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"success": false})
		return
	}

	database.DB.Exec("UPDATE notifications SET is_read = true WHERE user_id = $1", userID)
	c.JSON(200, gin.H{"success": true})
}

// Отметить одно уведомление прочитанным
func MarkOneNotificationRead(c *gin.Context) {
	session, _ := Store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"success": false})
		return
	}

	notifID := c.Param("id")
	database.DB.Exec("UPDATE notifications SET is_read = true WHERE id = $1 AND user_id = $2", notifID, userID)
	c.JSON(200, gin.H{"success": true})
}

// Отправить уведомление конкретному пользователю
func SendNotification(userID int, text, link string) {
	database.DB.Exec(
		"INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
		userID, text, link,
	)
}
