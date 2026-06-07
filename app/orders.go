package main

import (
	"fmt"
	"net/http"
	"nexus-boost/database"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func sendMessage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	orderID := c.Param("id")
	if !orderParticipant(userID, orderID) {
		c.Redirect(302, "/profile?error=Доступ+запрещён")
		return
	}

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	text := strings.TrimSpace(c.PostForm("message"))

	if text != "" {
		database.DB.Exec(
			"INSERT INTO messages (order_id, user_id, username, text) VALUES ($1, $2, $3, $4)",
			orderID, userID, username, text,
		)

		var buyerID, boosterID int
		err := database.DB.QueryRow("SELECT user_id, booster_id FROM orders WHERE id = $1", orderID).Scan(&buyerID, &boosterID)
		if err == nil {
			notifyUserID := boosterID
			if userID == boosterID {
				notifyUserID = buyerID
			}
			if notifyUserID > 0 {
				notifText := fmt.Sprintf("💬 Новое сообщение в заказе #%s от %s", orderID, username)
				database.DB.Exec(
					"INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
					notifyUserID, notifText, "/order/"+orderID,
				)
				go sendPushNotification(notifyUserID, "Новое сообщение", notifText, "/order/"+orderID)
			}
		}
	}

	c.Redirect(302, "/order/"+orderID)
}

func rateOrder(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	orderID := c.Param("id")
	rating, _ := strconv.Atoi(c.PostForm("rating"))
	text := c.PostForm("review")

	var o Order
	database.DB.QueryRow("SELECT id, booster_id, rated FROM orders WHERE id = $1 AND user_id = $2", orderID, user.ID).
		Scan(&o.ID, &o.BoosterID, &o.Rated)

	if !o.Rated && rating >= 1 && rating <= 5 {
		// Добавляем отзыв
		database.DB.Exec(
			"INSERT INTO reviews (order_id, user_id, booster_id, username, rating, text) VALUES ($1,$2,$3,$4,$5,$6)",
			o.ID, user.ID, o.BoosterID, user.Username, rating, text,
		)

		// Обновляем рейтинг и количество отзывов у продавца
		var currentRating float64
		var currentReviews int
		database.DB.QueryRow("SELECT COALESCE(rating, 0), COALESCE(reviews, 0) FROM users WHERE id = $1", o.BoosterID).Scan(&currentRating, &currentReviews)

		newRating := (currentRating*float64(currentReviews) + float64(rating)) / float64(currentReviews+1)
		database.DB.Exec("UPDATE users SET rating = $1, reviews = $2, orders = orders + 1 WHERE id = $3", newRating, currentReviews+1, o.BoosterID)

		// Обновляем рейтинг и количество отзывов у товара
		var boostID int
		database.DB.QueryRow("SELECT boost_id FROM orders WHERE id = $1", orderID).Scan(&boostID)

		var boostRating float64
		var boostReviews int
		database.DB.QueryRow("SELECT COALESCE(rating, 0), COALESCE(reviews, 0) FROM boosts WHERE id = $1", boostID).
			Scan(&boostRating, &boostReviews)

		newBoostRating := (boostRating*float64(boostReviews) + float64(rating)) / float64(boostReviews+1)
		database.DB.Exec("UPDATE boosts SET rating = $1, reviews = $2 WHERE id = $3", newBoostRating, boostReviews+1, boostID)

		// Помечаем заказ как оцененный и выполненный
		database.DB.Exec("UPDATE orders SET rated = true, status = 'completed' WHERE id = $1", o.ID)

		// Уведомление продавцу
		notifText := fmt.Sprintf("⭐ Новый отзыв на заказ #%s", orderID)
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
			o.BoosterID, notifText, "/seller/orders")
		go sendPushNotification(o.BoosterID, "Новый отзыв", notifText, "/seller/orders")
	}

	c.Redirect(302, "/order/"+orderID)
}

func getMessages(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}
	orderID := c.Param("id")
	if !orderParticipant(user.ID, orderID) && !isAdminSession(c) {
		c.JSON(403, gin.H{"error": "Доступ запрещён"})
		return
	}

	rows, _ := database.DB.Query("SELECT username, text, created_at, user_id FROM messages WHERE order_id=$1 ORDER BY created_at ASC", orderID)
	if rows != nil {
		defer rows.Close()
	}
	var msgs []gin.H
	if rows != nil {
		for rows.Next() {
			var u, t string
			var ct time.Time
			var uid int
			rows.Scan(&u, &t, &ct, &uid)
			msgs = append(msgs, gin.H{"username": u, "text": t, "created_at": ct, "user_id": uid})
		}
	}
	c.JSON(200, msgs)
}

func updateOrderStatus(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("id")
	status := c.PostForm("status")

	var boosterID int
	database.DB.QueryRow("SELECT booster_id FROM orders WHERE id = $1", orderID).Scan(&boosterID)

	if boosterID == userID {
		database.DB.Exec("UPDATE orders SET status = $1 WHERE id = $2", status, orderID)

		// Получаем buyerID и username покупателя
		var buyerID int
		var buyerUsername string
		err := database.DB.QueryRow(
			"SELECT u.id, u.username FROM users u JOIN orders o ON u.id = o.user_id WHERE o.id = $1",
			orderID,
		).Scan(&buyerID, &buyerUsername)

		if err == nil && buyerID > 0 {
			// Уведомление покупателю
			notifText := fmt.Sprintf("📦 Статус заказа #%s изменен на: %s", orderID, status)
			database.DB.Exec(
				"INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
				buyerID, notifText, "/order/"+orderID,
			)
			go sendPushNotification(buyerID, "Статус заказа", notifText, "/order/"+orderID)
		}
	}

	c.Redirect(302, "/seller/orders")
}

// Админка

func orderPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username, email, balance FROM users WHERE id = $1", userID).
		Scan(&user.ID, &user.Username, &user.Email, &user.Balance)

	orderID := c.Param("id")

	var o Order
	var boosterName string
	err := database.DB.QueryRow(
		`SELECT o.id, o.user_id, o.boost_id, o.booster_id, o.title, o.game, o.total, o.status, o.rated, o.created_at, u.username 
         FROM orders o 
         LEFT JOIN users u ON o.booster_id = u.id 
         WHERE o.id = $1 AND o.user_id = $2`,
		orderID, userID,
	).Scan(&o.ID, &o.UserID, &o.BoostID, &o.BoosterID, &o.Title, &o.Game, &o.Total, &o.Status, &o.Rated, &o.CreatedAt, &boosterName)

	if err != nil {
		c.String(404, "Заказ не найден")
		return
	}

	o.Booster = boosterName

	// Сообщения чата
	rows, _ := database.DB.Query(
		"SELECT id, user_id, username, text, created_at FROM messages WHERE order_id = $1 ORDER BY created_at ASC",
		orderID,
	)
	if rows != nil {
		defer rows.Close()
	}
	var messages []Message
	if rows != nil {
		for rows.Next() {
			var m Message
			rows.Scan(&m.ID, &m.UserID, &m.Username, &m.Text, &m.CreatedAt)
			messages = append(messages, m)
		}
	}
	if messages == nil {
		messages = []Message{}
	}

	// Проверяем есть ли спор по этому заказу
	var disputeID int
	database.DB.QueryRow("SELECT COALESCE((SELECT id FROM disputes WHERE order_id = $1), 0)", orderID).Scan(&disputeID)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "order",
		"Data": gin.H{
			"Order":     o,
			"Messages":  messages,
			"DisputeID": disputeID,
		},
	})
}
