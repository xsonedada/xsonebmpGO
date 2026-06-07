package main

import (
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
)

func getNotificationsHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(200, []gin.H{})
		return
	}

	rows, _ := database.DB.Query(
		"SELECT id, text, link, is_read, created_at FROM notifications WHERE user_id = $1 AND is_read = false ORDER BY created_at DESC LIMIT 20",
		userID,
	)
	if rows != nil {
		defer rows.Close()
	}

	var notifs []gin.H
	if rows != nil {
		for rows.Next() {
			var id int
			var text, link string
			var isRead bool
			var createdAt time.Time
			rows.Scan(&id, &text, &link, &isRead, &createdAt)
			notifs = append(notifs, gin.H{"id": id, "text": text, "link": link, "created_at": createdAt})
		}
	}

	c.JSON(200, notifs)
}

func getNotificationsCountHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(200, gin.H{"count": 0})
		return
	}

	var count int
	database.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND is_read = false", userID).Scan(&count)
	c.JSON(200, gin.H{"count": count})
}
