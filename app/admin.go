package main

import (
	"net/http"
	"nexus-boost/database"
	"strconv"

	"github.com/gin-gonic/gin"
)

func adminUpdateBalance(c *gin.Context) {
	userID := c.Param("id")
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	operation := c.PostForm("operation")

	if operation == "add" {
		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", amount, userID)
	} else if operation == "set" {
		database.DB.Exec("UPDATE users SET balance = $1 WHERE id = $2", amount, userID)
	}

	c.Redirect(302, "/admin/users")
}

// Бан/разбан пользователя

func adminViewMessages(c *gin.Context) {
	orderID := c.Param("id")
	user, _ := c.Get("user")

	rows, _ := database.DB.Query(
		"SELECT id, username, text, created_at FROM messages WHERE order_id = $1 ORDER BY created_at ASC",
		orderID,
	)
	if rows != nil {
		defer rows.Close()
	}

	var messages []Message
	if rows != nil {
		for rows.Next() {
			var m Message
			rows.Scan(&m.ID, &m.Username, &m.Text, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "admin-messages",
		"Data": gin.H{
			"OrderID":  orderID,
			"Messages": messages,
		},
	})
}

// Страница добавления товара админом

func adminUpdateFirstDiscount(c *gin.Context) {
	discount, _ := strconv.Atoi(c.PostForm("discount"))
	isActive := c.PostForm("is_active") == "on"

	database.DB.Exec("UPDATE first_order_discount SET discount_percent = $1, is_active = $2", discount, isActive)
	c.Redirect(302, "/admin/sales")
}

// Страница открытия диспута
