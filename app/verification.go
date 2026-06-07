package main

import (
	"net/http"
	"nexus-boost/database"

	"github.com/gin-gonic/gin"
)

func verificationPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	// Проверяем есть ли уже заявка
	var pendingCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM verification_requests WHERE user_id = $1 AND status = 'pending'", userID).Scan(&pendingCount)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "verification",
		"Data":   gin.H{"HasPending": pendingCount > 0},
	})
}

// Подача заявки

func applyVerification(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	fullName := c.PostForm("full_name")
	description := c.PostForm("description")
	contact := c.PostForm("contact")

	database.DB.Exec(
		"INSERT INTO verification_requests (user_id, full_name, description, contact) VALUES ($1,$2,$3,$4)",
		userID, fullName, description, contact,
	)

	// Уведомление админам
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES (1, '📝 Новая заявка на верификацию!', '/admin/verifications')")

	c.Redirect(302, "/profile?success=Заявка+отправлена")
}

// Админка - заявки на верификацию
