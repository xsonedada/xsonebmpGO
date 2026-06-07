package main

import (
	"nexus-boost/database"

	"github.com/gin-gonic/gin"
)

func getReviewLikes(c *gin.Context) {
	reviewID := c.Param("id")

	var count int
	database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1", reviewID).Scan(&count)

	// Проверяем лайкнул ли текущий пользователь
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	var liked bool
	if userID != nil {
		var exists int
		database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1 AND user_id = $2", reviewID, userID).Scan(&exists)
		liked = exists > 0
	}

	c.JSON(200, gin.H{"count": count, "liked": liked})
}

func toggleReviewLike(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}

	reviewID := c.Param("id")
	userIDint := userID.(int)

	// Проверяем есть ли лайк
	var exists int
	database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1 AND user_id = $2", reviewID, userIDint).Scan(&exists)

	if exists > 0 {
		// Убираем лайк
		database.DB.Exec("DELETE FROM review_likes WHERE review_id = $1 AND user_id = $2", reviewID, userIDint)
	} else {
		// Ставим лайк
		database.DB.Exec("INSERT INTO review_likes (review_id, user_id) VALUES ($1,$2)", reviewID, userIDint)
	}

	var count int
	database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1", reviewID).Scan(&count)

	c.JSON(200, gin.H{"count": count, "liked": exists == 0})
}

// Страница тикетов пользователя
